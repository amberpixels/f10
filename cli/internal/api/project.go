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

	for _, w := range sup.Status().Watches {
		if target.ProjectOf(ctx, w.Dir) == project || filepath.Base(w.Dir) == base {
			return w.Dir, nil
		}
	}

	dir, err := projects.Find(base)
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
