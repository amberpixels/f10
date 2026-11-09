package main

import (
	"context"
	"errors"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// pickupFor is the PR the start argument names, or nil for a task id; a
// bare #n the host knows as no PR is a task id.
func pickupFor(ctx context.Context, t *target, token string) (*pickup, forge.PR, error) {
	number, probe := pullToken(token)
	if number == "" {
		return nil, forge.PR{}, nil
	}

	h, err := t.host()

	switch {
	case err != nil && probe:
		return nil, forge.PR{}, nil
	case err != nil:
		return nil, forge.PR{}, err
	case probe && !h.SharedNumbers():
		// GitLab numbers issues and MRs apart: #n is an issue there, !n an MR
		return nil, forge.PR{}, nil
	}

	p, err := h.PR(ctx, number)

	switch {
	case err != nil && probe && errors.Is(err, forge.ErrNotFound):
		return nil, forge.PR{}, nil
	case err != nil:
		return nil, forge.PR{}, err
	}

	if err := refusePull(p); err != nil {
		return nil, forge.PR{}, err
	}

	return h.pickupOf(number, p), p, nil
}

// pickedTask is the task id for a picked-up PR: --task, else the one the
// PR names. A PR naming none exits with ui.ExitDeclined, which the start skill
// turns into a question; --task with no PR is a refusal.
func pickedTask(prefix string, hostIssues bool, pick *pickup, pr forge.PR, task string) (string, error) {
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
