package ui

import (
	"fmt"
	"io"

	"github.com/urfave/cli/v3"
)

// The exit codes skills, f10 drive and docs/cli.md read. A code's meaning
// is per command, so 6 and 7 also mean "drained" and "nothing watched" for
// drive and watch (internal/daemon owns those, as its protocol writes them).
const (
	// ExitUsage is a flag or argument the command cannot read.
	ExitUsage = 2
	// ExitDeclined is a normal "not here" answer the caller acts on, the same
	// code a driver declines with (driver.ExitUnsupported): task children with
	// no parent link, task create with no driver, task comment and update with
	// no write, forward with the task in this checkout, finish of someone
	// else's PR, start with no task named, and review round with no next round.
	ExitDeclined = 3
	// ExitAsk is a drive stopped on an ask the skill relays.
	ExitAsk = 4
	// ExitHalted is a drive stopped by any other halt.
	ExitHalted = 5
	// ExitParent is start on a parent with unfinished subtasks: the start
	// skill turns it into "drive them, or start one".
	ExitParent = 6
	// ExitRiders is finish of a bundle whose riders have no --close answer
	// yet: the finish skill asks which the PR delivers and reruns.
	ExitRiders = 7
)

const prefix = "f10: "

// Exit ends the command with code and a message carrying the f10 prefix.
func Exit(code int, format string, args ...any) cli.ExitCoder {
	return cli.Exit(prefix+fmt.Sprintf(format, args...), code)
}

// Warnf writes one warning line carrying the f10 prefix.
func Warnf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, prefix+format+"\n", args...)
}
