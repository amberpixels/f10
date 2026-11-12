package start_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A forwardFixture is the fakes plus a main checkout at /code/repo, GH-1's
// branch in the worktree beside it, and a Herdr listing that shows both.
// The state dir is empty until a test writes a run into it.
type forwardFixture struct {
	f  *testkit.Fakes
	in startcmd.ForwardInput
}

func newForwardFixture(t *testing.T) *forwardFixture {
	t.Helper()

	fx := &forwardFixture{f: testkit.NewFakes(t)}
	fx.in = startcmd.ForwardInput{
		Main:     testkit.FwdMain,
		Here:     testkit.FwdMain,
		Task:     testkit.Gh1,
		Text:     testkit.FwdText,
		StateDir: t.TempDir(),
		Ttl:      state.DefaultTTL,
		Now:      time.Now(),
	}

	fx.f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", testkit.FwdWT))
	fx.f.Script(testkit.WsList, fx.workspaces("working"))

	return fx
}

func (fx *forwardFixture) workspaces(agent string) string {
	return `{"result":{"workspaces":[` +
		`{"workspace_id":"ws:1","label":"repo","focused":true,"worktree":{"checkout_path":"` + testkit.FwdMain + `"}},` +
		`{"workspace_id":"ws:9","label":"GH-1","agent_status":"` + agent + `","worktree":{"checkout_path":"` + testkit.FwdWT + `"}}` +
		`]}}`
}

func (fx *forwardFixture) run(t *testing.T, ship, leaf string) {
	t.Helper()

	body := "v 1\ntask GH-1\nroot " + testkit.FwdWT + "\ncapture prior\nplan done\nship " + ship + "\nleaf " + leaf +
		"\nupdated " + strconv.FormatInt(fx.in.Now.Unix(), 10) + "\n"

	if err := os.WriteFile(filepath.Join(fx.in.StateDir, "remote.state"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (fx *forwardFixture) forward(t *testing.T) (string, error) {
	t.Helper()

	var out bytes.Buffer

	err := startcmd.Forward(t.Context(), &out, fx.in)

	return out.String(), err
}

// The happy path: the text reaches the agent start named, marked driven.
func TestForwardSubmitsToTheTasksAgent(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+testkit.FwdText+" --driven", "{}")

	out, err := fx.forward(t)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"task:", "GH-1", "workspace:", "ws:9", "sent:", testkit.FwdText + " --driven"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

// An agent whose name lacks the path hash answers to its bare name: forward
// reaches it there.
func TestForwardReachesAnAgentByItsBareName(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.Fail("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+testkit.FwdText+" --driven", shell.Result{
		Code:   1,
		Stderr: `{"error":{"code":"agent_not_found","message":"agent target not found"}}`,
	})
	fx.f.Script("herdr agent prompt gh-1 "+testkit.FwdText+" --driven", "{}")

	if _, err := fx.forward(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called("herdr agent prompt gh-1 ") {
		t.Error("the bare name was not tried")
	}
}

// An answer is plain text: it is sent as it is, and a suffix reaches the
// agent the suffix named.
func TestForwardSendsPlainTextUnmarked(t *testing.T) {
	fx := newForwardFixture(t)
	fx.in.Suffix = "v2"
	fx.in.Text = "1. no 2. client"
	fx.f.Script(testkit.RefsLocal, testkit.Heads("GH-1-v2"))
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1-v2", testkit.FwdWT))
	fx.f.Script("herdr agent prompt "+agentctl.AgentName("GH-1", "v2", testkit.FwdWT)+" 1. no 2. client", "{}")

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
		{
			name:   "no branch",
			refs:   "",
			wts:    testkit.Porcelain(),
			here:   testkit.FwdMain,
			reason: "GH-1 has no branch here",
		},
		{
			name:   "no worktree",
			refs:   testkit.Heads("GH-1"),
			wts:    testkit.Porcelain(),
			here:   testkit.FwdMain,
			reason: "checked out nowhere",
		},
		{
			name:   "this checkout",
			refs:   testkit.Heads("GH-1"),
			wts:    testkit.Porcelain("GH-1", testkit.FwdWT),
			here:   testkit.FwdWT,
			reason: "checked out here",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newForwardFixture(t)
			fx.in.Here = c.here
			fx.f.Script(testkit.RefsLocal, c.refs)
			fx.f.Script(testkit.Worktrees, c.wts)

			out, err := fx.forward(t)
			if testkit.ExitCode(err) != ui.ExitDeclined {
				t.Fatalf("err = %v, want exit %d", err, ui.ExitDeclined)
			}

			if !strings.Contains(err.Error(), c.reason) {
				t.Errorf("err = %v, want %q in it", err, c.reason)
			}

			if out != "" || fx.f.Called("herdr") {
				t.Errorf("declining printed %q and called herdr: %v", out, fx.f.Calls)
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
	fx.in.Here = testkit.FwdWT

	if _, err := fx.forward(t); testkit.ExitCode(err) != ui.ExitDeclined {
		t.Errorf("err = %v, want exit %d without Herdr", err, ui.ExitDeclined)
	}
}

func TestForwardNeedsAWorkspace(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.Script(testkit.WsList, `{"result":{"workspaces":[]}}`)

	_, err := fx.forward(t)
	if err == nil || !strings.Contains(err.Error(), "no Herdr workspace shows "+testkit.FwdWT) {
		t.Errorf("err = %v, want the missing workspace named", err)
	}

	if fx.f.Called("herdr agent") {
		t.Error("prompted an agent with no workspace")
	}
}

// An idle agent whose run says running is waiting on something in its pane:
// the forward names the workspace and the step and sends nothing.
func TestForwardRefusesAnIdleAgentStillRunning(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.Script(testkit.WsList, fx.workspaces("idle"))
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

	if fx.f.Called("herdr agent") {
		t.Error("prompted the stalled agent")
	}
}

// An idle agent on a blocked run is the driven state: the answer goes through.
func TestForwardSendsToAnIdleAgentThatIsBlocked(t *testing.T) {
	fx := newForwardFixture(t)
	fx.f.Script(testkit.WsList, fx.workspaces("idle"))
	fx.run(t, "blocked", "implement")
	fx.in.Text = "1. no"
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" 1. no", "{}")

	if _, err := fx.forward(t); err != nil {
		t.Fatal(err)
	}

	// and an idle agent with no live run at all is idle
	fx = newForwardFixture(t)
	fx.f.Script(testkit.WsList, fx.workspaces("idle"))
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+testkit.FwdText+" --driven", "{}")

	if _, err := fx.forward(t); err != nil {
		t.Fatal(err)
	}
}
