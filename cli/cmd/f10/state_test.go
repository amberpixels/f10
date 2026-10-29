package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/state"
)

func runState(t *testing.T, args ...string) (string, int) {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &out
	app.ExitErrHandler = func(context.Context, *cli.Command, error) {} // keep the exit code, not the exit

	err := app.Run(context.Background(), append([]string{"f10", "state"}, args...))
	if err == nil {
		return out.String(), 0
	}

	// the exit handler prints the message; the test's handler keeps it in err
	return out.String() + err.Error(), exitCode(err)
}

func TestStateCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "s1")

	for _, args := range [][]string{
		{"task", "GH-7", "https://github.com/acme/app/issues/7"},
		{"set", "ship", "blocked", "judge"},
		// free prose: a leading dash and a help word are text, not flags
		{"note", "push rejected", "-f was needed"},
		{"ask", "--help\n1. retry? [no | yes]"},
		{"wait", "GH-6"},
	} {
		if out, code := runState(t, args...); code != 0 {
			t.Fatalf("%q: exit %d\n%s", args, code, out)
		}
	}

	r, err := state.Load(state.Path(dir, "s1"))
	if err != nil {
		t.Fatal(err)
	}

	if r.Task != "GH-7" || r.Ship != "blocked" || r.Leaf != "judge" || r.Next != "-f was needed" ||
		r.Ask != "--help 1. retry? [no | yes]" || r.Wait != "GH-6" {
		t.Errorf("state %+v", r)
	}

	// running clears the wait with the ask, so a resumed ship carries neither
	if out, code := runState(t, "set", "ship", "running", "implement"); code != 0 {
		t.Fatalf("running: exit %d\n%s", code, out)
	}

	if r, err = state.Load(state.Path(dir, "s1")); err != nil || r.Wait != "" || r.Ask != "" {
		t.Errorf("after running: %+v, %v", r, err)
	}

	// a typo exits 2 and names itself, so step prose cannot hide it
	if out, code := runState(t, "set", "shp", "running"); code != 2 || !strings.Contains(out, "unknown phase 'shp'") {
		t.Errorf("typo: exit %d\n%s", code, out)
	}

	if out, code := runState(t, "note"); code != 2 || !strings.Contains(out, "note needs a reason") {
		t.Errorf("missing argument: exit %d\n%s", code, out)
	}

	// no session: nothing written, still exit 0
	t.Setenv("F10_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	if out, code := runState(t, "set", "plan", "done"); code != 0 {
		t.Errorf("no session: exit %d\n%s", code, out)
	}
}

// bin/f10-state.sh forwards writer verbs to the binary beside it: a typo's
// exit 2 and message pass through, a missing binary (the shim's exit 1) is
// silent, and the arguments arrive unchanged.
func TestStateScriptForwards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}

	_, file, _, _ := runtime.Caller(0)
	src, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "bin", "f10-state.sh"))
	if err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	script := filepath.Join(bin, "f10-state.sh")

	if err := os.WriteFile(script, src, 0o755); err != nil {
		t.Fatal(err)
	}

	stub := func(body string) {
		t.Helper()

		if err := os.WriteFile(
			filepath.Join(bin, "f10"),
			[]byte("#!/bin/sh\n"+body),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
	}

	run := func(args ...string) (string, string, int) {
		t.Helper()

		var stdout, stderr bytes.Buffer

		cmd := exec.Command(script, args...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr

		err := cmd.Run()
		code := 0

		if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns it unwrapped
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}

		return stdout.String(), stderr.String(), code
	}

	stub(`printf '%s|' "$@"` + "\n")

	if out, _, code := run(
		"note",
		"-f was needed",
		"two words",
	); code != 0 ||
		out != "state|note|-f was needed|two words|\n" {
		t.Errorf("forwarded %q, exit %d", out, code)
	}

	stub("echo \"f10-state: unknown phase 'shp'\" >&2; exit 2\n")

	if _, errOut, code := run("set", "shp", "running"); code != 2 || !strings.Contains(errOut, "unknown phase") {
		t.Errorf("typo: exit %d, stderr %q", code, errOut)
	}

	stub("echo 'f10: no binary for 0.0.0 yet' >&2; exit 1\n")

	if out, errOut, code := run("set", "ship", "done"); code != 0 || out != "" || errOut != "" {
		t.Errorf("no binary: exit %d, out %q, err %q", code, out, errOut)
	}
}
