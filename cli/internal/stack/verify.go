package stack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// verifyLimit bounds one verify command the drive runs itself, and
// verifyWaitDelay how long its output is read once it was killed.
var (
	VerifyLimit     = 30 * time.Minute
	VerifyWaitDelay = 5 * time.Second
)

// A VerifySpec is the verify the drive can run without a model: the
// frontmatter list, scoped to the branch's diff, or a fixed set of commands.
type VerifySpec struct {
	List []facts.VerifyDecl
	Cmds []string
}

// commands is what verify runs in the checkout at dir: the list cut to the
// diff, every listed command when the diff cannot be read.
func (s VerifySpec) commands(ctx context.Context, dir, main string) []string {
	if s.List == nil {
		return s.Cmds
	}

	pick := s.List
	if _, changed, err := BranchDiff(ctx, dir, main); err == nil {
		run, _ := facts.ScopeVerify(s.List, changed)

		pick = make([]facts.VerifyDecl, 0, len(run))
		for _, p := range run {
			pick = append(pick, p.VerifyDecl)
		}
	}

	cmds := make([]string, 0, len(pick))
	for _, c := range pick {
		cmds = append(cmds, c.Run)
	}

	return cmds
}

// A VerifyRun is verify run once: the first command that failed, with its
// output in log, or "" when every command passed.
type VerifyRun struct {
	Failed, Log string
}

// RunVerify runs each command in dir in order and stops at the first that
// fails, writing its output to log.
func RunVerify(ctx context.Context, dir string, cmds []string, log string) (VerifyRun, error) {
	for _, c := range cmds {
		cctx, cancel := context.WithTimeout(ctx, VerifyLimit)
		// the limit kills the whole group, or a hung test would outlive it
		out, err := shell.Group(cctx, dir, c, VerifyWaitDelay)

		timedOut := errors.Is(cctx.Err(), context.DeadlineExceeded)

		cancel()

		if err == nil {
			continue
		}

		if ctx.Err() != nil {
			return VerifyRun{}, ctx.Err()
		}

		if timedOut {
			out = append(out, fmt.Sprintf("\nf10: stopped after %s\n", VerifyLimit)...)
		}

		if werr := writeLog(log, out); werr != nil {
			return VerifyRun{}, werr
		}

		return VerifyRun{Failed: c, Log: log}, nil
	}

	return VerifyRun{}, nil
}

func writeLog(path string, out []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("writing the verify log: %w", err)
	}

	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("writing the verify log: %w", err)
	}

	return nil
}
