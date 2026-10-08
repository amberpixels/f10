package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
)

// A forwardFixture is the fakes plus a main checkout at /code/repo, GH-1's
// branch in the worktree beside it, and a Herdr listing that shows both.
// The state dir is empty until a test writes a run into it.
type forwardFixture struct {
	f  *fakes
	in forwardInput
}

const (
	fwdMain = "/code/repo"
	fwdWT   = "/code/repo.GH-1"
	fwdText = "/f10:ship GH-1"
)

func newForwardFixture(t *testing.T) *forwardFixture {
	t.Helper()

	fx := &forwardFixture{f: newFakes(t)}
	fx.in = forwardInput{
		main:     fwdMain,
		here:     fwdMain,
		task:     gh1,
		text:     fwdText,
		stateDir: t.TempDir(),
		ttl:      state.DefaultTTL,
		now:      time.Now(),
	}

	fx.f.script(refsLocal, heads("GH-1"))
	fx.f.script(worktrees, porcelain("GH-1", fwdWT))
	fx.f.script(wsList, fx.workspaces("working"))

	return fx
}

// workspaces lists main's workspace and the task's, with the agent status
// Herdr reports for the task's.
func (fx *forwardFixture) workspaces(agent string) string {
	return `{"result":{"workspaces":[` +
		`{"workspace_id":"ws:1","label":"repo","focused":true,"worktree":{"checkout_path":"` + fwdMain + `"}},` +
		`{"workspace_id":"ws:9","label":"GH-1","agent_status":"` + agent + `","worktree":{"checkout_path":"` + fwdWT + `"}}` +
		`]}}`
}

// run writes a live run for the worktree into the state dir, in the
// script's own shape.
func (fx *forwardFixture) run(t *testing.T, ship, leaf string) {
	t.Helper()

	body := "v 1\ntask GH-1\nroot " + fwdWT + "\ncapture prior\nplan done\nship " + ship + "\nleaf " + leaf +
		"\nupdated " + strconv.FormatInt(fx.in.now.Unix(), 10) + "\n"

	if err := os.WriteFile(filepath.Join(fx.in.stateDir, "remote.state"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (fx *forwardFixture) forward(t *testing.T) (string, error) {
	t.Helper()

	var out bytes.Buffer

	err := forward(t.Context(), &out, fx.in)

	return out.String(), err
}

// exitCode is the code an error asks the process to exit with, or -1.
func exitCode(err error) int {
	if coder, ok := errors.AsType[cli.ExitCoder](err); ok {
		return coder.ExitCode()
	}

	return -1
}

// The happy path: the branch is in the worktree, Herdr shows it, the agent
// is working, the text goes to the agent start named with the marker added,
// and the report carries the three rows.
func TestForwardSubmitsToTheTasksAgent(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+fwdText+" --driven", "{}")

	out, err := fx.forward(t)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"task:", "GH-1", "workspace:", "ws:9", "sent:", fwdText + " --driven"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

// An agent started before names carried the path hash answers to its bare
// name: forward reaches it there.
func TestForwardReachesAnAgentByItsBareName(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.fail("herdr agent prompt "+agentOf("GH-1")+" "+fwdText+" --driven", shell.Result{
		Code:   1,
		Stderr: `{"error":{"code":"agent_not_found","message":"agent target not found"}}`,
	})
	fx.f.script("herdr agent prompt gh-1 "+fwdText+" --driven", "{}")

	if _, err := fx.forward(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.called("herdr agent prompt gh-1 ") {
		t.Error("the bare name was not tried")
	}
}

// An answer is plain text: it is sent as it is, and a suffix reaches the
// agent the suffix named.
func TestForwardSendsPlainTextUnmarked(t *testing.T) {
	fx := newForwardFixture(t)
	fx.in.suffix = "v2"
	fx.in.text = "1. no 2. client"
	fx.f.script(refsLocal, heads("GH-1-v2"))
	fx.f.script(worktrees, porcelain("GH-1-v2", fwdWT))
	fx.f.script("herdr agent prompt "+agentName("GH-1", "v2", fwdWT)+" 1. no 2. client", "{}")

	out, err := fx.forward(t)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "sent:         1. no 2. client") || strings.Contains(out, "--driven") {
		t.Errorf("report = %q", out)
	}
}

// The three reasons to run locally all exit 3 and touch Herdr not at all.
func TestForwardDeclinesWhenTheTaskIsHere(t *testing.T) {
	cases := []struct {
		name   string
		refs   string
		wts    string
		here   string
		reason string
	}{
		{name: "no branch", refs: "", wts: porcelain(), here: fwdMain, reason: "GH-1 has no branch here"},
		{name: "no worktree", refs: heads("GH-1"), wts: porcelain(), here: fwdMain, reason: "checked out nowhere"},
		{
			name:   "this checkout",
			refs:   heads("GH-1"),
			wts:    porcelain("GH-1", fwdWT),
			here:   fwdWT,
			reason: "checked out here",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newForwardFixture(t)
			fx.in.here = c.here
			fx.f.script(refsLocal, c.refs)
			fx.f.script(worktrees, c.wts)

			out, err := fx.forward(t)
			if exitCode(err) != exitHere {
				t.Fatalf("err = %v, want exit %d", err, exitHere)
			}

			if !strings.Contains(err.Error(), c.reason) {
				t.Errorf("err = %v, want %q in it", err, c.reason)
			}

			if out != "" || fx.f.called("herdr") {
				t.Errorf("declining printed %q and called herdr: %v", out, fx.f.calls)
			}
		})
	}
}

// Outside Herdr a task that is elsewhere is start's refusal; a task that is
// here is still exit 3, since running it locally needs no Herdr.
func TestForwardOutsideHerdr(t *testing.T) {
	fx := newForwardFixture(t)
	t.Setenv("HERDR_ENV", "")

	if _, err := fx.forward(t); err == nil || !strings.Contains(err.Error(), "inside a Herdr session") {
		t.Errorf("err = %v, want the Herdr refusal", err)
	}

	fx = newForwardFixture(t)
	fx.in.here = fwdWT

	if _, err := fx.forward(t); exitCode(err) != exitHere {
		t.Errorf("err = %v, want exit %d without Herdr", err, exitHere)
	}
}

func TestForwardNeedsAWorkspace(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.script(wsList, `{"result":{"workspaces":[]}}`)

	_, err := fx.forward(t)
	if err == nil || !strings.Contains(err.Error(), "no Herdr workspace shows "+fwdWT) {
		t.Errorf("err = %v, want the missing workspace named", err)
	}

	if fx.f.called("herdr agent") {
		t.Error("prompted an agent with no workspace")
	}
}

// An idle agent whose run says running is waiting on something in its pane:
// the forward names the workspace and the step and sends nothing.
func TestForwardRefusesAnIdleAgentStillRunning(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.script(wsList, fx.workspaces("idle"))
	fx.run(t, "running", "implement")

	_, err := fx.forward(t)
	if err == nil {
		t.Fatal("forwarded to a stalled agent")
	}

	for _, want := range []string{"agent gh-1", "workspace GH-1", "ship running: implement", "its own pane"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q in it", err, want)
		}
	}

	if fx.f.called("herdr agent") {
		t.Error("prompted the stalled agent")
	}
}

// An idle agent on a blocked run is the driven state: the answer goes through.
func TestForwardSendsToAnIdleAgentThatIsBlocked(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.script(wsList, fx.workspaces("idle"))
	fx.run(t, "blocked", "implement")
	fx.in.text = "1. no"
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" 1. no", "{}")

	if _, err := fx.forward(t); err != nil {
		t.Fatal(err)
	}

	// and an idle agent with no live run at all is simply idle
	fx = newForwardFixture(t)
	fx.f.script(wsList, fx.workspaces("idle"))
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+fwdText+" --driven", "{}")

	if _, err := fx.forward(t); err != nil {
		t.Fatal(err)
	}
}

func TestDriven(t *testing.T) {
	cases := []struct{ text, want string }{
		{"/f10:ship GH-1", "/f10:ship GH-1 --driven"},
		{"/f10:plan GH-1 && /f10:ship GH-1 && defaults", "/f10:plan GH-1 && /f10:ship GH-1 && defaults --driven"},
		{"/f10:ship GH-1 --driven", "/f10:ship GH-1 --driven"},
		{"/f10:ship GH-1 --driven && go", "/f10:ship GH-1 --driven && go"},
		{"1. no 2. client", "1. no 2. client"},
		{"rebase first, then /f10:ship GH-1", "rebase first, then /f10:ship GH-1"},
	}

	for _, c := range cases {
		if got := driven(c.text); got != c.want {
			t.Errorf("driven(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

// taskCheckout is the lookup forward, status and finish share: the branch
// as start would find it, and the checkout that has it, or neither.
func TestTaskCheckout(t *testing.T) {
	f := newFakes(t)
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", fwdWT))

	name, path, err := taskCheckout(t.Context(), fwdMain, nil, gh1, "")
	if err != nil || name != "GH-1" || path != fwdWT {
		t.Errorf("taskCheckout = %q, %q, %v", name, path, err)
	}

	f.script(worktrees, porcelain())

	if name, path, err = taskCheckout(t.Context(), fwdMain, nil, gh1, ""); err != nil || name != "GH-1" || path != "" {
		t.Errorf("a branch with no worktree = %q, %q, %v", name, path, err)
	}

	f.script(refsLocal, "")

	if name, path, err = taskCheckout(t.Context(), fwdMain, nil, gh1, ""); err != nil || name != "" || path != "" {
		t.Errorf("no branch = %q, %q, %v", name, path, err)
	}
}
