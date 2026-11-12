package testkit

import (
	"errors"

	"github.com/urfave/cli/v3"
)

const (
	FwdMain = "/code/repo"
	FwdWT   = "/code/repo.GH-1"
	FwdText = "/f10:ship GH-1"
)

func ExitCode(err error) int {
	if coder, ok := errors.AsType[cli.ExitCoder](err); ok {
		return coder.ExitCode()
	}

	return -1
}
