package main

import (
	"context"
	"errors"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// pickupFor is the PR the start argument names, or nil for a task id; a
// bare #n the host knows as no PR is a task id.
func pickupFor(ctx context.Context, t *target, token string) (*pickup, pull, error) {
	number, probe := pullToken(token)
	if number == "" {
		return nil, pull{}, nil
	}

	h, err := t.host()

	switch {
	case err != nil && probe:
		return nil, pull{}, nil
	case err != nil:
		return nil, pull{}, err
	case probe && h.GLab():
		// GitLab numbers issues and MRs apart: #n is an issue there, !n an MR
		return nil, pull{}, nil
	}

	p, err := h.pullNumber(ctx, number)

	switch {
	case err != nil && probe && errors.Is(err, forge.ErrNotFound):
		return nil, pull{}, nil
	case err != nil:
		return nil, pull{}, err
	}

	if err := refusePull(p); err != nil {
		return nil, pull{}, err
	}

	return h.pickupOf(number, p), p, nil
}

// pickedTask is the task id for a picked-up PR: --task, else the one the
// PR names. A PR naming none exits with ui.ExitDeclined, which the start skill
// turns into a question; --task with no PR is a refusal.
func pickedTask(prefix string, hostIssues bool, pick *pickup, pr pull, task string) (string, error) {
	switch {
	case pick == nil && task != "":
		return "", errors.New("--task names the task of a PR: pass the PR's link or #n")
	case pick == nil || task != "":
		return task, nil
	}

	if task = pickupTask(prefix, hostIssues, pr); task == "" {
		return "", ui.Exit(ui.ExitDeclined, "%s names no task in its body, title or branch %s: "+
			"rerun with --task <ID>", pick.url, pick.branch)
	}

	return task, nil
}
