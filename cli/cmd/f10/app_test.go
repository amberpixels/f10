package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// appRun is one in-process run of the f10 app. combined holds stdout and
// stderr in the order they were written.
type appRun struct {
	out, errOut, combined string
	err                   error
}

// runApp runs `f10 <args>` in-process with empty stdin.
func runApp(t *testing.T, args ...string) appRun {
	t.Helper()

	return runAppStdin(t, strings.NewReader(""), args...)
}

// runAppStdin is the only test code that runs the app. An exit-coded error
// comes back in err, because the default handler would exit the test binary.
func runAppStdin(t *testing.T, stdin io.Reader, args ...string) appRun {
	t.Helper()

	var out, errOut, combined bytes.Buffer

	app := newApp()
	app.Reader = stdin
	app.Writer = io.MultiWriter(&out, &combined)
	app.ErrWriter = io.MultiWriter(&errOut, &combined)
	app.ExitErrHandler = func(context.Context, *cli.Command, error) {}

	err := app.Run(t.Context(), append([]string{"f10"}, args...))

	return appRun{out: out.String(), errOut: errOut.String(), combined: combined.String(), err: err}
}

// must fails the test on an error and returns stdout.
func (r appRun) must(t *testing.T) string {
	t.Helper()

	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.combined)
	}

	return r.out
}

func (r appRun) outErr() (string, error) { return r.out, r.err }

func (r appRun) streams() (string, string, error) { return r.out, r.errOut, r.err }
