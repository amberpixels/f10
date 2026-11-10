package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// drive runs the flow; nothing is created or prompted before the graph
// passed validation.
func drive(ctx context.Context, w io.Writer, in *driveInput) (err error) {
	in.out, in.leases = w, map[string]*drives.Lease{}
	in.resolveProfiles()

	tasks, notes, err := in.resolveTasks(ctx)
	if err != nil {
		return err
	}

	order, graphNotes, err := in.graph(ctx, tasks)
	if err != nil {
		return err
	}

	notes = append(notes, graphNotes...)

	if err := in.openRecord(tasks); err != nil {
		return err
	}

	if err := in.recordLaunch(); err != nil {
		notes = append(notes, "the drive was not recorded as this session's launch: "+err.Error())
	}

	// runs after releaseAll: an answer claimed while this drive still held
	// its lease is seen here, or by its claimer once the lease is free
	defer func() {
		if !in.resumeAnswered(tasks) {
			return
		}

		// the exit code is the daemon's cue, so a failure is said here
		if _, coded := errors.AsType[cli.ExitCoder](err); err != nil && !coded {
			fmt.Fprintln(w, "f10:", err)
		}

		err = cli.Exit("", daemon.ExitResume)
	}()
	defer in.releaseAll()

	if err := in.takeAnswers(tasks); err != nil {
		return err
	}

	in.takeInbox(tasks)

	if in.rec != nil {
		fmt.Fprintf(w, "drive %d\n", in.rec.Number)
		in.savePending(nil)

		// the daemon running this drive learns its number here
		if path := os.Getenv(daemon.NumberFileEnv); path != "" {
			//nolint:gosec // the daemon names the path, as this same user
			if err := os.WriteFile(path, []byte(strconv.Itoa(in.rec.Number)+"\n"), 0o600); err != nil {
				fmt.Fprintf(w, "  telling the daemon the drive's number: %v\n", err)
			}
		}
	}

	in.loadLinks(ctx, tasks)

	runs := make([]*driveRun, 0, len(order))
	byID := map[string]*driveRun{}

	for _, i := range order {
		r := &driveRun{task: tasks[i], lastCatchup: in.savedCatchup(tasks[i])}
		if r.task.finished {
			in.end(ctx, r, driveResult{row: driverec.RowAlreadyFinished})
		}

		runs = append(runs, r)

		for _, id := range r.task.ref.Members() {
			if _, seen := byID[id]; !seen {
				byID[id] = r
			}
		}
	}

	// a base outside the list was refused unless finished
	for _, r := range runs {
		r.base = byID[r.task.base]
	}

	in.runs = runs

	// every task this drive may act on is its own before anything opens,
	// so a second drive cannot start one this one is waiting to run
	for _, r := range runs {
		if r.end != nil {
			continue
		}

		if why, ok := in.lease(r); !ok {
			in.end(ctx, r, driveResult{row: why, code: ui.ExitHalted, drives: true})
		}
	}

	if err := in.startRunnable(ctx, runs, nil); err != nil {
		return err
	}

	defer in.leaveRuns(context.WithoutCancel(ctx), runs)

	for !stopping(runs) {
		if err := in.sleep(ctx, in.args.Every); err != nil {
			return err
		}

		if in.draining.Load() {
			fmt.Fprintf(w, "drive %d drained for an upgrade; the new daemon resumes it\n", in.number())

			return cli.Exit("", daemon.ExitDrained)
		}

		in.refreshRecord()

		if in.rec != nil && in.rec.Cancelled != nil {
			for _, r := range runs {
				if r.end == nil {
					in.end(ctx, r, driveResult{row: "cancelled", code: ui.ExitHalted, drives: true})
				}
			}

			break
		}

		in.retryPull(ctx)

		view, err := in.lookAll(ctx)
		if err != nil {
			return err
		}

		in.tick, in.wss = view, view.wss
		wss := view.wss

		for _, r := range runs {
			if !r.active() {
				continue
			}

			if err := in.onTask(ctx, r, in.poll(ctx, r)); err != nil {
				return fmt.Errorf("%s: %w", r.task.ref.ID, err)
			}
		}

		if err := in.markMoved(ctx, runs, wss); err != nil {
			return err
		}

		if err := in.startRunnable(ctx, runs, wss); err != nil {
			return err
		}
	}

	// a task read for the first time during the drive is in the ledger now
	in.loadLinks(ctx, tasks)

	return in.report(runs, append(notes, in.inboxNotes...))
}

// loadLinks reads the urls of the drive's tasks and their bases; a drive
// the daemon runs writes to a log, so it links nothing.
func (in *driveInput) loadLinks(ctx context.Context, tasks []driveTask) {
	if in.links == nil {
		in.links = render.LinksFor(in.out, false)
		if in.links.On {
			in.project = target.ProjectOf(ctx, in.main)
		}
	}

	ids := make([]string, 0, 2*len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ref.Members()...)
		if t.base != "" {
			ids = append(ids, t.base)
		}
	}

	in.links.Load(ctx, in.project, ids...)
}

// id is a task id as the drive prints it for people.
func (in *driveInput) id(id string) string {
	return in.links.ID(in.project, id)
}

// resolveProfiles resolves each named profile once over the layers (the
// run-wide flag, then a task's own, each as the map's default, the map's own
// under "") and says what it corrected or fell back on before anything opens.
func (in *driveInput) resolveProfiles() {
	in.profiles = map[string]agentctl.Profile{}

	base := in.layers.With(in.args.Agent, runplan.AgentFlagOrigin)

	for _, raw := range append([]string{""}, in.args.Profiles...) {
		if _, done := in.profiles[raw]; done {
			continue
		}

		r := base.With(raw, "<id>:"+raw).ProfileFor(facts.AgentKeyDefault, "")
		in.profiles[raw] = r.Profile

		for _, line := range r.Lines(in.settings) {
			fmt.Fprintln(in.out, line)
		}
	}
}

// profileFor is the resolved profile of the task listed at i: its own,
// else the run-wide one over the map's default. A range's tasks have no
// index of their own, i < 0.
func (in *driveInput) profileFor(i int) agentctl.Profile {
	if i >= 0 && i < len(in.args.Profiles) && in.args.Profiles[i] != "" {
		return in.profiles[in.args.Profiles[i]]
	}

	return in.profiles[""]
}

// profileFlagged says the command line chose the task's profile, so a
// rerun does not keep the one its branch recorded.
func (in *driveInput) profileFlagged(i int) bool {
	return in.args.Agent != "" || i >= 0 && i < len(in.args.Profiles) && in.args.Profiles[i] != ""
}

// stopping reports whether the drive ends: no task is active, or one asks
// and every active task is held on a base. An ask does not stop the rest,
// so a guard stop elsewhere still gets its catchup while the ask waits.
func stopping(runs []*driveRun) bool {
	asked, active, moving := false, false, false

	for _, r := range runs {
		asked = asked || r.end != nil && r.end.code == ui.ExitAsk
		active = active || r.active()
		moving = moving || r.active() && r.phase != atParked && r.phase != atWaiting
	}

	return !active || asked && !moving
}

// startRunnable walks runs in graph order, so a base released earlier in
// the same pass releases its dependents in it. Under the cap, tasks past it
// queue; wss is the tick's look, nil before the first.
func (in *driveInput) startRunnable(
	ctx context.Context,
	runs []*driveRun,
	wss []agentctl.Workspace,
) error {
	gate, err := in.openGate(ctx, runs, wss)
	if err != nil {
		return err
	}

	for _, r := range gate.order(runs) {
		if r.opened() || r.end != nil {
			continue
		}

		if r.base != nil && !in.released(ctx, r.base) {
			continue
		}

		if !gate.admit(in, r) {
			in.queue(r)

			continue
		}

		in.unqueue(r)

		if err := in.onTask(ctx, r, in.open(ctx, r)); err != nil {
			return fmt.Errorf("%s: %w", r.task.ref.ID, err)
		}

		if r.end != nil {
			continue
		}

		if err := in.onTask(ctx, r, in.next(ctx, r)); err != nil {
			return fmt.Errorf("%s: %w", r.task.ref.ID, err)
		}
	}

	return nil
}

// released: a base lets its dependents start once through, or once it has
// code of its own, which only a commit or pr step makes with verify green.
func (in *driveInput) released(ctx context.Context, b *driveRun) bool {
	if b.through() {
		return true
	}

	if !b.opened() || b.end != nil {
		return false
	}

	def, err := stack.LocalDefault(ctx, in.main)
	if err != nil {
		return false
	}

	return stack.BaseHasCode(ctx, in.main, b.o.Branch, def)
}

// markMoved marks a running dependent whose base branch moved past it, read
// while the base agent is not mid-turn, so its next boundary catches up; a
// catchup in flight clears the mark itself, and a finished base is landed's.
func (in *driveInput) markMoved(ctx context.Context, runs []*driveRun, wss []agentctl.Workspace) error {
	for _, r := range runs {
		b := r.base
		// a parked ship's own guard brings the base in when it resumes, and
		// so does one the cap queued after parking; an attached turn may be
		// a catchup, which a mark mid-turn would halt
		if !r.active() || r.phase == atParked || r.queued() || r.skill == "catchup" || r.attached || b == nil ||
			!b.opened() {
			continue
		}

		if b.end == nil {
			if ws := finish.WorkspaceByID(wss, b.o.Ws.ID); ws == nil || ws.Status == agentctl.StatusWorking {
				continue
			}
		}

		// a read git cannot answer skips the task for this poll: the next
		// poll reads again, and a wrong "moved" would send a catchup
		head, err := gitx.Ref(ctx, in.main, "refs/heads/"+b.o.Branch)
		if err != nil {
			fmt.Fprintf(in.out, "%s: skipped this poll, reading %s: %v\n", in.id(r.task.ref.ID), b.o.Branch, err)

			continue
		}

		if head == "" {
			continue
		}

		if has, err := gitx.IsAncestor(ctx, in.main, head, "refs/heads/"+r.o.Branch); has || err != nil {
			if err != nil {
				fmt.Fprintf(in.out, "%s: skipped this poll, reading %s: %v\n", in.id(r.task.ref.ID), r.o.Branch, err)
			}

			continue
		}

		marked, err := branchmeta.Get(ctx, in.main, r.o.Branch, branchmeta.Moved)
		if err != nil {
			fmt.Fprintf(in.out, "%s: skipped this poll, reading %s: %v\n",
				in.id(r.task.ref.ID), branchmeta.Moved.For(r.o.Branch), err)

			continue
		}

		if marked == head {
			continue
		}

		if err := branchmeta.Set(ctx, in.main, r.o.Branch, branchmeta.Moved, head); err != nil {
			return fmt.Errorf("%s: marking %s moved: %w", r.task.ref.ID, b.o.Branch, err)
		}

		fmt.Fprintf(in.out, "%s: base %s moved, catching up before the next skill\n", in.id(r.task.ref.ID), b.o.Branch)
	}

	return nil
}

// markBehind marks a pushed branch whose pull request's target moved past it,
// at the boundary before its next skill, so the open PR never sits behind; a
// listed base still in flight is markMoved's, and finish has readyToMerge.
func (in *driveInput) markBehind(ctx context.Context, r *driveRun) error {
	id, branch := r.task.ref.ID, r.o.Branch

	if r.base != nil && !r.base.through() {
		return nil
	}

	skip := func(what string, err error) error {
		fmt.Fprintf(in.out, "%s: skipped the target check, %s: %v\n", in.id(id), what, err)

		return nil
	}

	pushed, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+branch)
	if err != nil {
		return skip("reading origin/"+branch, err)
	}

	if pushed == "" {
		return nil
	}

	if err := in.view().fetch.Fetch(ctx, in.main); err != nil {
		return skip("fetching origin", err)
	}

	aim, err := in.prTarget(ctx, r)
	if err != nil {
		return skip("reading what "+branch+" targets", err)
	}

	// a base gone from everywhere is nobody's aim until finish releases the dependent
	if aim.SHA == "" {
		return nil
	}

	key, err := in.mark(ctx, r)
	if err != nil {
		return skip("reading the marks on "+branch, err)
	}

	if key != "" {
		return nil
	}

	has, err := gitx.IsAncestor(ctx, in.main, aim.SHA, "refs/heads/"+branch)
	if err != nil {
		return skip("reading "+branch, err)
	}

	if has {
		return nil
	}

	// a PR retargeted by hand: a catchup with git's base would carry another
	// branch's commits into it
	if in.pull != nil {
		if pr, err := in.pull(ctx, branch); err == nil && pr.Status == forge.Open {
			if st, err := stack.ReadTarget(ctx, in.main, aim, pr.Base, "", ""); err == nil && st.Retargeted {
				return fmt.Errorf("%s targets %s, not %s, which %s was started on: retarget it back, "+
					"or catch it up by hand with /f10:catchup in %s", pr.URL, pr.Base,
					strings.TrimPrefix(aim.Name, "origin/"), branch, r.o.Path)
			}
		}
	}

	if err := branchmeta.Set(ctx, in.main, branch, aim.Key, aim.Value); err != nil {
		return fmt.Errorf("marking %s behind %s: %w", branch, aim.Name, err)
	}

	fmt.Fprintf(in.out, "%s: %s moved past %s, catching up before the next skill\n", in.id(id), aim.Name, branch)

	return nil
}

// savedCatchup is the catchup record an earlier run of this drive kept for
// t, so a resumed drive leases its push as the first one would have.
func (in *driveInput) savedCatchup(t driveTask) *drives.Catchup {
	if in.rec == nil {
		return nil
	}

	for _, id := range []string{t.ref.Label() + worktree.SuffixOf(t.suffix), t.ref.ID + worktree.SuffixOf(t.suffix)} {
		if rt := in.rec.Task(id); rt != nil && rt.Catchup != nil {
			c := *rt.Catchup

			return &c
		}
	}

	return nil
}
