package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"

	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/target"
)

// projectDir is the checkout a request's project names: a path as given,
// else a project id or name matched against the projects the daemon
// watches, else against F10_ROOTS.
func projectDir(ctx context.Context, sup Supervisor, project string) (string, error) {
	project = strings.TrimSpace(project)

	switch {
	case project == "":
		return "", invalid("the request names no project: pass a project id or the path of its main checkout")
	case filepath.IsAbs(project):
		return project, nil
	}

	base := filepath.Base(project)
	watches := sup.Status().Watches

	// an exact project id wins over a directory name two repos may share
	var named []string

	for _, w := range watches {
		if target.ProjectOf(ctx, w.Dir) == project {
			return w.Dir, nil
		}

		if filepath.Base(w.Dir) == base {
			named = append(named, w.Dir)
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
