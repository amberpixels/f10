package testkit

import (
	"errors"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

type FixtureCall struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr,omitempty"`
	Code   int    `json:"code,omitempty"`
}

// GitExitCode is the code git exited with, 1 for a failure that never ran it,
// so a replayed absence reads as one.
func GitExitCode(err error) int {
	if exit, ok := errors.AsType[*gitx.ExitError](err); ok {
		return exit.Code
	}

	return 1
}
