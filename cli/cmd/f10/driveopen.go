package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/ui"
)

func (in *driveInput) open(ctx context.Context, w io.Writer, r *driveRun) error {
	task := r.task

	after, note, err := bodyAfter(ctx, in.tracker, in.main, task.ref, task.info.body)
	if err != nil {
		return err
	}

	var title func(context.Context) (string, error)
	if in.title != nil {
		title = in.title(task.ref)
	}

	o, err := openTask(ctx, startInput{
		main:           in.main,
		driver:         in.driver,
		task:           task.ref,
		suffix:         task.suffix,
		after:          after,
		storage:        in.storage,
		title:          title,
		profile:        task.profile,
		profileFlagged: task.flagged,
		progress:       w,
		flowKept:       true,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "%s: branch %s at %s, workspace %s%s\n", in.id(task.ref.ID), o.branch, o.path, o.ws.ID, onProfile(o))

	if o.fresh {
		o.notes = append(o.notes, markStarted(ctx, in.started, o.task)...)
	}

	fl, flowNote, err := in.keepFlow(ctx, o.branch)
	if err != nil {
		return err
	}

	if flowNote != "" {
		o.notes = append(o.notes, flowNote)
	}

	fl, paceNote, err := in.keepPace(ctx, o.branch, fl)
	if err != nil {
		return err
	}

	if paceNote != "" {
		o.notes = append(o.notes, paceNote)
	}

	// status reads the finish record as drive finishing the branch; a rerun
	// at a pace without finish takes it off
	skills := in.sequenceOf(fl)

	finishes := slices.Contains(skills, "finish")
	if finishes {
		in.finishStep(ctx, w, o.branch, branchmeta.FinishQueued, "")
	} else {
		_ = branchmeta.Unset(ctx, in.main, o.branch, branchmeta.Finish) // absent is fine
	}

	// drives before the skills were read off the flow stored them here
	_ = branchmeta.Unset(ctx, in.main, o.branch, branchmeta.Chain) // absent is fine

	for _, n := range appendNote(o.notes, note) {
		fmt.Fprintf(w, "  %s\n", n)
	}

	pos, err := in.position(ctx, o, skills)
	if err != nil {
		return err
	}

	r.o, r.opened, r.pos, r.flow, r.finishes = o, true, pos, fl, finishes
	if o.fresh {
		r.freshAt = in.now().Truncate(time.Second) // the state file keeps whole seconds
	}

	return nil
}

// keepFlow prefers the flow a branch already records over the run's, since
// switching mid-run would rerun or skip entries ship already declared; one
// project.md no longer declares gives way to the run's, with a note.
func (in *driveInput) keepFlow(ctx context.Context, branch string) (shipFlow, string, error) {
	recorded, err := branchmeta.Get(ctx, in.main, branch, branchmeta.Flow)
	if err != nil {
		return shipFlow{}, "", fmt.Errorf("reading the recorded flow: %w", err)
	}

	if recorded == in.flow.recorded() {
		return in.flow, "", nil
	}

	// an area picks the flow in ship, so a branch with none stays without
	if recorded == "" && in.flow.byArea {
		return in.flow, "", nil
	}

	// a recorded name is looked up as written: `default` is the default
	// flow, never the area's pick
	if recorded != "" && in.resolve != nil {
		if fl, err := in.resolve(recorded); err == nil {
			return fl, fmt.Sprintf("kept the %s flow its branch records; the run's %s does not apply to it",
				recorded, in.flow.label()), nil
		}
	}

	if !in.flow.byArea {
		if err := branchmeta.Set(ctx, in.main, branch, branchmeta.Flow, in.flow.recorded()); err != nil {
			return shipFlow{}, "", fmt.Errorf("recording the flow: %w", err)
		}
	}

	if recorded != "" {
		return in.flow, fmt.Sprintf("its branch recorded the %s flow, which project.md no longer declares; running %s",
			recorded, in.flow.label()), nil
	}

	return in.flow, "", nil
}

// keepPace: a run's --pace wins and is recorded on the branch, since a
// rerun that slows down must be able to take finish off; with none typed,
// the branch's record wins over the project's default.
func (in *driveInput) keepPace(ctx context.Context, branch string, fl shipFlow) (shipFlow, string, error) {
	recorded, err := recordedPace(ctx, in.main, branch)
	if err != nil {
		return shipFlow{}, "", err
	}

	switch {
	case in.args.pace != "":
		if recorded != in.args.pace {
			if err := branchmeta.Set(ctx, in.main, branch, branchmeta.Pace, in.args.pace); err != nil {
				return shipFlow{}, "", fmt.Errorf("recording the pace: %w", err)
			}
		}

		return fl, "", nil
	case recorded == "" || recorded == fl.pace || in.paced == nil:
		return fl, "", nil
	}

	kept, err := in.paced(fl, recorded)
	if err != nil {
		return fl, fmt.Sprintf("its branch records the %s pace, which does not resolve (%v); running %s",
			recorded, err, cmp.Or(fl.pace, "no pace")), nil
	}

	return kept, fmt.Sprintf("kept the %s pace its branch records over %s", recorded, cmp.Or(fl.pace, "no pace")), nil
}

// position: a skill drive recorded on the branch wins; else a plan the
// worktree reads counts as plan done, so driven ship takes it rather than
// planning again.
func (in *driveInput) position(ctx context.Context, o openedTask, skills []string) (int, error) {
	last, err := branchmeta.Get(ctx, in.main, o.branch, branchmeta.Drive)
	if err != nil {
		return 0, fmt.Errorf("reading where the drive left %s: %w", o.branch, err)
	}

	if last != "" {
		if i := slices.Index(skills, last); i >= 0 {
			return i + 1, nil
		}
	}

	pos := 0

	if i := slices.Index(skills, "plan"); i >= 0 && o.plan != "" {
		pos = i + 1
	}

	run, err := in.run(o.path)
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

// next sends the task its next element. A branch marked landed or moved is
// caught up before any skill, finish included; finish waits for the base,
// and a branch that conflicts is marked landed and caught up first.
func (in *driveInput) next(ctx context.Context, w io.Writer, r *driveRun) error {
	wasWaiting, held := r.waiting, r.holdsSlot()
	r.waiting = false

	spike, err := in.spikeDone(ctx, r)
	if err != nil {
		return halt(r.task.ref.ID, "finish", err.Error())
	}

	if spike && !r.finishes {
		in.closeAfterShip(ctx, w, r)
	}

	skills := in.skills(r)
	if r.pos >= len(skills) {
		return endWith(driveResult{row: "done: " + strings.Join(skills, " ")})
	}

	skill := skills[r.pos]

	if b := r.base; b != nil && b.end != nil && b.end.code == ui.ExitHalted {
		return endWith(baseHalted(r, skill))
	}

	// a drive resumed onto a ship stopped before pr waits for origin, not code
	if skill == "ship" && r.base != nil && !r.parked {
		if run, _ := in.run(r.o.path); originWait(run) {
			r.parkedOrigin = true
		}
	}

	if skill != "finish" && r.base != nil && !in.baseReady(ctx, r) {
		if !r.parked {
			fmt.Fprintf(w, "%s %s: waiting on %s for %s\n",
				in.id(r.task.ref.ID), skill, in.id(r.base.task.ref.ID), r.waitsFor())
		}

		r.parked = true

		return nil
	}

	r.parked, r.parkedHead, r.parkedOrigin = false, "", false

	if skill == "finish" && r.base != nil && !r.base.through() {
		if !wasWaiting {
			fmt.Fprintf(w, "%s finish: waiting for %s to finish\n", in.id(r.task.ref.ID), in.id(r.base.task.ref.ID))
			in.finishStep(ctx, w, r.o.branch, branchmeta.FinishQueued, "waiting for "+r.base.task.ref.ID)
		}

		r.waiting = true

		return nil
	}

	// finish runs here and sends no skill; anything else held back by the
	// cap retries each poll
	if skill != "finish" && !held && !in.slotFree(r) {
		in.queue(w, r)

		return nil
	}

	in.unqueue(r)

	// a spike merges nothing, so it needs no catchup and no clean merge
	if skill == "finish" && spike {
		return endWith(in.runFinish(ctx, w, r))
	}

	// finish tries the merge itself and catches up only a conflict, under
	// the progress rule, so a clean PR merges as it stands
	if skill != "finish" {
		if err := in.markBehind(ctx, w, r); err != nil {
			return halt(r.task.ref.ID, skill, err.Error())
		}
	}

	key, err := in.mark(ctx, r)
	if err == nil && skill == "finish" && key == "" {
		if err := in.readyToMerge(ctx, w, r); err != nil {
			return err
		}

		key, err = in.mark(ctx, r)
	}

	if err != nil {
		return halt(r.task.ref.ID, skill, err.Error())
	}

	switch {
	case key != "":
		skill = "catchup"

		if err := in.recordCatchup(ctx, r, key); err != nil {
			return halt(r.task.ref.ID, skill, err.Error())
		}
	case skill == "finish":
		return endWith(in.runFinish(ctx, w, r))
	}

	return in.send(ctx, w, r, skill)
}

// spikeDone: a spike report on a branch never pushed closes without a merge;
// a pushed one finishes through its PR.
func (in *driveInput) spikeDone(ctx context.Context, r *driveRun) (bool, error) {
	i := slices.Index(in.skills(r), "ship")
	if i < 0 || r.pos <= i || taskSpike(r.o.path, in.storage, r.task.ref.ID) == "" {
		return false, nil
	}

	pushed, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+r.o.branch)
	if err != nil {
		return false, fmt.Errorf("reading whether %s was pushed: %w", r.o.branch, err)
	}

	return pushed == "", nil
}

// closeAfterShip has drive finish a spike its pace does not finish: a spike
// close merges nothing, which the pace's finish gates, and a spike left open
// keeps its dependents on an empty base. status reads the finish record.
func (in *driveInput) closeAfterShip(ctx context.Context, w io.Writer, r *driveRun) {
	r.finishes = true
	in.finishStep(ctx, w, r.o.branch, branchmeta.FinishQueued, "")
}

// baseReady reports the base's code there for a skill: released, and
// for a ship that parked itself, moved past the head it found empty. A ship
// parked before pr needs the base's branch on origin instead, or the base done.
func (in *driveInput) baseReady(ctx context.Context, r *driveRun) bool {
	if r.parkedOrigin {
		return r.base.through() || in.onOrigin(ctx, r.base)
	}

	if !in.released(ctx, r.base) {
		return false
	}

	if r.parkedHead == "" || r.base.through() {
		return true
	}

	head, err := gitx.Ref(ctx, in.main, "refs/heads/"+r.base.o.branch)

	return err == nil && head != "" && head != r.parkedHead
}

// park holds a ship that stopped on a base without code until the base
// commits, or ends it when nothing in this drive ever will. A base that
// committed between the ship's look and this read gets ship sent at once.
func (in *driveInput) park(ctx context.Context, w io.Writer, r *driveRun, run *state.Run) error {
	b := r.base
	if b != nil && originWait(run) {
		return in.parkForOrigin(ctx, w, r)
	}

	if b == nil || b.through() {
		return endWith(in.verdict(r.task, r.skill, run))
	}

	head, why := in.baseHead(ctx, b)
	if why != "" {
		return halt(r.task.ref.ID, r.skill, why)
	}

	if head != r.waitHead && in.released(ctx, b) {
		r.waitHead = head

		return in.next(ctx, w, r)
	}

	r.parked, r.parkedHead, r.waitHead = true, head, head

	fmt.Fprintf(w, "%s ship: waiting on %s for code\n", in.id(r.task.ref.ID), in.id(b.task.ref.ID))

	return nil
}

// parkForOrigin holds a ship that stopped before pr on a base origin lacks
// until the base is pushed or done. A base already there gets ship sent at
// once; the same origin sha stopping it twice ends it, so the two never loop.
func (in *driveInput) parkForOrigin(ctx context.Context, w io.Writer, r *driveRun) error {
	b := r.base
	r.parkedOrigin = true

	// a base done in this drive is resent to once: a finish deleted its branch
	// and pr then targets the default branch, while a base that ended unpushed
	// stops pr the same way again, and nothing here will push it
	if b.through() {
		if r.waitHead == "through" {
			in.end(ctx, w, r, halted(r.task.ref.ID, r.skill,
				fmt.Sprintf("%s ended in this drive, yet its pr step still found %s not on origin",
					in.id(b.task.ref.ID), b.o.branch)))

			return nil
		}

		r.waitHead = "through"

		return in.next(ctx, w, r)
	}

	sha, why := in.originHead(ctx, b)
	if why != "" {
		in.end(ctx, w, r, halted(r.task.ref.ID, r.skill, why))

		return nil
	}

	if sha != "" {
		if r.waitHead == "origin "+sha {
			why = fmt.Sprintf("its pr step found %s not on origin though origin/%s is at %s",
				b.o.branch, b.o.branch, sha)
			in.end(ctx, w, r, halted(r.task.ref.ID, r.skill, why))

			return nil
		}

		r.waitHead = "origin " + sha

		return in.next(ctx, w, r)
	}

	r.parked, r.parkedHead = true, ""

	fmt.Fprintf(w, "%s ship: waiting on %s for origin\n", in.id(r.task.ref.ID), in.id(b.task.ref.ID))

	return nil
}

// onOrigin reports b's branch on origin, read as f10 pr base --origin reads
// it: the base's own push from its worktree updates this shared ref.
func (in *driveInput) onOrigin(ctx context.Context, b *driveRun) bool {
	sha, why := in.originHead(ctx, b)

	return why == "" && sha != ""
}

// originHead is origin's copy of b's branch, "" when origin lacks it, and why
// when git cannot say.
func (in *driveInput) originHead(ctx context.Context, b *driveRun) (string, string) {
	sha, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+b.o.branch)
	if err != nil {
		return "", "reading origin/" + b.o.branch + ": " + err.Error()
	}

	return sha, ""
}

// originWait is a ship stopped before pr on a base origin lacks: a wait whose
// leaf is a pr entry, where the implement guard's wait is on the base's code.
func originWait(run *state.Run) bool {
	return run != nil && run.Wait != "" && state.Stopped(run.Ship) && flow.Parse(run.Leaf).Step == "pr"
}

// waitsFor names what a parked task waits on its base for.
func (r *driveRun) waitsFor() string {
	if r.parkedOrigin {
		return "origin"
	}

	return "code"
}

// baseHalted ends a dependent whose base halted after releasing it, at the
// boundary before skill: its work stays on its branch.
func baseHalted(r *driveRun, skill string) driveResult {
	id, base := r.task.ref.ID, r.base.task.ref.ID
	reason := fmt.Sprintf("base %s halted, its work stays on %s", base, r.o.branch)

	return driveResult{
		row:    "halted: base " + base + " halted",
		code:   ui.ExitHalted,
		lines:  []string{fmt.Sprintf("%s halted before %s: %s", id, skill, reason)},
		skill:  skill,
		reason: reason,
	}
}

// skillPrompt: plan is bare and ship names only a non-default flow, since
// driven mode takes every gap on its default and reuses a saved plan; every
// skill but plan and ship takes a bundle's lead, which finds the branch.
func skillPrompt(skill, id string, dep *dependency, fl shipFlow, finish bool) string {
	var p string

	if skill != "plan" && skill != "ship" {
		id, _, _ = strings.Cut(id, ref.Separator)
	}

	switch skill {
	case "ship":
		p = "/f10:ship " + fl.word() + id
	case "catchup":
		p = "/f10:catchup" // it catches up the branch its session stands on, and takes no id
	default:
		p = "/f10:" + skill + " " + id
	}

	if dep != nil && (skill == "plan" || skill == "ship") {
		p += " && " + dependencyClause(id, dep)
	}

	if finish && skill == "ship" {
		p += " && " + finishClause
	}

	return driven(p)
}

// finishClause tells a driven ship that drive finishes the task after it.
const finishClause = "drive finishes it after ship and removes this worktree"

// onProfile is the open line's tail naming the profile a fresh agent
// started on, "" for the kind's default or an agent left as it was.
func onProfile(o openedTask) string {
	if !o.fresh || o.profile.IsDefault() {
		return ""
	}

	return " on " + o.profile.Short()
}
