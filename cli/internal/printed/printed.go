// Package printed holds what a command prints when the daemon runs it for
// a client: the client prints it, so a command served over the API prints
// the bytes it printed in process.
package printed

import (
	"bytes"
	"errors"

	"github.com/amberpixels/f10/cli/internal/render"
)

// ExitHeader carries a command's exit code on its error, which Connect has
// no field for, so the CLI exits with the code it exited with in process.
const ExitHeader = "F10-Exit"

// KindHeader names a refusal a caller tells apart by kind rather than by
// its words: KindOutside is a caller outside Herdr.
const (
	KindHeader  = "F10-Error"
	KindOutside = "outside-herdr"
)

// Output is one call's stdout and stderr. Stdout links task ids and paths
// when the client's own stdout is a terminal, since the client copies it
// there unchanged.
type Output struct {
	Stdout render.LinkBuffer
	Stderr bytes.Buffer
}

// New is an empty output for a client whose stdout is a terminal or not.
func New(terminal bool) *Output {
	return &Output{Stdout: render.LinkBuffer{Links: terminal}}
}

// Wrap carries what o holds on err, so a client prints it before the error
// as the command did; nil stays nil.
func (o *Output) Wrap(err error) error {
	if err == nil {
		return nil
	}

	return &Error{Err: err, Stdout: o.Stdout.String(), Stderr: o.Stderr.String()}
}

// An Error is a failure after the command printed something.
type Error struct {
	Err            error
	Stdout, Stderr string
}

func (e *Error) Error() string { return e.Err.Error() }

func (e *Error) Unwrap() error { return e.Err }

// Of is the output err carries, and whether it carries one.
func Of(err error) (*Error, bool) {
	return errors.AsType[*Error](err)
}
