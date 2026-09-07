// Package shell is the binary's door to every external command that is not
// git: a tracker CLI, a project's driver, a pager, an editor, the OS
// opener. gitx stays separate because it mirrors the three calls
// bin/resolve.sh makes; this is everything that script never runs.
//
// Both entry points are variables so tests can substitute them - nothing
// here should launch a browser or block on a pager during `go test`.
package shell

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// A Result is one finished command: its trimmed streams and its exit code.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Capture runs name in dir and collects its output. A non-zero exit is a
// Result, not an error: the driver contract reserves an exit code with a
// meaning, so classifying codes belongs to the caller. Only a command that
// could not start at all is an error here.
var Capture = func(ctx context.Context, dir, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

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

// Pipe runs name with input on its stdin and this process's terminal on
// its output - what a pager needs, and the one case where f10 both holds
// the text and wants someone else to display it.
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
// A url and a file both go through it.
func Opener() string {
	for _, c := range []string{"open", "xdg-open"} {
		if Has(c) {
			return c
		}
	}

	return ""
}

// Editor is the editor to hand a local file to: the usual two variables,
// then the OS opener. Values carry arguments ("code -w"), so callers split
// on fields rather than exec'ing the string.
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
