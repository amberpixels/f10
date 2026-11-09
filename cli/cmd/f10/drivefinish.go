package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// readyToMerge brings the branch to where it merges cleanly, or says why not.
// A pushed branch is tried as origin's copy against origin's default branch,
// since the host merges that; one origin lacks against the local default,
// since finish merges it here.
func (in *driveInput) readyToMerge(ctx context.Context, w io.Writer, r *driveRun) *driveResult {
	id, branch := r.task.ref.ID, r.o.branch

	def, err := localDefault(ctx, in.main)
	if err != nil {
		res := halted(id, "finish", err.Error())

		return &res
	}

	if _, err := gitx.Exec(ctx, r.o.path, "fetch", "origin"); err != nil {
		res := halted(id, "finish", err.Error())

		return &res
	}

	pushed, err := gitx.Ref(ctx, r.o.path, "origin/"+branch)
	if err != nil {
		res := halted(id, "finish", err.Error())

		return &res
	}

	base, head := "origin/"+def, pushed

	if pushed == "" {
		if base, err = landingBase(ctx, in.main, def); err != nil {
			res := halted(id, "finish", err.Error())

			return &res
		}

		head = "refs/heads/" + branch
	}

	// every read before a push halts on a git that cannot answer: a wrong
	// "not an ancestor" would pick the lease and rewrite origin's branch
	clean, err := gitx.MergesCleanly(ctx, r.o.path, base, head)
	if err != nil {
		res := halted(id, "finish", err.Error())

		return &res
	}

	if clean {
		return nil
	}

	caughtUp := false
	if pushed != "" {
		if caughtUp, err = gitx.IsAncestor(ctx, r.o.path, base, "HEAD"); err != nil {
			res := halted(id, "finish", err.Error())

			return &res
		}
	}

	// after a catchup, origin's copy is the tree before it: HEAD is what the
	// catchup left and what the push would send, so HEAD is what is tried
	if !caughtUp && pushed != "" && r.lastCatchup != nil {
		if caughtUp, err = gitx.MergesCleanly(ctx, r.o.path, base, "HEAD"); err != nil {
			res := halted(id, "finish", err.Error())

			return &res
		}
	}

	if caughtUp {
		if err := in.pushCaughtUp(ctx, w, r, "finish", pushed, base); err != nil {
			res := pushHalted(id, "finish", err)

			return &res
		}

		return nil
	}

	reason, conflict, err := in.conflictAgain(ctx, r, branchmeta.Landed)

	switch {
	case err != nil:
		res := halted(id, "finish", err.Error())

		return &res
	case reason != "":
		if res := in.catchupAttempt(w, r, "finish", reason, conflict); res != nil {
			return res
		}
	}

	if err := branchmeta.Set(ctx, in.main, branch, branchmeta.Landed, def); err != nil {
		res := halted(id, "finish", fmt.Sprintf("marking %s landed on %s: %v", branch, def, err))

		return &res
	}

	fmt.Fprintf(w, "%s finish: %s conflicts with %s, catching up first\n", in.id(id), branch, base)
	in.finishStep(ctx, w, branch, branchmeta.FinishQueued, "catching up with "+base)

	return nil
}

// The answers drive's own finish question takes, for a PR someone else
// opened: merge it, or clean up locally and leave it open.
const (
	finishMerge     = "merge"
	finishLeaveOpen = "local"
)

// driveFinish is what a drive's finish needs from its run; the rest comes
// from the project's settings.
type driveFinish struct {
	task     ref.Ref
	suffix   string
	branch   string // the run's branch, "" to look it up by task and suffix
	pr       string // the answer to a foreign PR: finishMerge, finishLeaveOpen or ""
	progress func(string)
	handoff  func([]string)
	deferred func(string)
}

func driveFinishInput(t *target, main string, h host, req driveFinish) (finishInput, error) {
	in := finishInputFor(t, main, h, req.task)

	var err error
	if in.cwd, in.workspace, err = processPlace(); err != nil {
		return finishInput{}, err
	}

	in.suffix, in.branch = req.suffix, req.branch
	in.flags.merge, in.flags.leaveOpen = req.pr == finishMerge, req.pr == finishLeaveOpen
	in.progress, in.handoff, in.deferred = req.progress, req.handoff, req.deferred
	// a driven finish asks no second question, since a resume carries only
	// the latest answer: a bundle's riders stay open, the reversible choice,
	// and the report names them
	in.close = []string{}

	return in, nil
}

// runFinish runs finish in process; its refusals halt the task, except a PR
// someone else opened, which asks.
func (in *driveInput) runFinish(ctx context.Context, w io.Writer, r *driveRun) driveResult {
	task, id := r.task, r.task.ref.ID

	pr, err := in.finishAnswer(id)
	if err != nil {
		return halted(id, "finish", err.Error())
	}

	fmt.Fprintf(w, "%s finish: running\n", in.id(id))

	in.ranFinish = true

	var (
		hand   []string
		pulled string
	)

	buf := &linkBuffer{links: linkTerminal(w)}

	in.finishStep(ctx, w, r.o.branch, branchmeta.FinishRunning, "starting")

	err = in.finish(ctx, buf, driveFinish{
		task:   task.ref,
		suffix: task.suffix,
		branch: r.o.branch,
		pr:     pr,
		progress: func(action string) {
			in.finishStep(ctx, w, r.o.branch, branchmeta.FinishRunning, action)
		},
		handoff:  func(lines []string) { hand = lines },
		deferred: func(note string) { pulled, in.pullLine = note, note },
	})

	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		fmt.Fprintf(w, "  %s\n", sc.Text())
	}

	if fp := (foreignPullError{}); errors.As(err, &fp) {
		ask := fmt.Sprintf("1. %s was opened by %s: [%s | %s]", fp.url, fp.author, finishLeaveOpen, finishMerge)

		return driveResult{
			row:   "asked at finish",
			code:  ui.ExitAsk,
			lines: []string{id + " asks at finish: " + ask},
			ask:   ask,
		}
	}

	if err != nil {
		return halted(id, "finish", err.Error())
	}

	if in.record != nil {
		in.record(ctx, in.main, finishedEvent(id, "", in.now()))
	}

	var lines []string
	if pulled != "" {
		// finished all the same: the merge is on origin, and the drive retries the pull
		lines = append(lines, id+" finished; "+pulled)
	}

	return driveResult{row: rowFinished, lines: append(lines, handoffBlock(id, hand)...)}
}

// retryPull retries a pull a finish deferred, once a tick, in a drive that
// ran a finish, since pulling is an act; a line already printed is not
// printed again, so a diverged main is said once.
func (in *driveInput) retryPull(ctx context.Context, w io.Writer) {
	if !in.ranFinish {
		return
	}

	line, _ := retryPendingPull(ctx, in.root, in.main)
	if line == "" || line == in.pullLine {
		return
	}

	in.pullLine = line
	fmt.Fprintln(w, line)
}

// finishStep records finish's progress on the branch for status; a failed
// write is a line, never a halt, since the record is only a view.
func (in *driveInput) finishStep(ctx context.Context, w io.Writer, branch, st, action string) {
	if branch == "" {
		return
	}

	rec := branchmeta.DriveFinish{State: st, Action: action}
	if err := branchmeta.Set(ctx, in.main, branch, branchmeta.Finish, rec.Format()); err != nil {
		fmt.Fprintf(w, "  recording finish's progress on %s: %v\n", branch, err)
	}
}

// parkFinish sets the record back to queued with no action when the drive
// stops short of finish: status reads the record as "drive finishes this
// branch", so it stays, and never claims a finish running with no drive.
func (in *driveInput) parkFinish(ctx context.Context, r *driveRun) {
	if !r.opened || !r.finishes {
		return
	}

	_ = branchmeta.Set(ctx, in.main, r.o.branch, branchmeta.Finish,
		branchmeta.DriveFinish{State: branchmeta.FinishQueued}.Format()) // a view only
}
