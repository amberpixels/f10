// Package shell is the binary's door to every external command that is not
// git; gitx stays separate because its reads mirror bin/f10-load-project.sh.
// Every entry point is a variable so tests can substitute it: nothing here
// should launch a browser or block on a pager under `go test`.
package shell

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// A Result is one finished command: its trimmed streams and its exit code.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Capture runs name in dir. A non-zero exit is a Result, not an error, since
// the driver contract reserves an exit code with a meaning and classifying
// codes belongs to the caller; only a command that could not start is an error.
var Capture = func(ctx context.Context, dir, name string, args ...string) (Result, error) {
	return run(ctx, dir, nil, name, args...)
}

// CaptureInput is Capture with input on stdin, for a driver's write verbs.
var CaptureInput = func(ctx context.Context, dir, input, name string, args ...string) (Result, error) {
	return run(ctx, dir, strings.NewReader(input), name, args...)
}

// waitDelay is how long a finished or killed command's leftover children
// (a driver's curl) may hold its pipes open before run stops reading them.
const waitDelay = 2 * time.Second

func run(ctx context.Context, dir string, stdin io.Reader, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.WaitDelay = waitDelay

	var stdout, stderr strings.Builder

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{
		Stdout: strings.TrimRight(stdout.String(), "\n"),
		Stderr: strings.TrimSpace(stderr.String()),
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		res.Code = exitErr.ExitCode()

		return res, nil
	}

	// the command itself exited 0; only a child it left behind was cut off
	if errors.Is(err, exec.ErrWaitDelay) {
		return res, nil
	}

	return res, err
}

// Passthrough runs name in dir wired straight to this process's streams. A
// pager, an editor and `gh --web` all want the terminal, not a buffer.
var Passthrough = func(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	return cmd.Run()
}

// Pipe runs name with input on its stdin and the terminal on its output: what
// a pager needs.
var Pipe = func(ctx context.Context, dir, input, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr

	return cmd.Run()
}

// Has reports whether name is on PATH.
var Has = func(name string) bool {
	_, err := exec.LookPath(name)

	return err == nil
}

// Opener is the OS's "open this thing" command, or "" where there is none.
func Opener() string {
	for _, c := range []string{"open", "xdg-open"} {
		if Has(c) {
			return c
		}
	}

	return ""
}

// Editor is VISUAL, EDITOR, then the OS opener, split on fields since values
// carry arguments ("code -w").
func Editor() []string {
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if v := strings.Fields(os.Getenv(key)); len(v) > 0 {
			return v
		}
	}

	if c := Opener(); c != "" {
		return []string{c}
	}

	return nil
}
