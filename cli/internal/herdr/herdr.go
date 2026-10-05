// Package herdr is the binary's door to Herdr, the terminal multiplexer
// `f10 start` opens a task's worktree in and `f10 finish` closes it from.
// Six calls, all over the herdr CLI: open a worktree as a workspace, start
// an agent in its root pane, hand that agent a prompt, list the
// workspaces, focus one, close one. Every answer is JSON, and the ids the
// next call needs are read from it rather than predicted.
package herdr

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// ErrNotInside is the one refusal: start and finish run inside a Herdr
// session and nowhere else, by decision rather than by accident of a
// missing fallback.
var ErrNotInside = errors.New("f10 start and finish run inside a Herdr session: " +
	"open a pane there, or install it from https://herdr.dev")

// Available reports whether this process can reach Herdr: the session
// marks itself in the environment, and the CLI has to be on PATH.
func Available() error {
	if os.Getenv("HERDR_ENV") != "1" || !shell.Has("herdr") {
		return ErrNotInside
	}

	return nil
}

// A Workspace is what opening a worktree yields: the ids the agent calls
// are addressed by.
type Workspace struct {
	ID          string
	RootPane    string
	AlreadyOpen bool // Herdr was showing this checkout before the call
}

// OpenWorktree turns the checkout at path into a workspace labelled label,
// or returns the one already showing it.
func OpenWorktree(ctx context.Context, dir, path, label string) (Workspace, error) {
	res, err := call(ctx, dir, "worktree", "open", "--path", path, "--label", label)
	if err != nil {
		return Workspace{}, err
	}

	ws := Workspace{
		ID:          field(res, "workspace", "workspace_id"),
		RootPane:    field(res, "root_pane", "pane_id"),
		AlreadyOpen: res["already_open"] == true,
	}

	switch {
	case ws.ID == "":
		return Workspace{}, errors.New("herdr worktree open: no workspace.workspace_id in the response")
	case ws.RootPane == "":
		return Workspace{}, errors.New("herdr worktree open: no root_pane.pane_id in the response")
	}

	return ws, nil
}

// StartAgent launches an agent of kind in pane under name. It returns once
// Herdr sees the agent ready for input, which is herdr's own wait, not ours.
func StartAgent(ctx context.Context, dir, name, kind, pane string) error {
	_, err := call(ctx, dir, "agent", "start", name, "--kind", kind, "--pane", pane)

	return err
}

// Prompt submits text to the named agent and returns as soon as it is
// submitted. Never --wait: the caller may itself be an agent in Herdr.
func Prompt(ctx context.Context, dir, name, text string) error {
	_, err := call(ctx, dir, "agent", "prompt", name, text)

	return err
}

// A Listed is one workspace as `workspace list` reports it. Path is the
// checkout the workspace shows, or "" for a workspace with no worktree.
type Listed struct {
	ID      string
	Label   string
	Path    string
	Focused bool
	Agent   string // herdr's agent_status: idle, working, done, unknown
}

// Workspaces lists every workspace the session shows.
func Workspaces(ctx context.Context, dir string) ([]Listed, error) {
	res, err := call(ctx, dir, "workspace", "list")
	if err != nil {
		return nil, err
	}

	raw, _ := res["workspaces"].([]any)

	list := make([]Listed, 0, len(raw))

	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}

		list = append(list, Listed{
			ID:      field(obj, "workspace_id"),
			Label:   field(obj, "label"),
			Path:    field(obj, "worktree", "checkout_path"),
			Focused: obj["focused"] == true,
			Agent:   field(obj, "agent_status"),
		})
	}

	return list, nil
}

// FocusWorkspace brings the workspace to the front.
func FocusWorkspace(ctx context.Context, dir, id string) error {
	_, err := call(ctx, dir, "workspace", "focus", id)

	return err
}

// CloseWorkspace closes the workspace, its tabs and panes, and whatever
// agent ran in them.
func CloseWorkspace(ctx context.Context, dir, id string) error {
	_, err := call(ctx, dir, "workspace", "close", id)

	return err
}

// call runs one herdr command and returns its `result` object. A failure
// is JSON on stderr; its code and message are what the user needs.
func call(ctx context.Context, dir string, args ...string) (map[string]any, error) {
	res, err := shell.Capture(ctx, dir, "herdr", args...)
	if err != nil {
		return nil, fmt.Errorf("running herdr %s: %w", args[0], err)
	}

	what := "herdr " + strings.Join(args[:min(2, len(args))], " ")

	if res.Code != 0 {
		return nil, fmt.Errorf(
			"%s: %s",
			what,
			cmp.Or(failure(res.Stderr), res.Stderr, fmt.Sprintf("exit %d", res.Code)),
		)
	}

	var env struct {
		Result map[string]any `json:"result"`
	}

	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		return nil, fmt.Errorf("parsing %s output: %w", what, err)
	}

	return env.Result, nil
}

// failure reads herdr's error envelope, or returns "" when stderr is not one.
func failure(stderr string) string {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if json.Unmarshal([]byte(stderr), &env) != nil || env.Error.Message == "" {
		return ""
	}

	if env.Error.Code == "" {
		return env.Error.Message
	}

	return env.Error.Code + ": " + env.Error.Message
}

// field walks a nested object and returns the string at the path, or "".
func field(obj map[string]any, path ...string) string {
	var cur any = obj

	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}

		cur = m[key]
	}

	s, _ := cur.(string)

	return s
}
