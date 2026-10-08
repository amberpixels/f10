package herdr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// fake answers shell.Capture for herdr with scripted results, one per call
// with the last repeating, and records the arguments it was called with.
func fake(t *testing.T, res ...shell.Result) *[]string {
	t.Helper()

	var calls []string

	prev := shell.Capture
	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))

		next := res[0]
		if len(res) > 1 {
			res = res[1:]
		}

		return next, nil
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

	if want := "herdr worktree open --cwd /repo --path /repo.GH-1 --label GH-1"; (*calls)[0] != want {
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

	err := StartAgent(t.Context(), "/repo", "gh-1", "claude", "pane:7", nil)
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

func TestPanesReadsTheListing(t *testing.T) {
	calls := fake(
		t,
		shell.Result{Stdout: `{"id":"cli:pane:list","result":{"type":"pane_list","panes":[` +
			`{"pane_id":"wM6:p1","agent":"claude","agent_status":"idle","workspace_id":"wM6"},` +
			`{"pane_id":"wM6:p2","workspace_id":"wM6"}]}}`},
	)

	panes, err := Panes(t.Context(), "/code/f10", "wM6")
	if err != nil {
		t.Fatal(err)
	}

	want := []Pane{{ID: "wM6:p1", Agent: "claude"}, {ID: "wM6:p2"}}
	if len(panes) != len(want) || panes[0] != want[0] || panes[1] != want[1] {
		t.Errorf("Panes = %+v, want %+v", panes, want)
	}

	if want := "herdr pane list --workspace wM6"; (*calls)[0] != want {
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

// busy is herdr's answer while the pane's shell is still starting.
var busy = shell.Result{
	Code:   1,
	Stderr: `{"error":{"code":"agent_pane_busy","message":"agent target pane pane:7 is not an available shell"}}`,
}

// shortWait shrinks StartAgent's wait so a test never sits it out.
func shortWait(t *testing.T, deadline time.Duration) {
	t.Helper()

	prevDeadline, prevPoll := StartDeadline, StartPoll
	StartDeadline, StartPoll = deadline, time.Millisecond

	t.Cleanup(func() { StartDeadline, StartPoll = prevDeadline, prevPoll })
}

func TestErrorCarriesCodeAndMessage(t *testing.T) {
	fake(t, busy)
	shortWait(t, 0)

	err := StartAgent(t.Context(), "/repo", "gh-1", "claude", "pane:7", nil)

	var he *Error
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want an *Error", err)
	}

	if he.Code != CodePaneBusy || he.Op != "herdr agent start" || !IsCode(err, CodePaneBusy) {
		t.Errorf("Error = %+v", he)
	}

	if want := "herdr agent start: agent_pane_busy: agent target pane pane:7 is not an available shell"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}

	if IsCode(errors.New("agent_pane_busy"), CodePaneBusy) {
		t.Error("a plain error matched a herdr code")
	}
}

func TestStartAgentWaitsOutABusyPane(t *testing.T) {
	calls := fake(t, busy, busy, shell.Result{Stdout: `{"result":{}}`})
	shortWait(t, time.Minute)

	if err := StartAgent(t.Context(), "/repo", "gh-1", "claude", "pane:7", nil); err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 3 {
		t.Errorf("calls = %v, want three tries", *calls)
	}
}

func TestStartAgentGivesUpAtTheDeadline(t *testing.T) {
	calls := fake(t, busy)
	shortWait(t, 20*time.Millisecond)

	err := StartAgent(t.Context(), "/repo", "gh-1", "claude", "pane:7", nil)
	if !IsCode(err, CodePaneBusy) {
		t.Errorf("err = %v, want herdr's busy error", err)
	}

	if len(*calls) < 2 {
		t.Errorf("calls = %v, want it retried before giving up", *calls)
	}
}

func TestStartAgentReturnsOtherErrorsAtOnce(t *testing.T) {
	calls := fake(t, shell.Result{Code: 1, Stderr: `{"error":{"code":"pane_not_found","message":"no pane pane:7"}}`})
	shortWait(t, time.Minute)

	err := StartAgent(t.Context(), "/repo", "gh-1", "claude", "pane:7", nil)
	if !IsCode(err, "pane_not_found") || len(*calls) != 1 {
		t.Errorf("err = %v after %d calls, want pane_not_found after one", err, len(*calls))
	}
}

func TestStartAgentStopsWithTheContext(t *testing.T) {
	fake(t, busy)
	shortWait(t, time.Minute)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := StartAgent(ctx, "/repo", "gh-1", "claude", "pane:7", nil)
	if !errors.Is(err, context.Canceled) || !IsCode(err, CodePaneBusy) {
		t.Errorf("err = %v, want the busy error joined with the cancellation", err)
	}
}

func TestAgentExists(t *testing.T) {
	calls := fake(t, shell.Result{Stdout: `{"result":{"agent":{"name":"gh-1"}}}`})

	if ok, err := AgentExists(t.Context(), "/repo", "gh-1"); !ok || err != nil {
		t.Errorf("AgentExists = %v, %v, want true", ok, err)
	}

	if want := "herdr agent get gh-1"; (*calls)[0] != want {
		t.Errorf("called %q, want %q", (*calls)[0], want)
	}

	fake(t, shell.Result{
		Code:   1,
		Stderr: `{"error":{"code":"agent_not_found","message":"agent target gh-1 not found"}}`,
	})

	if ok, err := AgentExists(t.Context(), "/repo", "gh-1"); ok || err != nil {
		t.Errorf("AgentExists = %v, %v, want false with no error", ok, err)
	}

	fake(t, shell.Result{Code: 1, Stderr: "socket gone"})

	if _, err := AgentExists(t.Context(), "/repo", "gh-1"); err == nil {
		t.Error("AgentExists swallowed a failure other than agent_not_found")
	}
}

func TestPaneIdleShell(t *testing.T) {
	calls := fake(t, shell.Result{Stdout: `{"result":{"process_info":{"foreground_process_group_id":56060,` +
		`"foreground_processes":[{"name":"zsh","pid":56060}],"pane_id":"pane:7","shell_pid":56060}}}`})

	if idle, err := PaneIdleShell(t.Context(), "/repo", "pane:7"); !idle || err != nil {
		t.Errorf("PaneIdleShell = %v, %v, want an idle shell", idle, err)
	}

	if want := "herdr pane process-info --pane pane:7"; (*calls)[0] != want {
		t.Errorf("called %q, want %q", (*calls)[0], want)
	}

	fake(t, shell.Result{Stdout: `{"result":{"process_info":{"foreground_process_group_id":6179,` +
		`"foreground_processes":[{"name":"claude","pid":6179}],"pane_id":"pane:7","shell_pid":6030}}}`})

	if idle, err := PaneIdleShell(t.Context(), "/repo", "pane:7"); idle || err != nil {
		t.Errorf("PaneIdleShell = %v, %v, want a busy pane", idle, err)
	}

	fake(t, shell.Result{Stdout: `{"result":{}}`})

	if idle, _ := PaneIdleShell(t.Context(), "/repo", "pane:7"); idle {
		t.Error("a pane with no process info read as an idle shell")
	}
}

func TestStartAgentReportsTheWait(t *testing.T) {
	fake(t, busy)
	shortWait(t, 300*time.Millisecond)

	prevPoll, prevReport := StartPoll, StartReport
	StartPoll, StartReport = 5*time.Millisecond, 50*time.Millisecond

	t.Cleanup(func() { StartPoll, StartReport = prevPoll, prevReport })

	var waits []time.Duration

	_ = StartAgent(t.Context(), "/repo", "gh-1", "claude", "pane:7", func(d time.Duration) { waits = append(waits, d) })

	if len(waits) < 3 {
		t.Fatalf("reported %v, want the first busy answer and one per StartReport after it", waits)
	}

	for i := 1; i < len(waits); i++ {
		if gap := waits[i] - waits[i-1]; gap > StartReport {
			t.Errorf("reports %v and %v are %v apart, want at most %v", waits[i-1], waits[i], gap, StartReport)
		}
	}

	fake(t, shell.Result{Stdout: `{"result":{}}`})

	waits = nil
	if err := StartAgent(
		t.Context(),
		"/repo",
		"gh-1",
		"claude",
		"pane:7",
		func(d time.Duration) { waits = append(waits, d) },
	); err != nil {
		t.Fatal(err)
	}

	if len(waits) != 0 {
		t.Errorf("reported %v for a pane that was never busy", waits)
	}
}
