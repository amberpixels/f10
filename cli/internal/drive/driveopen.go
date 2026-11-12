package drive

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

func (in *DriveInput) open(ctx context.Context, r *DriveRun) error {
	task := r.Task

	after, note, err := stack.BodyAfter(ctx, in.Tracker, in.Main, task.Ref, task.info.Body)
	if err != nil {
		return err
	}

	var title func(context.Context) (string, error)
	if in.Title != nil {
		title = in.Title(task.Ref)
	}

	o, err := start.OpenTask(ctx, start.StartInput{
		Main:           in.Main,
		Driver:         in.Driver,
		Task:           task.Ref,
		Suffix:         task.Suffix,
		After:          after,
		Storage:        in.Storage,
		Title:          title,
		Profile:        task.profile,
		ProfileFlagged: task.flagged,
		Progress:       in.Out,
		FlowKept:       true,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(
		in.Out,
		"%s: branch %s at %s, workspace %s%s\n",
		in.id(task.Ref.ID),
		o.Branch,
		o.Path,
		o.Ws.ID,
		onProfile(o),
	)

	if o.Fresh {
		o.Notes = append(o.Notes, start.MarkStarted(ctx, in.Started, o.Task)...)
	}

	fl, flowNote, err := in.KeepFlow(ctx, o.Branch)
	if err != nil {
		return err
	}

	if flowNote != "" {
		o.Notes = append(o.Notes, flowNote)
	}

	fl, paceNote, err := in.KeepPace(ctx, o.Branch, fl)
	if err != nil {
		return err
	}

	if paceNote != "" {
		o.Notes = append(o.Notes, paceNote)
	}

	// status reads the finish record as drive finishing the branch; a rerun
	// at a pace without finish takes it off
	skills := in.SequenceOf(fl)

	finishes := slices.Contains(skills, "finish")
	if finishes {
		in.finishStep(ctx, o.Branch, branchmeta.FinishQueued, "")
	} else {
		_ = branchmeta.Unset(ctx, in.Main, o.Branch, branchmeta.Finish) // absent is fine
	}

	// drives before the skills were read off the flow stored them here
	_ = branchmeta.Unset(ctx, in.Main, o.Branch, branchmeta.Chain) // absent is fine

	for _, n := range textx.AppendNote(o.Notes, note) {
		fmt.Fprintf(in.Out, "  %s\n", n)
	}

	pos, err := in.Position(ctx, o, skills)
	if err != nil {
		return err
	}

	r.O, r.Pos, r.Flow, r.Finishes = o, pos, fl, finishes
	in.View().JustOpened(r)
	in.Enter(r, atRunning)
	if o.Fresh {
		r.freshAt = in.Now().Truncate(time.Second) // the state file keeps whole seconds
	}

	return nil
}

// KeepFlow prefers the flow a branch already records over the run's, since
// switching mid-run would rerun or skip entries ship already declared; one
// project.md no longer declares gives way to the run's, with a note.
func (in *DriveInput) KeepFlow(ctx context.Context, branch string) (runplan.ShipFlow, string, error) {
	recorded, err := branchmeta.Get(ctx, in.Main, branch, branchmeta.Flow)
	if err != nil {
		return runplan.ShipFlow{}, "", fmt.Errorf("reading the recorded flow: %w", err)
	}

	if recorded == in.Flow.Recorded() {
		return in.Flow, "", nil
	}

	// an area picks the flow in ship, so a branch with none stays without
	if recorded == "" && in.Flow.ByArea {
		return in.Flow, "", nil
	}

	// a recorded name is looked up as written: `default` is the default
	// flow, never the area's pick
	if recorded != "" && in.Resolve != nil {
		if fl, err := in.Resolve(recorded); err == nil {
			return fl, fmt.Sprintf("kept the %s flow its branch records; the run's %s does not apply to it",
				recorded, in.Flow.Label()), nil
		}
	}

	if !in.Flow.ByArea {
		if err := branchmeta.Set(ctx, in.Main, branch, branchmeta.Flow, in.Flow.Recorded()); err != nil {
			return runplan.ShipFlow{}, "", fmt.Errorf("recording the flow: %w", err)
		}
	}

	if recorded != "" {
		return in.Flow, fmt.Sprintf("its branch recorded the %s flow, which project.md no longer declares; running %s",
			recorded, in.Flow.Label()), nil
	}

	return in.Flow, "", nil
}

// KeepPace: a run's --pace wins and is recorded on the branch, since a
// rerun that slows down must be able to take finish off; with none typed,
// the branch's record wins over the project's default.
func (in *DriveInput) KeepPace(
	ctx context.Context,
	branch string,
	fl runplan.ShipFlow,
) (runplan.ShipFlow, string, error) {
	recorded, err := runplan.RecordedPace(ctx, in.Main, branch)
	if err != nil {
		return runplan.ShipFlow{}, "", err
	}

	switch {
	case in.Args.Pace != "":
		if recorded != in.Args.Pace {
			if err := branchmeta.Set(ctx, in.Main, branch, branchmeta.Pace, in.Args.Pace); err != nil {
				return runplan.ShipFlow{}, "", fmt.Errorf("recording the pace: %w", err)
			}
		}

		return fl, "", nil
	case recorded == "" || recorded == fl.Pace || in.Paced == nil:
		return fl, "", nil
	}

	kept, err := in.Paced(fl, recorded)
	if err != nil {
		return fl, fmt.Sprintf("its branch records the %s pace, which does not resolve (%v); running %s",
			recorded, err, cmp.Or(fl.Pace, "no pace")), nil
	}

	return kept, fmt.Sprintf("kept the %s pace its branch records over %s", recorded, cmp.Or(fl.Pace, "no pace")), nil
}

// Position: a skill drive recorded on the branch wins; else a plan the
// worktree reads counts as plan done, so driven ship takes it rather than
// planning again.
func (in *DriveInput) Position(ctx context.Context, o start.OpenedTask, skills []string) (int, error) {
	last, err := branchmeta.Get(ctx, in.Main, o.Branch, branchmeta.Drive)
	if err != nil {
		return 0, fmt.Errorf("reading where the drive left %s: %w", o.Branch, err)
	}

	if last != "" {
		if i := slices.Index(skills, last); i >= 0 {
			return i + 1, nil
		}
	}

	pos := 0

	if i := slices.Index(skills, "plan"); i >= 0 && o.Plan != "" {
		pos = i + 1
	}

	run, err := in.run(o.Path)
	if err != nil || run == nil {
		return pos, err
	}

	for _, phase := range []string{"plan", "ship"} {
		if s := run.Status(phase); s != "done" && s != "prior" {
			continue
		}

		if i := slices.Index(skills, phase); i >= 0 {
			pos = max(pos, i+1)
		}
	}

	return pos, nil
}

// Next sends the task its next element. A branch marked landed or moved is
// caught up before any skill, finish included; finish waits for the base,
// and a branch that conflicts is marked landed and caught up first.
func (in *DriveInput) Next(ctx context.Context, r *DriveRun) error {
	wasWaiting, held := r.Phase == atWaiting, r.HoldsSlot()

	spike, err := in.SpikeDone(ctx, r)
	if err != nil {
		return halt(r.Task.Ref.ID, "finish", err.Error())
	}

	if spike && !r.Finishes {
		in.closeAfterShip(ctx, r)
	}

	skills := in.Skills(r)
	if r.Pos >= len(skills) {
		return endWith(driveResult{Row: "done: " + strings.Join(skills, " ")})
	}

	skill := skills[r.Pos]

	if b := r.Base; b != nil && b.End != nil && b.End.Code == ui.ExitHalted {
		return endWith(baseHalted(r, skill))
	}

	// a drive resumed onto a ship stopped before pr waits for origin, not code
	if skill == "ship" && r.Base != nil && r.Phase != atParked {
		if run, _ := in.run(r.O.Path); originWait(run) {
			r.ParkedOrigin = true
		}
	}

	if skill != "finish" && r.Base != nil && !in.BaseReady(ctx, r) {
		if r.Phase != atParked {
			fmt.Fprintf(in.Out, "%s %s: waiting on %s for %s\n",
				in.id(r.Task.Ref.ID), skill, in.id(r.Base.Task.Ref.ID), r.waitsFor())
		}

		in.Enter(r, atParked)

		return nil
	}

	if r.Phase == atParked {
		in.Enter(r, atRunning)
	}

	r.parkedHead, r.ParkedOrigin = "", false

	if skill == "finish" && r.Base != nil && !r.Base.through() {
		if !wasWaiting {
			fmt.Fprintf(
				in.Out,
				"%s finish: waiting for %s to finish\n",
				in.id(r.Task.Ref.ID),
				in.id(r.Base.Task.Ref.ID),
			)
			in.finishStep(ctx, r.O.Branch, branchmeta.FinishQueued, "waiting for "+r.Base.Task.Ref.ID)
		}

		in.Enter(r, atWaiting)

		return nil
	}

	// a finish still waiting stays waiting above, so the record never reads
	// running between two polls
	if wasWaiting {
		in.Enter(r, atRunning)
	}

	// finish runs here and sends no skill; anything else held back by the
	// cap retries each poll
	if skill != "finish" && !held && !in.SlotFree(r) {
		in.Queue(r)

		return nil
	}

	in.Unqueue(r)

	// a spike merges nothing, so it needs no catchup and no clean merge
	if skill == "finish" && spike {
		return endWith(in.RunFinish(ctx, r))
	}

	// finish tries the merge itself and catches up only a conflict, under
	// the progress rule, so a clean PR merges as it stands
	if skill != "finish" {
		if err := in.MarkBehind(ctx, r); err != nil {
			return halt(r.Task.Ref.ID, skill, err.Error())
		}
	}

	key, err := in.mark(ctx, r)
	if err == nil && skill == "finish" && key == "" {
		if err := in.readyToMerge(ctx, r); err != nil {
			return err
		}

		key, err = in.mark(ctx, r)
	}

	if err != nil {
		return halt(r.Task.Ref.ID, skill, err.Error())
	}

	switch {
	case key != "":
		skill = "catchup"

		if err := in.recordCatchup(ctx, r, key); err != nil {
			return halt(r.Task.Ref.ID, skill, err.Error())
		}
	case skill == "finish":
		return endWith(in.RunFinish(ctx, r))
	}

	return in.send(ctx, r, skill)
}

// SpikeDone: a spike report on a branch never pushed closes without a merge;
// a pushed one finishes through its PR.
func (in *DriveInput) SpikeDone(ctx context.Context, r *DriveRun) (bool, error) {
	i := slices.Index(in.Skills(r), "ship")
	if i < 0 || r.Pos <= i || finish.TaskSpike(r.O.Path, in.Storage, r.Task.Ref.ID) == "" {
		return false, nil
	}

	pushed, err := gitx.Ref(ctx, in.Main, "refs/remotes/origin/"+r.O.Branch)
	if err != nil {
		return false, fmt.Errorf("reading whether %s was pushed: %w", r.O.Branch, err)
	}

	return pushed == "", nil
}

// closeAfterShip has drive finish a spike its pace does not finish: a spike
// close merges nothing, which the pace's finish gates, and a spike left open
// keeps its dependents on an empty base. status reads the finish record.
func (in *DriveInput) closeAfterShip(ctx context.Context, r *DriveRun) {
	r.Finishes = true
	in.finishStep(ctx, r.O.Branch, branchmeta.FinishQueued, "")
}

// BaseReady reports the base's code there for a skill: released, and
// for a ship that parked itself, moved past the head it found empty. A ship
// parked before pr needs the base's branch on origin instead, or the base done.
func (in *DriveInput) BaseReady(ctx context.Context, r *DriveRun) bool {
	if r.ParkedOrigin {
		return r.Base.through() || in.onOrigin(ctx, r.Base)
	}

	if !in.Released(ctx, r.Base) {
		return false
	}

	if r.parkedHead == "" || r.Base.through() {
		return true
	}

	head, err := gitx.Ref(ctx, in.Main, "refs/heads/"+r.Base.O.Branch)

	return err == nil && head != "" && head != r.parkedHead
}

// park holds a ship that stopped on a base without code until the base
// commits, or ends it when nothing in this drive ever will. A base that
// committed between the ship's look and this read gets ship sent at once.
func (in *DriveInput) park(ctx context.Context, r *DriveRun, run *state.Run) error {
	b := r.Base
	if b != nil && originWait(run) {
		return in.ParkForOrigin(ctx, r)
	}

	if b == nil || b.through() {
		return endWith(in.verdict(r.Task, r.Skill, run))
	}

	head, why := in.baseHead(ctx, b)
	if why != "" {
		return halt(r.Task.Ref.ID, r.Skill, why)
	}

	if head != r.WaitHead && in.Released(ctx, b) {
		r.WaitHead = head

		return in.Next(ctx, r)
	}

	r.parkedHead, r.WaitHead = head, head
	in.Enter(r, atParked)

	fmt.Fprintf(in.Out, "%s ship: waiting on %s for code\n", in.id(r.Task.Ref.ID), in.id(b.Task.Ref.ID))

	return nil
}

// ParkForOrigin holds a ship that stopped before pr on a base origin lacks
// until the base is pushed or done. A base already there gets ship sent at
// once; the same origin sha stopping it twice ends it, so the two never loop.
func (in *DriveInput) ParkForOrigin(ctx context.Context, r *DriveRun) error {
	b := r.Base
	r.ParkedOrigin = true

	// a base done in this drive is resent to once: a finish deleted its branch
	// and pr then targets the default branch, while a base that ended unpushed
	// stops pr the same way again, and nothing here will push it
	if b.through() {
		if r.WaitHead == "through" {
			return halt(r.Task.Ref.ID, r.Skill,
				fmt.Sprintf("%s ended in this drive, yet its pr step still found %s not on origin",
					in.id(b.Task.Ref.ID), b.O.Branch))
		}

		r.WaitHead = "through"

		return in.Next(ctx, r)
	}

	sha, why := in.originHead(ctx, b)
	if why != "" {
		return halt(r.Task.Ref.ID, r.Skill, why)
	}

	if sha != "" {
		if r.WaitHead == "origin "+sha {
			why = fmt.Sprintf("its pr step found %s not on origin though origin/%s is at %s",
				b.O.Branch, b.O.Branch, sha)
			return halt(r.Task.Ref.ID, r.Skill, why)
		}

		r.WaitHead = "origin " + sha

		return in.Next(ctx, r)
	}

	r.parkedHead = ""
	in.Enter(r, atParked)

	fmt.Fprintf(in.Out, "%s ship: waiting on %s for origin\n", in.id(r.Task.Ref.ID), in.id(b.Task.Ref.ID))

	return nil
}

// onOrigin reports b's branch on origin, read as f10 pr base --origin reads
// it: the base's own push from its worktree updates this shared ref.
func (in *DriveInput) onOrigin(ctx context.Context, b *DriveRun) bool {
	sha, why := in.originHead(ctx, b)

	return why == "" && sha != ""
}

// originHead is origin's copy of b's branch, "" when origin lacks it, and why
// when git cannot say.
func (in *DriveInput) originHead(ctx context.Context, b *DriveRun) (string, string) {
	sha, err := gitx.Ref(ctx, in.Main, "refs/remotes/origin/"+b.O.Branch)
	if err != nil {
		return "", "reading origin/" + b.O.Branch + ": " + err.Error()
	}

	return sha, ""
}

// originWait is a ship stopped before pr on a base origin lacks: a wait whose
// leaf is a pr entry, where the implement guard's wait is on the base's code.
func originWait(run *state.Run) bool {
	return run != nil && run.Wait != "" && state.Stopped(run.Ship) && flow.Parse(run.Leaf).Step == "pr"
}

// waitsFor names what a parked task waits on its base for.
func (r *DriveRun) waitsFor() string {
	if r.ParkedOrigin {
		return "origin"
	}

	return "code"
}

// baseHalted ends a dependent whose base halted after releasing it, at the
// boundary before skill: its work stays on its branch.
func baseHalted(r *DriveRun, skill string) driveResult {
	id, base := r.Task.Ref.ID, r.Base.Task.Ref.ID
	reason := fmt.Sprintf("base %s halted, its work stays on %s", base, r.O.Branch)

	return driveResult{
		Row:    "halted: base " + base + " halted",
		Code:   ui.ExitHalted,
		Lines:  []string{fmt.Sprintf("%s halted before %s: %s", id, skill, reason)},
		Skill:  skill,
		Reason: reason,
	}
}

// skillPrompt: plan is bare and ship names only a non-default flow, since
// driven mode takes every gap on its default and reuses a saved plan; every
// skill but plan and ship takes a bundle's lead, which finds the branch.
func skillPrompt(skill, id string, dep *launch.Dependency, fl runplan.ShipFlow, finishing bool) string {
	var p string

	if skill != "plan" && skill != "ship" {
		id, _, _ = strings.Cut(id, ref.Separator)
	}

	switch skill {
	case "ship":
		p = "/f10:ship " + fl.Word() + id
	case "catchup":
		p = "/f10:catchup" // it catches up the branch its session stands on, and takes no id
	default:
		p = "/f10:" + skill + " " + id
	}

	if dep != nil && (skill == "plan" || skill == "ship") {
		p += " && " + launch.DependencyClause(id, dep)
	}

	if finishing && skill == "ship" {
		p += " && " + finishClause
	}

	return launch.Driven(p)
}

// finishClause tells a driven ship that drive finishes the task after it.
const finishClause = "drive finishes it after ship and removes this worktree"

// onProfile is the open line's tail naming the profile a fresh agent
// started on, "" for the kind's default or an agent left as it was.
func onProfile(o start.OpenedTask) string {
	if !o.Fresh || o.Profile.IsDefault() {
		return ""
	}

	return " on " + o.Profile.Short()
}
