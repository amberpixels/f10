package main

import (
	"context"
	"strings"
	"time"

	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/stack"
)

// verifySpecOf reads the project's verify as commands. A Verify heading
// counts only in the form f10 init writes (backticked commands joined by
// " / "); any other prose is rules only a model can follow, and why says so.
func verifySpecOf(eff *facts.Effective) (stack.VerifySpec, string) {
	if eff.Verify != nil {
		return stack.VerifySpec{List: eff.Verify}, ""
	}

	f := configcmd.VerifyFieldIn(eff.Fields)
	body := strings.TrimSpace(f.Value)

	switch {
	case f.Origin.Kind == facts.OriginDetected:
		return stack.VerifySpec{Cmds: eff.VerifyCommands}, ""
	case f.Origin.Declared():
		if cmds, ok := backticked(body); ok {
			return stack.VerifySpec{Cmds: cmds}, ""
		}

		return stack.VerifySpec{}, "the Verify heading is prose, not commands"
	case f.Origin.Kind == facts.OriginJudgment:
		return stack.VerifySpec{}, "verify is left to judgment: nothing declared or detected names its commands"
	}

	return stack.VerifySpec{}, "no verify is declared or detected"
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

// A verifyJob is verify running off the poll loop, since a test suite can
// take minutes and the loop polls every task.
type verifyJob struct {
	done chan struct{}
	stop context.CancelFunc // ends the verify when its run ends first
	res  stack.VerifyRun
	err  error
}

func startVerifyJob(ctx context.Context, run func(context.Context) (stack.VerifyRun, error)) *verifyJob {
	ctx, stop := context.WithCancel(ctx)
	job := &verifyJob{done: make(chan struct{}), stop: stop}

	go func() {
		defer close(job.done)
		defer stop()

		job.res, job.err = run(ctx)
	}()

	return job
}

// halt stops the job and waits for its kill to land, bounded as the verify's
// own wait is, since the process may exit right after.
func (j *verifyJob) halt() {
	j.stop()

	select {
	case <-j.done:
	case <-time.After(stack.VerifyWaitDelay):
	}
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
