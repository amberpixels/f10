package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"

	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/target"
)

// projectDir is the checkout a request's project names: a path as given,
// else a project id or name matched against the projects the daemon
// watches or runs a drive in, which ListProjects offers, else against
// F10_ROOTS.
func projectDir(ctx context.Context, sup Supervisor, project string) (string, error) {
	project = strings.TrimSpace(project)

	switch {
	case project == "":
		return "", invalid("the request names no project: pass a project id or the path of its main checkout")
	case filepath.IsAbs(project):
		return project, nil
	}

	base := filepath.Base(project)

	// an exact project id wins over a directory name two repos may share
	var named []string

	for _, dir := range knownDirs(sup.Status()) {
		if target.ProjectOf(ctx, dir) == project {
			return dir, nil
		}

		if filepath.Base(dir) == base {
			named = append(named, dir)
		}
	}

	// a full id names one project, so a checkout that merely shares its last
	// segment is another project's
	full := strings.Contains(project, "/")

	switch {
	case len(named) == 1 && !full:
		return named[0], nil
	case len(named) > 1 && !full:
		return "", invalid(fmt.Sprintf("project %s names %d watched checkouts (%s): pass its path or its full id",
			project, len(named), strings.Join(named, ", ")))
	}

	dir, err := projects.Find(base)
	if err == nil && full && target.ProjectOf(ctx, dir) != project {
		err = fmt.Errorf("the checkout named %s under F10_ROOTS is %s", base, target.ProjectOf(ctx, dir))
	}

	if err != nil {
		return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("project %s: %w", project, err))
	}

	return dir, nil
}

// projectTarget is the project a request names, read as a command run in
// its checkout reads it.
func projectTarget(ctx context.Context, sup Supervisor, project, verb string) (*target.Target, error) {
	dir, err := projectDir(ctx, sup, project)
	if err != nil {
		return nil, err
	}

	t, err := target.At(ctx, dir, true, verb)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.Join(fmt.Errorf("project %s", project), err))
	}

	return t, nil
}

// knownDirs is every checkout the daemon watches or runs a drive in, once
// each, watched ones first.
func knownDirs(st daemon.Status) []string {
	var dirs []string

	for _, w := range st.Watches {
		dirs = append(dirs, w.Dir)
	}

	for _, r := range st.Runs {
		dirs = append(dirs, r.Dir)
	}

	seen := map[string]bool{}
	out := dirs[:0]

	for _, d := range dirs {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}

	return out
}
