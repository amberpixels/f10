package api

import (
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/printed"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A command's exit code and what it printed before failing travel on its
// error, so the CLI prints the same and exits with the same code.
func TestCodeOfCarriesTheExitCodeAndWhatPrinted(t *testing.T) {
	t.Parallel()

	err := &printed.Error{Err: ui.Exit(ui.ExitRiders, "riders need a --close answer"), Stdout: "task: GH-1\n"}

	ce, ok := errors.AsType[*connect.Error](codeOf(err))
	if !ok {
		t.Fatalf("codeOf = %T, want a Connect error", codeOf(err))
	}

	if got := ce.Meta().Get(printed.ExitHeader); got != "7" {
		t.Errorf("exit header = %q, want 7", got)
	}

	if !strings.Contains(ce.Message(), "riders need a --close answer") {
		t.Errorf("message = %q", ce.Message())
	}

	var got *f10v1.Printed

	for _, d := range ce.Details() {
		if v, err := d.Value(); err == nil {
			got, _ = v.(*f10v1.Printed)
		}
	}

	if got.GetStdout() != "task: GH-1\n" {
		t.Errorf("printed detail = %v, want the report printed before the failure", got)
	}
}

// A caller context reaches the handlers only from the unix socket: over any
// other listener the request names no checkout, so it names no project.
func TestCallerContextIsDroppedOffTheSocket(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(Handler(&fakeSup{}))
	t.Cleanup(srv.Close)

	c := f10v1connect.NewRunServiceClient(srv.Client(), srv.URL)

	_, err := c.GetRunStatus(t.Context(), connect.NewRequest(&f10v1.GetRunStatusRequest{
		Task:   "GH-1",
		Caller: &f10v1.CallerContext{Cwd: t.TempDir(), Session: "s1"},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "names no project") {
		t.Errorf("err = %v, want the request refused for naming no project", err)
	}
}

// A local call runs its commands in the caller's environment, its gh token
// and tracker settings included, as the command did in process.
func TestALocalCallRunsInTheCallersEnvironment(t *testing.T) {
	t.Parallel()

	env := []string{"GH_TOKEN=caller", "PATH=/usr/bin"}

	ctx, c := withCaller(t.Context(), connect.NewRequest(&f10v1.GetRunStatusRequest{}), &f10v1.CallerContext{Env: env})
	if !slices.Equal(shell.EnvOf(ctx), env) || c == nil {
		t.Errorf("env = %v, want the caller's %v", shell.EnvOf(ctx), env)
	}
}

// A drive started by a caller with no env of its own (the web client, MCP)
// carries no session id, so its launch never lands in the last local
// caller's launches.
func TestSessionlessDropsSessionIDs(t *testing.T) {
	t.Parallel()

	env := []string{"F10_SESSION_ID=s1", "CLAUDE_CODE_SESSION_ID=s2", "CLAUDE_CODE_ENTRYPOINT=cli", "PATH=/bin"}
	if got := sessionless(env); !slices.Equal(got, []string{"CLAUDE_CODE_ENTRYPOINT=cli", "PATH=/bin"}) {
		t.Errorf("sessionless = %v", got)
	}

	if len(env) != 4 {
		t.Errorf("the input was changed: %v", env)
	}
}
