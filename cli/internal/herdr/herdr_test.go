package herdr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// fake answers shell.Capture for herdr with one scripted result and records
// the arguments it was called with.
func fake(t *testing.T, res shell.Result) *[]string {
	t.Helper()

	var calls []string

	prev := shell.Capture
	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))

		return res, nil
	}

	t.Cleanup(func() { shell.Capture = prev })

	return &calls
}

func TestAvailable(t *testing.T) {
	prev := shell.Has
	shell.Has = func(string) bool { return true }

	t.Cleanup(func() { shell.Has = prev })

	t.Setenv("HERDR_ENV", "")

	if err := Available(); !errors.Is(err, ErrNotInside) {
		t.Errorf("Available outside herdr = %v, want ErrNotInside", err)
	}

	t.Setenv("HERDR_ENV", "1")

	if err := Available(); err != nil {
		t.Errorf("Available inside herdr = %v", err)
	}

	shell.Has = func(string) bool { return false }

	if err := Available(); !errors.Is(err, ErrNotInside) {
		t.Errorf("Available without the binary = %v, want ErrNotInside", err)
	}
}

func TestOpenWorktreeReadsIds(t *testing.T) {
	calls := fake(t, shell.Result{Stdout: `{"id":"x","result":{"type":"worktree_opened",` +
		`"workspace":{"workspace_id":"ws:3","label":"GH-1"},"root_pane":{"pane_id":"pane:7"},"already_open":false}}`})

	ws, err := OpenWorktree(t.Context(), "/repo", "/repo.GH-1", "GH-1")
	if err != nil {
		t.Fatal(err)
	}

	if ws.ID != "ws:3" || ws.RootPane != "pane:7" || ws.AlreadyOpen {
		t.Errorf("OpenWorktree = %+v, want ws:3/pane:7, not already open", ws)
	}

	if want := "herdr worktree open --path /repo.GH-1 --label GH-1"; (*calls)[0] != want {
		t.Errorf("called %q, want %q", (*calls)[0], want)
	}
}

func TestOpenWorktreeReportsAlreadyOpen(t *testing.T) {
	fake(
		t,
		shell.Result{
			Stdout: `{"result":{"workspace":{"workspace_id":"ws:3"},"root_pane":{"pane_id":"pane:7"},"already_open":true}}`,
		},
	)

	ws, err := OpenWorktree(t.Context(), "/repo", "/p", "l")
	if err != nil {
		t.Fatal(err)
	}

	if !ws.AlreadyOpen {
		t.Error("already_open was not read")
	}
}

func TestOpenWorktreeMissingField(t *testing.T) {
	fake(t, shell.Result{Stdout: `{"result":{"workspace":{"workspace_id":"ws:3"}}}`})

	_, err := OpenWorktree(t.Context(), "/repo", "/p", "l")
	if err == nil || !strings.Contains(err.Error(), "root_pane.pane_id") {
		t.Errorf("err = %v, want it to name the missing field", err)
	}
}

func TestFailureCarriesHerdrsMessage(t *testing.T) {
	fake(
		t,
		shell.Result{
			Code:   1,
			Stderr: `{"id":"x","error":{"code":"worktree_open_failed","message":"not a git worktree"}}`,
		},
	)

	err := StartAgent(t.Context(), "/repo", "gh-1", "claude", "pane:7")
	if err == nil || !strings.Contains(err.Error(), "worktree_open_failed: not a git worktree") {
		t.Errorf("err = %v, want herdr's code and message", err)
	}

	fake(t, shell.Result{Code: 1, Stderr: "plain text"})

	err = Prompt(t.Context(), "/repo", "gh-1", "hi")
	if err == nil || !strings.Contains(err.Error(), "plain text") {
		t.Errorf("err = %v, want the raw stderr", err)
	}
}

func TestWorkspacesReadsTheListing(t *testing.T) {
	calls := fake(
		t,
		shell.Result{Stdout: `{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[` +
			`{"workspace_id":"wJA","label":"f10","focused":false,"agent_status":"done",` +
			`"worktree":{"checkout_path":"/code/f10","is_linked_worktree":false}},` +
			`{"workspace_id":"wJ2","label":"p44","focused":true}]}}`},
	)

	list, err := Workspaces(t.Context(), "/code/f10")
	if err != nil {
		t.Fatal(err)
	}

	if len(list) != 2 {
		t.Fatalf("Workspaces = %+v, want two", list)
	}

	if list[0] != (Listed{ID: "wJA", Label: "f10", Path: "/code/f10", Agent: "done"}) {
		t.Errorf("first = %+v", list[0])
	}

	if list[1] != (Listed{ID: "wJ2", Label: "p44", Focused: true}) {
		t.Errorf("second = %+v, want no path for a workspace without a worktree", list[1])
	}

	if want := "herdr workspace list"; (*calls)[0] != want {
		t.Errorf("called %q, want %q", (*calls)[0], want)
	}
}

func TestFocusAndCloseAddressTheWorkspace(t *testing.T) {
	calls := fake(t, shell.Result{Stdout: `{"result":{}}`})

	if err := FocusWorkspace(t.Context(), "/repo", "ws:1"); err != nil {
		t.Fatal(err)
	}

	if err := CloseWorkspace(t.Context(), "/repo", "ws:9"); err != nil {
		t.Fatal(err)
	}

	if (*calls)[0] != "herdr workspace focus ws:1" || (*calls)[1] != "herdr workspace close ws:9" {
		t.Errorf("calls = %v", *calls)
	}

	fake(t, shell.Result{Code: 1, Stderr: `{"error":{"code":"workspace_not_found","message":"no workspace ws:9"}}`})

	err := CloseWorkspace(t.Context(), "/repo", "ws:9")
	if err == nil || !strings.Contains(err.Error(), "workspace_not_found: no workspace ws:9") {
		t.Errorf("err = %v, want herdr's code and message", err)
	}
}
