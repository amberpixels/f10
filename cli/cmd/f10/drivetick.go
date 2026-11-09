package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// drive runs the flow; nothing is created or prompted before the graph
// passed validation.
func drive(ctx context.Context, w io.Writer, in *driveInput) error {
	in.out, in.leases = w, map[string]*drives.Lease{}
	in.resolveProfiles(w)

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
	defer in.resumeAnswered(context.WithoutCancel(ctx), tasks)
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

	in.loadLinks(ctx, w, tasks)

	runs := make([]*driveRun, 0, len(order))
	byID := map[string]*driveRun{}

	for _, i := range order {
		r := &driveRun{task: tasks[i]}
		if r.task.finished {
			in.end(ctx, w, r, driveResult{row: rowAlreadyFinished})
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
			in.end(ctx, w, r, driveResult{row: why, code: exitHalted, drives: true})
		}
	}

	if err := in.startRunnable(ctx, w, runs, nil); err != nil {
		return err
	}

	defer in.leaveRuns(context.WithoutCancel(ctx), runs)

	for !stopping(runs) {
		if err := in.sleep(ctx, in.args.every); err != nil {
			return err
		}

		if in.draining.Load() {
			fmt.Fprintf(w, "drive %d drained for an upgrade; the new daemon resumes it\n", in.number())

			return cli.Exit("", exitDrained)
		}

		if in.cancelled() {
			for _, r := range runs {
				if r.end == nil {
					in.end(ctx, w, r, driveResult{row: "cancelled", code: exitHalted, drives: true})
				}
			}

			break
		}

		in.retryPull(ctx, w)

		wss, live, err := in.lookAll(ctx)
		if err != nil {
			return err
		}

		in.wss = wss

		for _, r := range runs {
			if !r.active() {
				continue
			}

			if err := in.poll(ctx, w, r, wss, live); err != nil {
				return fmt.Errorf("%s: %w", r.task.ref.ID, err)
			}
		}

		if err := in.markMoved(ctx, w, runs, wss); err != nil {
			return err
		}

		if err := in.startRunnable(ctx, w, runs, wss); err != nil {
			return err
		}
	}

	// a task read for the first time during the drive is in the ledger now
	in.loadLinks(ctx, w, tasks)

	return in.report(w, runs, append(notes, in.inboxNotes...))
}

// loadLinks reads the urls of the drive's tasks and their bases; a drive
// the daemon runs writes to a log, so it links nothing.
func (in *driveInput) loadLinks(ctx context.Context, w io.Writer, tasks []driveTask) {
	if in.links == nil {
		in.links = linksFor(w, false)
		if in.links.on {
			in.project = projectOf(ctx, in.main)
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
func (in *driveInput) resolveProfiles(w io.Writer) {
	in.profiles = map[string]agentctl.Profile{}

	base := in.layers.with(in.args.agent, agentFlagOrigin)

	for _, raw := range append([]string{""}, in.args.profiles...) {
		if _, done := in.profiles[raw]; done {
			continue
		}

		r := base.with(raw, "<id>:"+raw).profileFor(facts.AgentKeyDefault, "")
		in.profiles[raw] = r.Profile

		for _, line := range r.lines(in.settings) {
			fmt.Fprintln(w, line)
		}
	}
}

// profileFor is the resolved profile of the task listed at i: its own,
// else the run-wide one over the map's default. A range's tasks have no
// index of their own, i < 0.
func (in *driveInput) profileFor(i int) agentctl.Profile {
	if i >= 0 && i < len(in.args.profiles) && in.args.profiles[i] != "" {
		return in.profiles[in.args.profiles[i]]
	}

	return in.profiles[""]
}

// profileFlagged says the command line chose the task's profile, so a
// rerun does not keep the one its branch recorded.
func (in *driveInput) profileFlagged(i int) bool {
	return in.args.agent != "" || i >= 0 && i < len(in.args.profiles) && in.args.profiles[i] != ""
}

// stopping reports whether the drive ends: no task is active, or one asks
// and every active task is held on a base. An ask does not stop the rest,
// so a guard stop elsewhere still gets its catchup while the ask waits.
func stopping(runs []*driveRun) bool {
	asked, active, moving := false, false, false

	for _, r := range runs {
		asked = asked || r.end != nil && r.end.code == exitAsk
		active = active || r.active()
		moving = moving || r.active() && !r.parked && !r.waiting
	}

	return !active || asked && !moving
}

// startRunnable walks runs in graph order, so a base released earlier in
// the same pass releases its dependents in it. Under the cap, tasks past it
// queue; wss is the tick's look, nil before the first.
func (in *driveInput) startRunnable(
	ctx context.Context,
	w io.Writer,
	runs []*driveRun,
	wss []agentctl.Workspace,
) error {
	gate, err := in.openGate(ctx, runs, wss)
	if err != nil {
		return err
	}

	for _, r := range gate.order(runs) {
		if r.opened || r.end != nil {
			continue
		}

		if r.base != nil && !in.released(ctx, r.base) {
			continue
		}

		if !gate.admit(in, r) {
			in.queue(w, r)

			continue
		}

		in.unqueue(r)

		if err := in.open(ctx, w, r); err != nil {
			return fmt.Errorf("%s: %w", r.task.ref.ID, err)
		}

		if err := in.next(ctx, w, r); err != nil {
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

	if !b.opened || b.end != nil {
		return false
	}

	def, err := localDefault(ctx, in.main)
	if err != nil {
		return false
	}

	return baseHasCode(ctx, in.main, b.o.branch, def)
}

// createdAt is the commit branch was created at, from its oldest reflog
// entry, or "" once the reflog no longer reaches back to the creation.
func createdAt(ctx context.Context, dir, branch string) string {
	lines := strings.Split(gitx.Out(ctx, dir, "reflog", "show", "--format=%H %gs", "refs/heads/"+branch), "\n")

	sha, subject, _ := strings.Cut(lines[len(lines)-1], " ")
	if !strings.HasPrefix(subject, "branch: Created from") {
		return ""
	}

	return sha
}

// markMoved marks a running dependent whose base branch moved past it, read
// while the base agent is not mid-turn, so its next boundary catches up; a
// catchup in flight clears the mark itself, and a finished base is landed's.
func (in *driveInput) markMoved(ctx context.Context, w io.Writer, runs []*driveRun, wss []agentctl.Workspace) error {
	for _, r := range runs {
		b := r.base
		// a parked ship's own guard brings the base in when it resumes, and
		// so does one the cap queued after parking; an attached turn may be
		// a catchup, which a mark mid-turn would halt
		if !r.active() || r.parked || r.queued || r.skill == "catchup" || r.attached || b == nil || !b.opened {
			continue
		}

		if b.end == nil {
			if ws := workspaceByID(wss, b.o.ws.ID); ws == nil || ws.Status == agentctl.StatusWorking {
				continue
			}
		}

		// a read git cannot answer skips the task for this poll: the next
		// poll reads again, and a wrong "moved" would send a catchup
		head, err := gitx.Ref(ctx, in.main, "refs/heads/"+b.o.branch)
		if err != nil {
			fmt.Fprintf(w, "%s: skipped this poll, reading %s: %v\n", in.id(r.task.ref.ID), b.o.branch, err)

			continue
		}

		if head == "" {
			continue
		}

		if has, err := gitx.IsAncestor(ctx, in.main, head, "refs/heads/"+r.o.branch); has || err != nil {
			if err != nil {
				fmt.Fprintf(w, "%s: skipped this poll, reading %s: %v\n", in.id(r.task.ref.ID), r.o.branch, err)
			}

			continue
		}

		key := "branch." + r.o.branch + "." + cfgMoved

		marked, err := gitx.Config(ctx, in.main, key)
		if err != nil {
			fmt.Fprintf(w, "%s: skipped this poll, reading %s: %v\n", in.id(r.task.ref.ID), key, err)

			continue
		}

		if marked == head {
			continue
		}

		if err := gitx.SetConfig(ctx, in.main, key, head); err != nil {
			return fmt.Errorf("%s: marking %s moved: %w", r.task.ref.ID, b.o.branch, err)
		}

		fmt.Fprintf(w, "%s: base %s moved, catching up before the next skill\n", in.id(r.task.ref.ID), b.o.branch)
	}

	return nil
}

// markBehind marks a pushed branch whose pull request's target moved past it,
// at the boundary before its next skill, so the open PR never sits behind; a
// listed base still in flight is markMoved's, and finish has readyToMerge.
func (in *driveInput) markBehind(ctx context.Context, w io.Writer, r *driveRun) error {
	id, branch := r.task.ref.ID, r.o.branch

	if r.base != nil && !r.base.through() {
		return nil
	}

	skip := func(what string, err error) error {
		fmt.Fprintf(w, "%s: skipped the target check, %s: %v\n", in.id(id), what, err)

		return nil
	}

	pushed, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+branch)
	if err != nil {
		return skip("reading origin/"+branch, err)
	}

	if pushed == "" {
		return nil
	}

	if _, err := gitx.Exec(ctx, in.main, "fetch", "origin"); err != nil {
		return skip("fetching origin", err)
	}

	target, err := in.prTarget(ctx, r)
	if err != nil {
		return skip("reading what "+branch+" targets", err)
	}

	// a base gone from everywhere is nobody's target until finish releases the dependent
	if target.sha == "" {
		return nil
	}

	key, err := in.mark(ctx, r)
	if err != nil {
		return skip("reading the marks on "+branch, err)
	}

	if key != "" {
		return nil
	}

	has, err := gitx.IsAncestor(ctx, in.main, target.sha, "refs/heads/"+branch)
	if err != nil {
		return skip("reading "+branch, err)
	}

	if has {
		return nil
	}

	if err := gitx.SetConfig(ctx, in.main, "branch."+branch+"."+target.key, target.value); err != nil {
		return fmt.Errorf("marking %s behind %s: %w", branch, target.name, err)
	}

	fmt.Fprintf(w, "%s: %s moved past %s, catching up before the next skill\n", in.id(id), target.name, branch)

	return nil
}
