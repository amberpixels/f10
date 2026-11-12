package testkit

import (
	"github.com/amberpixels/f10/cli/internal/shell"
)

// Broken is git failing to read rather than finding nothing: a lock, a
// broken worktree path, a killed process.
var Broken = shell.Result{Code: 128, Stderr: "fatal: unable to read config"}
