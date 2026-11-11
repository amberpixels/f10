package drive

import (
	"bufio"
	"context"
	"errors"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// readyToMerge brings the branch to where it merges cleanly, or says why not.
// A pushed branch is tried as origin's copy against origin's default branch,
// since the host merges that; one origin lacks against the local default,
// since finish merges it here.
func (in *DriveInput) readyToMerge(ctx context.Context, r *DriveRun) error {
	id, branch := r.Task.Ref.ID, r.O.Branch

	def, err := stack.LocalDefault(ctx, in.Main)
	if err != nil {
		return Halt(id, "finish", err.Error())
	}

	if err := in.View().fetch.Fetch(ctx, in.Main); err != nil {
		return Halt(id, "finish", err.Error())
	}

	pushed, err := gitx.Ref(ctx, r.O.Path, "origin/"+branch)
	if err != nil {
		return Halt(id, "finish", err.Error())
	}

	base, head := "origin/"+def, pushed

	if pushed == "" {
		if base, err = finish.LandingBase(ctx, in.Main, def); err != nil {
			return Halt(id, "finish", err.Error())
		}

		head = "refs/heads/" + branch
	}

	// every read before a push halts on a git that cannot answer: a wrong
	// "not an ancestor" would pick the lease and rewrite origin's branch
	clean, err := gitx.MergesCleanly(ctx, r.O.Path, base, head)
	if err != nil {
		return Halt(id, "finish", err.Error())
	}

	if clean {
		return nil
	}

	caughtUp := false
	if pushed != "" {
		if caughtUp, err = gitx.IsAncestor(ctx, r.O.Path, base, "HEAD"); err != nil {
			return Halt(id, "finish", err.Error())
		}
	}

	// after a catchup, origin's copy is the tree before it: HEAD is what the
	// catchup left and what the push would send, so HEAD is what is tried
	if !caughtUp && pushed != "" && r.LastCatchup != nil {
		if caughtUp, err = gitx.MergesCleanly(ctx, r.O.Path, base, "HEAD"); err != nil {
			return Halt(id, "finish", err.Error())
		}
	}

	if caughtUp {
		return in.pushAfterCatchup(ctx, r, "finish", base)
	}

	if err := in.conflictAttempt(ctx, r, "finish", branchmeta.Landed); err != nil {
		return err
	}

	if err := branchmeta.Set(ctx, in.Main, branch, branchmeta.Landed, def); err != nil {
		return Halt(id, "finish", fmt.Sprintf("marking %s landed on %s: %v", branch, def, err))
	}

	fmt.Fprintf(in.Out, "%s finish: %s conflicts with %s, catching up first\n", in.id(id), branch, base)
	in.finishStep(ctx, branch, branchmeta.FinishQueued, "catching up with "+base)

	return nil
}

// The answers drive's own finish question takes, for a PR someone else
// opened: merge it, or clean up locally and leave it open.
const (
	FinishMerge     = "merge"
	FinishLeaveOpen = "local"
)

// DriveFinish is what a drive's finish needs from its run; the rest comes
// from the project's settings.
type DriveFinish struct {
	Task     ref.Ref
	Suffix   string
	Branch   string // the run's branch, "" to look it up by task and suffix
	Pr       string // the answer to a foreign PR: FinishMerge, FinishLeaveOpen or ""
	Progress func(string)
	Handoff  func([]string)
	Deferred func(string)
}

func DriveFinishInput(t *target.Target, main string, h hostops.Host, req DriveFinish) (finish.FinishInput, error) {
	in := finish.InputFor(t, main, h, req.Task)

	var err error
	if in.Cwd, in.Workspace, err = finish.ProcessPlace(); err != nil {
		return finish.FinishInput{}, err
	}

	in.Suffix, in.Branch = req.Suffix, req.Branch
	in.Flags.Merge, in.Flags.LeaveOpen = req.Pr == FinishMerge, req.Pr == FinishLeaveOpen
	in.Progress, in.Handoff, in.Deferred = req.Progress, req.Handoff, req.Deferred
	// a driven finish asks no second question, since a resume carries only
	// the latest answer: a bundle's riders stay open, the reversible choice,
	// and the report names them
	in.Close = []string{}

	return in, nil
}

// RunFinish runs finish in process; its refusals halt the task, except a PR
// someone else opened, which asks.
func (in *DriveInput) RunFinish(ctx context.Context, r *DriveRun) DriveResult {
	task, id := r.Task, r.Task.Ref.ID

	pr, err := in.finishAnswer(id)
	if err != nil {
		return Halted(id, "finish", err.Error())
	}

	fmt.Fprintf(in.Out, "%s finish: running\n", in.id(id))

	in.RanFinish = true

	var (
		hand   []string
		pulled string
	)

	buf := &render.LinkBuffer{Links: render.LinkTerminal(in.Out)}

	in.finishStep(ctx, r.O.Branch, branchmeta.FinishRunning, "starting")

	err = in.Finish(ctx, buf, DriveFinish{
		Task:   task.Ref,
		Suffix: task.Suffix,
		Branch: r.O.Branch,
		Pr:     pr,
		Progress: func(action string) {
			in.finishStep(ctx, r.O.Branch, branchmeta.FinishRunning, action)
		},
		Handoff:  func(lines []string) { hand = lines },
		Deferred: func(note string) { pulled, in.PullLine = note, note },
	})

	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		fmt.Fprintf(in.Out, "  %s\n", sc.Text())
	}

	if fp := (finish.ForeignPullError{}); errors.As(err, &fp) {
		ask := fmt.Sprintf("1. %s was opened by %s: [%s | %s]", fp.URL, fp.Author, FinishLeaveOpen, FinishMerge)

		return DriveResult{
			Row:   "asked at finish",
			Code:  ui.ExitAsk,
			Lines: []string{id + " asks at finish: " + ask},
			ask:   ask,
		}
	}

	if err != nil {
		return Halted(id, "finish", err.Error())
	}

	if in.Record != nil {
		in.Record(ctx, in.Main, finish.FinishedEvent(id, "", in.Now()))
	}

	var lines []string
	if pulled != "" {
		// finished all the same: the merge is on origin, and the drive retries the pull
		lines = append(lines, id+" finished; "+pulled)
	}

	return DriveResult{Row: driverec.RowFinished, Lines: append(lines, handoffBlock(id, hand)...)}
}

// RetryPull retries a pull a finish deferred, once a tick, in a drive that
// ran a finish, since pulling is an act; a line already printed is not
// printed again, so a diverged main is said once.
func (in *DriveInput) RetryPull(ctx context.Context) {
	if !in.RanFinish {
		return
	}

	line, _ := finish.RetryPendingPull(ctx, in.Root, in.Main)
	if line == "" || line == in.PullLine {
		return
	}

	in.PullLine = line
	fmt.Fprintln(in.Out, line)
}

// finishStep records finish's progress on the branch for status; a failed
// write is a line, never a halt, since the record is only a view.
func (in *DriveInput) finishStep(ctx context.Context, branch, st, action string) {
	if branch == "" {
		return
	}

	rec := branchmeta.DriveFinish{State: st, Action: action}
	if err := branchmeta.Set(ctx, in.Main, branch, branchmeta.Finish, rec.Format()); err != nil {
		fmt.Fprintf(in.Out, "  recording finish's progress on %s: %v\n", branch, err)
	}
}

// parkFinish sets the record back to queued with no action when the drive
// stops short of finish: status reads the record as "drive finishes this
// branch", so it stays, and never claims a finish running with no drive.
func (in *DriveInput) parkFinish(ctx context.Context, r *DriveRun) {
	if !r.Opened() || !r.Finishes {
		return
	}

	_ = branchmeta.Set(ctx, in.Main, r.O.Branch, branchmeta.Finish,
		branchmeta.DriveFinish{State: branchmeta.FinishQueued}.Format()) // a view only
}
