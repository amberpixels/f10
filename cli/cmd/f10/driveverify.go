package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// verifyLimit bounds one verify command the drive runs itself, and
// verifyWaitDelay how long its output is read once it was killed.
var (
	verifyLimit     = 30 * time.Minute
	verifyWaitDelay = 5 * time.Second
)

// A verifySpec is the verify the drive can run without a model: the
// frontmatter list, scoped to the branch's diff, or a fixed set of commands.
type verifySpec struct {
	list []facts.VerifyDecl
	cmds []string
}

// verifySpecOf reads the project's verify as commands. A Verify heading
// counts only in the form f10 init writes (backticked commands joined by
// " / "); any other prose is rules only a model can follow, and why says so.
func verifySpecOf(eff *facts.Effective) (verifySpec, string) {
	if eff.Verify != nil {
		return verifySpec{list: eff.Verify}, ""
	}

	f := verifyFieldIn(eff.Fields)
	body := strings.TrimSpace(f.Value)

	switch {
	case f.Origin == facts.OriginDetected:
		return verifySpec{cmds: strings.Split(body, " / ")}, ""
	case strings.HasPrefix(f.Origin, "declared"):
		if cmds, ok := backticked(body); ok {
			return verifySpec{cmds: cmds}, ""
		}

		return verifySpec{}, "the Verify heading is prose, not commands"
	case f.Origin == facts.OriginJudgment:
		return verifySpec{}, "verify is left to judgment: nothing declared or detected names its commands"
	}

	return verifySpec{}, "no verify is declared or detected"
}

// backticked reads "`a` / `b`" as [a b], and anything else as not commands.
func backticked(body string) ([]string, bool) {
	if body == "" || strings.Contains(body, "\n") {
		return nil, false
	}

	var cmds []string

	for part := range strings.SplitSeq(body, " / ") {
		if len(part) < 3 || part[0] != '`' || part[len(part)-1] != '`' {
			return nil, false
		}

		inner := part[1 : len(part)-1]
		if strings.Contains(inner, "`") {
			return nil, false
		}

		cmds = append(cmds, inner)
	}

	return cmds, true
}

// commands is what verify runs in the checkout at dir: the list cut to the
// diff, every listed command when the diff cannot be read.
func (s verifySpec) commands(ctx context.Context, dir, main string) []string {
	if s.list == nil {
		return s.cmds
	}

	pick := s.list
	if _, changed, err := branchDiff(ctx, dir, main); err == nil {
		run, _ := facts.ScopeVerify(s.list, changed)

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

// A verifyRun is verify run once: the first command that failed, with its
// output in log, or "" when every command passed.
type verifyRun struct {
	failed, log string
}

// runVerify runs each command in dir in order and stops at the first that
// fails, writing its output to log.
func runVerify(ctx context.Context, dir string, cmds []string, log string) (verifyRun, error) {
	for _, c := range cmds {
		cctx, cancel := context.WithTimeout(ctx, verifyLimit)
		cmd := exec.CommandContext(cctx, "sh", "-c", c)
		cmd.Dir = dir
		// the limit kills the whole group, and the wait stops reading a pipe
		// an orphan still holds, or a hung test would outlive the limit
		killGroup(cmd)
		cmd.WaitDelay = verifyWaitDelay

		out, err := cmd.CombinedOutput()

		timedOut := errors.Is(cctx.Err(), context.DeadlineExceeded)

		cancel()

		if err == nil {
			continue
		}

		if ctx.Err() != nil {
			return verifyRun{}, ctx.Err()
		}

		if timedOut {
			out = append(out, fmt.Sprintf("\nf10: stopped after %s\n", verifyLimit)...)
		}

		if werr := writeLog(log, out); werr != nil {
			return verifyRun{}, werr
		}

		return verifyRun{failed: c, log: log}, nil
	}

	return verifyRun{}, nil
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

// A verifyJob is verify running off the poll loop, since a test suite can
// take minutes and the loop polls every task.
type verifyJob struct {
	done chan struct{}
	res  verifyRun
	err  error
}

func startVerifyJob(run func() (verifyRun, error)) *verifyJob {
	job := &verifyJob{done: make(chan struct{})}

	go func() {
		defer close(job.done)

		job.res, job.err = run()
	}()

	return job
}

// finished says the job is through, without waiting for it.
func (j *verifyJob) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}
