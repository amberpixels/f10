// Package ui is how a command talks to whoever runs it: a person at a
// terminal, a pipe, or an agent parsing --json. It holds no project state,
// so every command can depend on it.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// JSON writes v the way every --json prints: indented two spaces, one
// trailing newline.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}

	return nil
}

// JSONFlag is the --json flag; usage says what the command emits.
func JSONFlag(usage string) cli.Flag {
	return &cli.BoolFlag{Name: "json", Usage: usage}
}

// A Terminal writer says for itself whether it stands for a terminal, as a
// buffer copied on to one does.
type Terminal interface {
	Terminal() bool
}

// IsTerminal reports whether w is a terminal, so escape codes and paging
// go only to a person and never into what an agent or script parses.
func IsTerminal(w io.Writer) bool {
	if t, ok := w.(Terminal); ok {
		return t.Terminal()
	}

	f, ok := w.(*os.File)

	return ok && term.IsTerminal(int(f.Fd()))
}

// Interactive reports whether a person can both read and answer: stdin and
// stdout are terminals.
func Interactive() bool {
	return StdinTerminal() && IsTerminal(os.Stdout)
}

// StdinTerminal reports whether stdin is a terminal rather than piped input.
func StdinTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// Width is w's terminal width, 0 when piped, so output stays unwrapped for
// grep.
func Width(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}

	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}

	return width
}

// PlainWidth is the width plain output is cut to: $COLUMNS, else 100, as a
// skill relays it into a pane whose width the binary cannot read.
func PlainWidth() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}

	return 100
}

// Open hands target, a url or a path, to the OS's opener.
func Open(ctx context.Context, dir, target string) error {
	opener := shell.Opener()
	if opener == "" {
		return errors.New("no OS opener (open/xdg-open) on PATH - use --link and open it yourself")
	}

	return shell.Passthrough(ctx, dir, opener, target)
}
