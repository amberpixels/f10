package drive

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktree"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// A drive's record, under the storage root, is what a rerun of the same
// command picks up; its leases make a second drive, or `f10 finish`, skip or
// refuse a task it holds.

// healFinished settles a stopped drive's open rows whose task finished
// outside it, on screen always and on disk when the lease is free.
func healFinished(root string, rec *drivestore.Record, finished func(string) bool) {
	if finished == nil {
		return
	}

	hit := map[string]bool{}

	for i := range rec.Tasks {
		if t := &rec.Tasks[i]; !drivectl.RowDone(*t) && finished(t.ID) {
			hit[t.ID] = true
			drivectl.SetFinished(t)
		}
	}

	if len(hit) > 0 {
		_ = drivectl.MarkFinished(root, rec.Number, func(t drivestore.Task) bool { return hit[t.ID] })
	}
}

// recordArgs is the command line a record is matched on: the flags that only
// tune a drive dropped, since none of them names another drive.
func recordArgs(argv []string) []string {
	var out []string

	for i := 0; i < len(argv); i++ {
		tok := strings.TrimSpace(argv[i])
		name, _, inline := strings.Cut(tok, "=")

		if tok == "" {
			continue
		}

		if f, ok := drivectl.DriveFlagNamed(name); ok && f.Tunes {
			if f.Value != "" && !inline {
				i++
			}

			continue
		}

		out = append(out, tok)
	}

	return out
}

// OpenRecord folds the record's kept answers under the ones given now; a
// root of "" keeps no record.
func (in *DriveInput) OpenRecord(tasks []DriveTask) error {
	in.Parallel = cmp.Or(in.Args.Parallel, in.DefaultParallel)

	if in.Root == "" {
		return nil
	}

	// before the kept answers join the ones given now
	in.launched = in.Args.Resume == 0 && len(in.Args.Answers) == 0

	var err error

	switch {
	case in.Args.Resume > 0:
		in.Rec, err = drivestore.Read(in.Root, in.Args.Resume)
		if err == nil && in.Rec.Cancelled != nil {
			err = fmt.Errorf("drive %d was cancelled: run the same command without --resume to start a new one",
				in.Rec.Number)
		}
	default:
		in.Rec, err = drivestore.Find(in.Root, recordArgs(in.Argv))
	}

	if err != nil {
		return err
	}

	if in.Rec == nil {
		in.Rec = &drivestore.Record{
			Created:  in.Now(),
			Args:     recordArgs(in.Argv),
			Flow:     in.Args.Flow,
			Agent:    in.Args.Agent,
			Session:  in.Session,
			Parallel: in.Parallel,
		}

		for i, t := range tasks {
			profile := ""
			if i < len(in.Args.Profiles) {
				profile = in.Args.Profiles[i]
			}

			in.Rec.Tasks = append(in.Rec.Tasks, drivestore.Task{
				ID: t.Ref.Label() + worktree.SuffixOf(t.Suffix), Agent: profile, After: t.after,
			})
		}

		if err := drivestore.Create(in.Root, in.Rec); err != nil {
			return err
		}
	}

	own, err := drivestore.Acquire(in.Root, drivestore.DriveKey(in.Rec.Number), in.Rec.Number)
	if held := (drivestore.HeldError{}); errors.As(err, &held) {
		return fmt.Errorf("drive %d is running (pid %d): f10 drive cancel %d stops it", in.Rec.Number,
			held.Holder.PID, in.Rec.Number)
	}

	if err != nil {
		return err
	}

	in.own = own

	// the drive keeps the cap it started with, unless this run passes one
	in.Parallel = cmp.Or(in.Args.Parallel, in.Rec.Parallel, in.DefaultParallel)
	if in.Rec.Parallel != in.Parallel {
		in.save(func(rec *drivestore.Record) { rec.Parallel = in.Parallel })
	}

	// an answer kept from a run that stopped before taking it goes first,
	// and one given now for the same task replaces it
	kept := make([]string, 0, len(in.Rec.Answers))

	for _, key := range slices.Sorted(maps.Keys(in.Rec.Answers)) {
		text := in.Rec.Answers[key]
		if key == "" {
			if !slices.ContainsFunc(in.Args.Answers, func(a string) bool {
				_, _, keyed := keyedAnswer(a, tasks)

				return !keyed
			}) {
				kept = append(kept, text)
			}

			continue
		}

		if !slices.ContainsFunc(in.Args.Answers, func(a string) bool {
			id, _, keyed := keyedAnswer(a, tasks)

			return keyed && strings.EqualFold(id, key)
		}) {
			kept = append(kept, key+"="+text)
		}
	}

	in.Args.Answers = append(kept, in.Args.Answers...)

	return nil
}

// RecordLaunch: a --resume (a drained drive, a drive resuming itself) and an
// --answer relayed from another session are nobody's launch.
func (in *DriveInput) RecordLaunch() error {
	if !in.launched || in.Rec == nil {
		return nil
	}

	return state.AppendLaunch(in.StateDir, in.Session,
		state.Launch{Kind: state.LaunchDrive, Value: strconv.Itoa(in.Rec.Number)})
}

// save applies change to the record. A failed write is a line, never a
// halt: the branch keeps the position, the record what it cannot.
func (in *DriveInput) save(change func(*drivestore.Record)) {
	if in.Rec == nil {
		return
	}

	rec, err := drivestore.Update(in.Root, in.Rec.Number, change)
	if err != nil {
		fmt.Fprintf(in.Out, "  recording drive %d: %v\n", in.Rec.Number, err)

		return
	}

	in.Rec = rec
}

func (in *DriveInput) saveTask(r *DriveRun, change func(*drivestore.Task)) {
	ids := []string{
		r.Task.Ref.Label() + worktree.SuffixOf(r.Task.Suffix),
		r.Task.Ref.ID + worktree.SuffixOf(r.Task.Suffix),
	}

	in.save(func(rec *drivestore.Record) {
		for _, id := range ids {
			if t := rec.Task(id); t != nil {
				change(t)

				return
			}
		}
	})
}

func (in *DriveInput) savePending(taken *drivestore.Answer) {
	in.save(func(rec *drivestore.Record) {
		rec.Answers = in.Answers.Pending()

		if taken != nil {
			rec.Answered = append(rec.Answered, *taken)
		}
	})
}

func (in *DriveInput) took(id, skill, text string) {
	if in.Rec != nil {
		in.savePending(&drivestore.Answer{Task: id, Skill: skill, Text: text, At: in.Now()})
	}
}

// refreshRecord reads the drive's record again, for a cancel or a new cap
// written since; a failed read keeps the one it has.
func (in *DriveInput) refreshRecord() {
	if in.Rec == nil {
		return
	}

	rec, err := drivestore.Read(in.Root, in.Rec.Number)
	if err != nil {
		return
	}

	in.Rec = rec

	// a rerun attached to this live drive writes its new cap here
	if rec.Parallel > 0 {
		in.Parallel = rec.Parallel
	}
}

// leaseKey is a task's lease: its lead id, so two attempts at one task are
// one owner's.
func leaseKey(task DriveTask) string { return task.Ref.Head().ID }

// lease takes the task's lease, or shares one this drive already holds
// for another attempt at the same task. Held elsewhere, it says who holds it.
func (in *DriveInput) lease(r *DriveRun) (string, bool) {
	if in.Root == "" {
		return "", true
	}

	key := leaseKey(r.Task)
	if _, ok := in.leases[key]; ok {
		return "", true
	}

	l, err := drivestore.Acquire(in.Root, key, in.Rec.Number)
	if held := (drivestore.HeldError{}); errors.As(err, &held) {
		return "owned by " + held.Holder.String(), false
	}

	if err != nil {
		return err.Error(), false
	}

	in.leases[key] = l

	return "", true
}

// unlease releases the task's lease once no other run of this drive on the
// same task is still going.
func (in *DriveInput) unlease(r *DriveRun, runs []*DriveRun) {
	key := leaseKey(r.Task)

	l, ok := in.leases[key]
	if !ok {
		return
	}

	if slices.ContainsFunc(runs, func(o *DriveRun) bool {
		return o != r && o.End == nil && leaseKey(o.Task) == key
	}) {
		return
	}

	l.Release()
	delete(in.leases, key)
}

// ReleaseAll drops every lease the drive still holds, its own last.
func (in *DriveInput) ReleaseAll() {
	for key, l := range in.leases {
		l.Release()
		delete(in.leases, key)
	}

	in.own.Release()
}

func (in *DriveInput) number() int {
	if in.Rec == nil {
		return 0
	}

	return in.Rec.Number
}

func (in *DriveInput) resumeLine() string {
	if in.Rec != nil {
		return "f10 drive --resume " + strconv.Itoa(in.Rec.Number)
	}

	return resumeCommand(in.Argv)
}

// DriveList prints the records, newest first. finished reports a row's task
// finished outside its drive; nil checks nothing.
func DriveList(
	ctx context.Context,
	w io.Writer,
	links *render.TaskLinks,
	project, root string,
	all bool,
	finished func(row string) bool,
) error {
	recs, err := drivestore.List(root)
	if err != nil {
		return err
	}

	if len(recs) == 0 {
		fmt.Fprintln(w, "no drives recorded")

		return nil
	}

	const shown = 20

	more := 0
	if !all && len(recs) > shown {
		more, recs = len(recs)-shown, recs[:shown]
	}

	var ids []string
	for _, rec := range recs {
		for _, t := range rec.Tasks {
			ids = append(ids, t.ID)
		}
	}

	links.Load(ctx, project, ids...)

	for i, rec := range recs {
		if i > 0 {
			fmt.Fprintln(w)
		}

		holder, running := drivestore.Probe(root, drivestore.DriveKey(rec.Number))
		if !running {
			healFinished(root, rec, finished)
		}

		status := "stopped"

		switch {
		case rec.Cancelled != nil && running:
			status = "cancelled, stopping (pid " + strconv.Itoa(holder.PID) + ")"
		case rec.Cancelled != nil:
			status = "cancelled"
		case running:
			status = "running (pid " + strconv.Itoa(holder.PID) + ")"
		case rec.Done():
			status = "done"
		}

		fmt.Fprintf(w, "drive %d  %s  %s\n", rec.Number, rec.Created.Local().Format(time.DateTime), status)
		fmt.Fprintf(w, "  f10 drive %s\n", strings.Join(rec.Args, " "))

		nodes := make([]tasktree.Node, len(rec.Tasks))
		for j, t := range rec.Tasks {
			id := links.ID(project, t.ID) + ":"
			nodes[j] = tasktree.Node{Cells: []tasktree.Cell{
				{Text: t.ID + ":", Render: func(string) string { return id }},
				{Text: taskStanding(t, running)},
			}}
		}

		if err := (tasktree.Tree{Indent: "  ", Hang: true, Align: true}).Write(w, nodes); err != nil {
			return err
		}
	}

	if more > 0 {
		fmt.Fprintf(w, "\n%d older drives not shown: --all lists them\n", more)
	}

	return nil
}

// RowFinishedFor checks a record's row, an id as typed, by FinishedTask, as
// drive and status do; a row that names no task, or a git error, reads as
// unfinished, since a wrong yes hides a halt.
func RowFinishedFor(ctx context.Context, t *target.Target) func(string) bool {
	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)
	tr := stack.TrackerFor(t)
	seen := map[string]bool{} // a task several records list is checked once

	return func(row string) bool {
		id, _ := ref.SplitSuffix(row)
		if done, ok := seen[id]; ok {
			return done
		}

		seen[id] = false

		r, err := t.Reference(ctx, id)
		if err != nil {
			return false
		}

		head := r.Head()
		seen[id] = finishedLazily(tr.Storage, head.ID,
			func() (bool, error) { return stack.HasTaskBranch(ctx, main, head.ID) },
			func() hostops.TaskInfo {
				info, _ := tr.Info(ctx, head) // an unread tracker leaves the archive to decide

				return info
			})

		return seen[id]
	}
}

// finishedLazily is FinishedTask with its reads made lazily: the tracker is
// asked only when no branch, live plan or archived plan already decides.
func finishedLazily(root storage.Root, id string, branched func() (bool, error), info func() hostops.TaskInfo) bool {
	if b, err := branched(); err != nil || b {
		return false
	}

	var ti hostops.TaskInfo
	if !root.PlanLive(id) && !root.PlanArchived(id) {
		ti = info()
	}

	return stack.FinishedTask(ti, root, id, false)
}

func taskStanding(t drivestore.Task, running bool) string {
	switch {
	case t.Outcome != nil:
		return t.Outcome.Row
	case running && t.Queued:
		return "queued"
	case running && t.Phase == drivestore.PhaseParked:
		return "waiting on its base"
	case running && t.Phase == drivestore.PhaseWaiting:
		return "waiting to finish after its base"
	case running && t.Skill != "":
		return "running " + t.Skill
	case t.Skill != "":
		return "stopped at " + t.Skill
	}

	return "not reached"
}

// DriveCancel marks drive n cancelled: a live drive stops prompting at its
// next poll; worktrees, branches and agents stay as they are.
func DriveCancel(w io.Writer, root string, n int, now time.Time) error {
	rec, err := drivestore.Update(root, n, func(r *drivestore.Record) {
		if r.Cancelled == nil {
			r.Cancelled = &now
		}
	})
	if err != nil {
		return err
	}

	if holder, running := drivestore.Probe(root, drivestore.DriveKey(n)); running {
		fmt.Fprintf(w, "drive %d cancelled: pid %d stops at its next poll, after any finish it is running\n",
			rec.Number, holder.PID)

		return nil
	}

	fmt.Fprintf(w, "drive %d cancelled: it was not running\n", rec.Number)

	return nil
}
