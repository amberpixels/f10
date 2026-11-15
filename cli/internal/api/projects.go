package api

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/target"
)

type projectService struct{ sup Supervisor }

// ListProjects is every project the daemon watches or runs a drive in, once
// each by id, since a drive's checkout is often a worktree of a watched one.
func (s *projectService) ListProjects(
	ctx context.Context,
	_ *connect.Request[f10v1.ListProjectsRequest],
) (*connect.Response[f10v1.ListProjectsResponse], error) {
	st := s.sup.Status()
	byID := map[string]*f10v1.KnownProject{}

	add := func(dir string, watched bool) {
		if dir == "" {
			return
		}

		id := cmp.Or(target.ProjectOf(ctx, dir), dir)

		p := byID[id]
		if p == nil {
			p = &f10v1.KnownProject{Id: id, Name: filepath.Base(id)}
			byID[id] = p
		}

		p.Watched = p.GetWatched() || watched
	}

	watched := map[string]bool{}
	for _, w := range st.Watches {
		watched[w.Dir] = true
	}

	// the same checkouts projectDir resolves, so every listed id resolves
	for _, dir := range knownDirs(st) {
		add(dir, watched[dir])
	}

	out := make([]*f10v1.KnownProject, 0, len(byID))
	for _, p := range byID {
		out = append(out, p)
	}

	slices.SortFunc(out, func(a, b *f10v1.KnownProject) int { return cmp.Compare(a.GetId(), b.GetId()) })

	return connect.NewResponse(&f10v1.ListProjectsResponse{Projects: out}), nil
}
