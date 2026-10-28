//go:build !unix

package drives

import (
	"errors"
	"os"
)

// No flock here: every lease is taken and none reads held.
var errHeld = errors.New("lock held")

func lockExclusive(*os.File) error { return nil }

func lockShared(*os.File) error { return nil }

func unlock(*os.File) error { return nil }
