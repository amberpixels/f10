package apiclient

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/printed"
	"github.com/amberpixels/f10/cli/internal/seam"
)

// The CLI turns the daemon's error back into what the command returned in
// process: what it printed first, its words, its exit code, its kind.
func TestErrRebuildsTheCommandsError(t *testing.T) {
	t.Parallel()

	ce := connect.NewError(connect.CodeUnknown, errors.New("f10: riders need a --close answer"))
	ce.Meta().Set(printed.ExitHeader, "7")

	d, err := connect.NewErrorDetail(&f10v1.Printed{Stdout: "task: GH-1\n", Stderr: "merging\n"})
	if err != nil {
		t.Fatal(err)
	}

	ce.AddDetail(d)

	var out, errOut bytes.Buffer

	got := Err(ce, &out, &errOut)

	ec, ok := errors.AsType[cli.ExitCoder](got)
	if !ok || ec.ExitCode() != 7 || got.Error() != "f10: riders need a --close answer" {
		t.Errorf("err = %v (%T), want the riders refusal exiting 7", got, got)
	}

	if out.String() != "task: GH-1\n" || errOut.String() != "merging\n" {
		t.Errorf("printed %q and %q, want the report and the progress", out.String(), errOut.String())
	}

	outside := connect.NewError(connect.CodeFailedPrecondition, errors.New("run inside a Herdr session"))
	outside.Meta().Set(printed.KindHeader, printed.KindOutside)

	if got := Err(outside, &out, &errOut); !agents.IsOutside(got) || got.Error() != "run inside a Herdr session" {
		t.Errorf("err = %v, want the outside-Herdr refusal in its own words", got)
	}

	if got := Err(
		connect.NewError(connect.CodeInvalidArgument, errors.New("plain")),
		&out,
		&errOut,
	); got.Error() != "plain" {
		t.Errorf("err = %v, want the daemon's sentence without Connect's code", got)
	}
}

// A test binary serves the handlers in process, so no test reaches a daemon.
func TestDialServesInProcessUnderTest(t *testing.T) {
	seam.Swap(t, &Local, func() http.Handler { return http.NewServeMux() })

	c, err := Dial(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !c.inProcess {
		t.Error("a test binary dialled the daemon")
	}
}
