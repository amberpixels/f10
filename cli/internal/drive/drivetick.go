package drive

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

// Drive runs the flow; nothing is created or prompted before the graph
// passed validation.
func Drive(ctx context.Context, w io.Writer, in *DriveInput) (err error) {
	in.Out, in.leases = w, map[string]*drives.Lease{}
	in.resolveProfiles()

	tasks, notes, err := in.ResolveTasks(ctx)
	if err != nil {
		return err
	}

	order, graphNotes, err := in.Graph(ctx, tasks)
	if err != nil {
		return err
	}

	notes = append(notes, graphNotes...)

	if err := in.OpenRecord(tasks); err != nil {
		return err
	}

	if err := in.RecordLaunch(); err != nil {
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
	defer in.ReleaseAll()

	if err := in.TakeAnswers(tasks); err != nil {
		return err
	}

	in.takeInbox(tasks)

	if in.Rec != nil {
		fmt.Fprintf(w, "drive %d\n", in.Rec.Number)
		in.savePending(nil)

		// the daemon running this drive learns its number here
		if path := os.Getenv(daemon.NumberFileEnv); path != "" {
			//nolint:gosec // the daemon names the path, as this same user
			if err := os.WriteFile(path, []byte(strconv.Itoa(in.Rec.Number)+"\n"), 0o600); err != nil {
				fmt.Fprintf(w, "  telling the daemon the drive's number: %v\n", err)
			}
		}
	}

	in.LoadLinks(ctx, tasks)

	runs := make([]*DriveRun, 0, len(order))
	byID := map[string]*DriveRun{}

	for _, i := range order {
		r := &DriveRun{Task: tasks[i], LastCatchup: in.SavedCatchup(tasks[i])}
		if r.Task.finished {
			in.End(ctx, r, DriveResult{Row: driverec.RowAlreadyFinished})
		}

		runs = append(runs, r)

		for _, id := range r.Task.Ref.Members() {
			if _, seen := byID[id]; !seen {
				byID[id] = r
			}
		}
	}

	// a base outside the list was refused unless finished
	for _, r := range runs {
		r.Base = byID[r.Task.Base]
	}

	in.Runs = runs

	// every task this drive may act on is its own before anything opens,
	// so a second drive cannot start one this one is waiting to run
	for _, r := range runs {
		if r.End != nil {
			continue
		}

		if why, ok := in.lease(r); !ok {
			in.End(ctx, r, DriveResult{Row: why, Code: ui.ExitHalted, Drives: true})
		}
	}

	if err := in.startRunnable(ctx, runs, nil); err != nil {
		return err
	}

	defer in.LeaveRuns(context.WithoutCancel(ctx), runs)

	for !Stopping(runs) {
		if err := in.Sleep(ctx, in.Args.Every); err != nil {
			return err
		}

		if in.Draining.Load() {
			fmt.Fprintf(w, "drive %d drained for an upgrade; the new daemon resumes it\n", in.number())

			return cli.Exit("", daemon.ExitDrained)
		}

		in.refreshRecord()

		if in.Rec != nil && in.Rec.Cancelled != nil {
			for _, r := range runs {
				if r.End == nil {
					in.End(ctx, r, DriveResult{Row: "cancelled", Code: ui.ExitHalted, Drives: true})
				}
			}

			break
		}

		in.RetryPull(ctx)

		view, err := in.lookAll(ctx)
		if err != nil {
			return err
		}

		in.Tick, in.Wss = view, view.Wss
		wss := view.Wss

		for _, r := range runs {
			if !r.active() {
				continue
			}

			if err := in.OnTask(ctx, r, in.poll(ctx, r)); err != nil {
				return fmt.Errorf("%s: %w", r.Task.Ref.ID, err)
			}
		}

		if err := in.MarkMoved(ctx, runs, wss); err != nil {
			return err
		}

		if err := in.startRunnable(ctx, runs, wss); err != nil {
			return err
		}
	}

	// a task read for the first time during the drive is in the ledger now
	in.LoadLinks(ctx, tasks)

	return in.Report(runs, append(notes, in.inboxNotes...))
}

// LoadLinks reads the urls of the drive's tasks and their bases; a drive
// the daemon runs writes to a log, so it links nothing.
func (in *DriveInput) LoadLinks(ctx context.Context, tasks []DriveTask) {
	if in.links == nil {
		in.links = render.LinksFor(in.Out, false)
		if in.links.On {
			in.project = target.ProjectOf(ctx, in.Main)
		}
	}

	ids := make([]string, 0, 2*len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.Ref.Members()...)
		if t.Base != "" {
			ids = append(ids, t.Base)
		}
	}

	in.links.Load(ctx, in.project, ids...)
}

// id is a task id as the drive prints it for people.
func (in *DriveInput) id(id string) string {
	return in.links.ID(in.project, id)
}

// resolveProfiles resolves each named profile once over the layers (the
// run-wide flag, then a task's own, each as the map's default, the map's own
// under "") and says what it corrected or fell back on before anything opens.
func (in *DriveInput) resolveProfiles() {
	in.profiles = map[string]agentctl.Profile{}

	base := in.Layers.With(in.Args.Agent, runplan.AgentFlagOrigin)

	for _, raw := range append([]string{""}, in.Args.Profiles...) {
		if _, done := in.profiles[raw]; done {
			continue
		}

		r := base.With(raw, "<id>:"+raw).ProfileFor(facts.AgentKeyDefault, "")
		in.profiles[raw] = r.Profile

		for _, line := range r.Lines(in.Settings) {
			fmt.Fprintln(in.Out, line)
		}
	}
}

// profileFor is the resolved profile of the task listed at i: its own,
// else the run-wide one over the map's default. A range's tasks have no
// index of their own, i < 0.
func (in *DriveInput) profileFor(i int) agentctl.Profile {
	if i >= 0 && i < len(in.Args.Profiles) && in.Args.Profiles[i] != "" {
		return in.profiles[in.Args.Profiles[i]]
	}

	return in.profiles[""]
}

// profileFlagged says the command line chose the task's profile, so a
// rerun does not keep the one its branch recorded.
func (in *DriveInput) profileFlagged(i int) bool {
	return in.Args.Agent != "" || i >= 0 && i < len(in.Args.Profiles) && in.Args.Profiles[i] != ""
}

// Stopping reports whether the drive ends: no task is active, or one asks
// and every active task is held on a base. An ask does not stop the rest,
// so a guard stop elsewhere still gets its catchup while the ask waits.
func Stopping(runs []*DriveRun) bool {
	asked, active, moving := false, false, false

	for _, r := range runs {
		asked = asked || r.End != nil && r.End.Code == ui.ExitAsk
		active = active || r.active()
		moving = moving || r.active() && r.Phase != AtParked && r.Phase != AtWaiting
	}

	return !active || asked && !moving
}

// startRunnable walks runs in graph order, so a base released earlier in
// the same pass releases its dependents in it. Under the cap, tasks past it
// queue; wss is the tick's look, nil before the first.
func (in *DriveInput) startRunnable(
	ctx context.Context,
	runs []*DriveRun,
	wss []agentctl.Workspace,
) error {
	gate, err := in.openGate(ctx, runs, wss)
	if err != nil {
		return err
	}

	for _, r := range gate.Order(runs) {
		if r.Opened() || r.End != nil {
			continue
		}

		if r.Base != nil && !in.Released(ctx, r.Base) {
			continue
		}

		if !gate.Admit(in, r) {
			in.Queue(r)

			continue
		}

		in.Unqueue(r)

		if err := in.OnTask(ctx, r, in.open(ctx, r)); err != nil {
			return fmt.Errorf("%s: %w", r.Task.Ref.ID, err)
		}

		if r.End != nil {
			continue
		}

		if err := in.OnTask(ctx, r, in.Next(ctx, r)); err != nil {
			return fmt.Errorf("%s: %w", r.Task.Ref.ID, err)
		}
	}

	return nil
}

// Released: a base lets its dependents start once through, or once it has
// code of its own, which only a commit or pr step makes with verify green.
func (in *DriveInput) Released(ctx context.Context, b *DriveRun) bool {
	if b.through() {
		return true
	}

	if !b.Opened() || b.End != nil {
		return false
	}

	def, err := stack.LocalDefault(ctx, in.Main)
	if err != nil {
		return false
	}

	return stack.BaseHasCode(ctx, in.Main, b.O.Branch, def)
}

// MarkMoved marks a running dependent whose base branch moved past it, read
// while the base agent is not mid-turn, so its next boundary catches up; a
// catchup in flight clears the mark itself, and a finished base is landed's.
func (in *DriveInput) MarkMoved(ctx context.Context, runs []*DriveRun, wss []agentctl.Workspace) error {
	for _, r := range runs {
		b := r.Base
		// a parked ship's own guard brings the base in when it resumes, and
		// so does one the cap queued after parking; an attached turn may be
		// a catchup, which a mark mid-turn would halt
		if !r.active() || r.Phase == AtParked || r.Queued() || r.Skill == "catchup" || r.attached || b == nil ||
			!b.Opened() {
			continue
		}

		if b.End == nil {
			if ws := finish.WorkspaceByID(wss, b.O.Ws.ID); ws == nil || ws.Status == agentctl.StatusWorking {
				continue
			}
		}

		// a read git cannot answer skips the task for this poll: the next
		// poll reads again, and a wrong "moved" would send a catchup
		head, err := gitx.Ref(ctx, in.Main, "refs/heads/"+b.O.Branch)
		if err != nil {
			fmt.Fprintf(in.Out, "%s: skipped this poll, reading %s: %v\n", in.id(r.Task.Ref.ID), b.O.Branch, err)

			continue
		}

		if head == "" {
			continue
		}

		if has, err := gitx.IsAncestor(ctx, in.Main, head, "refs/heads/"+r.O.Branch); has || err != nil {
			if err != nil {
				fmt.Fprintf(in.Out, "%s: skipped this poll, reading %s: %v\n", in.id(r.Task.Ref.ID), r.O.Branch, err)
			}

			continue
		}

		marked, err := branchmeta.Get(ctx, in.Main, r.O.Branch, branchmeta.Moved)
		if err != nil {
			fmt.Fprintf(in.Out, "%s: skipped this poll, reading %s: %v\n",
				in.id(r.Task.Ref.ID), branchmeta.Moved.For(r.O.Branch), err)

			continue
		}

		if marked == head {
			continue
		}

		if err := branchmeta.Set(ctx, in.Main, r.O.Branch, branchmeta.Moved, head); err != nil {
			return fmt.Errorf("%s: marking %s moved: %w", r.Task.Ref.ID, b.O.Branch, err)
		}

		fmt.Fprintf(in.Out, "%s: base %s moved, catching up before the next skill\n", in.id(r.Task.Ref.ID), b.O.Branch)
	}

	return nil
}

// MarkBehind marks a pushed branch whose pull request's target moved past it,
// at the boundary before its next skill, so the open PR never sits behind; a
// listed base still in flight is markMoved's, and finish has readyToMerge.
func (in *DriveInput) MarkBehind(ctx context.Context, r *DriveRun) error {
	id, branch := r.Task.Ref.ID, r.O.Branch

	if r.Base != nil && !r.Base.through() {
		return nil
	}

	skip := func(what string, err error) error {
		fmt.Fprintf(in.Out, "%s: skipped the target check, %s: %v\n", in.id(id), what, err)

		return nil
	}

	pushed, err := gitx.Ref(ctx, in.Main, "refs/remotes/origin/"+branch)
	if err != nil {
		return skip("reading origin/"+branch, err)
	}

	if pushed == "" {
		return nil
	}

	if err := in.View().fetch.Fetch(ctx, in.Main); err != nil {
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

	has, err := gitx.IsAncestor(ctx, in.Main, aim.SHA, "refs/heads/"+branch)
	if err != nil {
		return skip("reading "+branch, err)
	}

	if has {
		return nil
	}

	// a PR retargeted by hand: a catchup with git's base would carry another
	// branch's commits into it
	if in.Pull != nil {
		if pr, err := in.Pull(ctx, branch); err == nil && pr.Status == forge.Open {
			if st, err := stack.ReadTarget(ctx, in.Main, aim, pr.Base, "", ""); err == nil && st.Retargeted {
				return fmt.Errorf("%s targets %s, not %s, which %s was started on: retarget it back, "+
					"or catch it up by hand with /f10:catchup in %s", pr.URL, pr.Base,
					strings.TrimPrefix(aim.Name, "origin/"), branch, r.O.Path)
			}
		}
	}

	if err := branchmeta.Set(ctx, in.Main, branch, aim.Key, aim.Value); err != nil {
		return fmt.Errorf("marking %s behind %s: %w", branch, aim.Name, err)
	}

	fmt.Fprintf(in.Out, "%s: %s moved past %s, catching up before the next skill\n", in.id(id), aim.Name, branch)

	return nil
}

// SavedCatchup is the catchup record an earlier run of this drive kept for
// t, so a resumed drive leases its push as the first one would have.
func (in *DriveInput) SavedCatchup(t DriveTask) *drives.Catchup {
	if in.Rec == nil {
		return nil
	}

	for _, id := range []string{t.Ref.Label() + worktree.SuffixOf(t.Suffix), t.Ref.ID + worktree.SuffixOf(t.Suffix)} {
		if rt := in.Rec.Task(id); rt != nil && rt.Catchup != nil {
			c := *rt.Catchup

			return &c
		}
	}

	return nil
}
