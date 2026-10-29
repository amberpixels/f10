package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/tasktree"
)

// A drive keeps a record under the storage root, what it was started with,
// the answers given and each task's outcome, and holds a lease per task
// while it acts on it. Rerunning the same command picks up the record; a
// second drive, or `f10 finish`, skips or refuses a task whose lease it
// holds.

// recordArgs is the command line a record is matched on: --answer, --every
// and --resume dropped, since none of them names another drive.
func recordArgs(argv []string) []string {
	var out []string

	for i := 0; i < len(argv); i++ {
		tok := strings.TrimSpace(argv[i])
		name, _, inline := strings.Cut(tok, "=")

		switch {
		case tok == "":
			continue
		case tok == "--foreground":
			continue
		case name == "--answer" || name == "--every" || name == "--resume":
			if !inline {
				i++
			}

			continue
		}

		out = append(out, tok)
	}

	return out
}

// openRecord finds the record this command line resumes, else creates
// one, takes the drive's own lease, and folds the answers it kept under
// the ones given now. A root of "" keeps no record.
func (in *driveInput) openRecord(tasks []driveTask) error {
	if in.root == "" {
		return nil
	}

	// before the kept answers join the ones given now
	in.launched = in.args.resume == 0 && len(in.args.answers) == 0

	var err error

	switch {
	case in.args.resume > 0:
		in.rec, err = drives.Read(in.root, in.args.resume)
		if err == nil && in.rec.Cancelled != nil {
			err = fmt.Errorf("drive %d was cancelled: run the same command without --resume to start a new one",
				in.rec.Number)
		}
	default:
		in.rec, err = drives.Find(in.root, recordArgs(in.argv))
	}

	if err != nil {
		return err
	}

	if in.rec == nil {
		in.rec = &drives.Record{
			Created:     in.now(),
			Args:        recordArgs(in.argv),
			Flow:        in.args.flow,
			Agent:       in.args.agent,
			DriveFinish: slices.Contains(in.chain, "finish"),
			Session:     in.session,
		}

		for i, t := range tasks {
			profile := ""
			if i < len(in.args.profiles) {
				profile = in.args.profiles[i]
			}

			in.rec.Tasks = append(in.rec.Tasks, drives.Task{ID: t.ref.Label() + suffixOf(t.suffix), Agent: profile})
		}

		if err := drives.Create(in.root, in.rec); err != nil {
			return err
		}
	}

	own, err := drives.Acquire(in.root, drives.DriveKey(in.rec.Number), in.rec.Number)
	if held := (drives.HeldError{}); errors.As(err, &held) {
		return fmt.Errorf("drive %d is running (pid %d): f10 drive cancel %d stops it", in.rec.Number,
			held.Holder.PID, in.rec.Number)
	}

	if err != nil {
		return err
	}

	in.own = own

	// an answer kept from a run that stopped before taking it goes first,
	// and one given now for the same task replaces it
	kept := make([]string, 0, len(in.rec.Answers))

	for _, key := range slices.Sorted(maps.Keys(in.rec.Answers)) {
		text := in.rec.Answers[key]
		if key == "" {
			if !slices.ContainsFunc(in.args.answers, func(a string) bool { return !strings.Contains(a, "=") }) {
				kept = append(kept, text)
			}

			continue
		}

		if !slices.ContainsFunc(in.args.answers, func(a string) bool {
			k, _, ok := strings.Cut(a, "=")

			return ok && strings.EqualFold(strings.TrimSpace(k), key)
		}) {
			kept = append(kept, key+"="+text)
		}
	}

	in.args.answers = append(kept, in.args.answers...)

	return nil
}

// recordLaunch adds the drive to the launches of the session that typed
// it, a rerun of the same command from a new session included. A --resume
// (a drained drive, a drive resuming itself) and an --answer relayed from
// another session add nothing.
func (in *driveInput) recordLaunch() error {
	if !in.launched || in.rec == nil {
		return nil
	}

	return state.AppendLaunch(in.stateDir, in.session,
		state.Launch{Kind: state.LaunchDrive, Value: strconv.Itoa(in.rec.Number)})
}

func suffixOf(s string) string {
	if s == "" {
		return ""
	}

	return "-" + s
}

// save applies change to the record. A failed write is a line, never a
// halt: the branch keeps the position, the record what it cannot.
func (in *driveInput) save(change func(*drives.Record)) {
	if in.rec == nil {
		return
	}

	rec, err := drives.Update(in.root, in.rec.Number, change)
	if err != nil {
		fmt.Fprintf(in.out, "  recording drive %d: %v\n", in.rec.Number, err)

		return
	}

	in.rec = rec
}

// saveTask applies change to one task's entry in the record.
func (in *driveInput) saveTask(r *driveRun, change func(*drives.Task)) {
	ids := []string{r.task.ref.Label() + suffixOf(r.task.suffix), r.task.ref.ID + suffixOf(r.task.suffix)}

	in.save(func(rec *drives.Record) {
		for _, id := range ids {
			if t := rec.Task(id); t != nil {
				change(t)

				return
			}
		}
	})
}

// savePending writes the answers not yet taken, and records one taken.
func (in *driveInput) savePending(taken *drives.Answer) {
	in.save(func(rec *drives.Record) {
		rec.Answers = map[string]string{}
		if in.answer != "" {
			rec.Answers[""] = in.answer
		}

		maps.Copy(rec.Answers, in.answers)

		if taken != nil {
			rec.Answered = append(rec.Answered, *taken)
		}
	})
}

// took records an answer taken: by which task, at which skill.
func (in *driveInput) took(id, skill, text string) {
	if in.rec != nil {
		in.savePending(&drives.Answer{Task: id, Skill: skill, Text: text, At: in.now()})
	}
}

// cancelled reports the record cancelled since the drive read it last.
func (in *driveInput) cancelled() bool {
	if in.rec == nil {
		return false
	}

	rec, err := drives.Read(in.root, in.rec.Number)
	if err != nil {
		return false
	}

	in.rec = rec

	return rec.Cancelled != nil
}

// leaseKey is a task's lease: its lead id, so two attempts at one task are
// one owner's.
func leaseKey(task driveTask) string { return task.ref.Head().ID }

// lease takes the task's lease, or shares one this drive already holds
// for another attempt at the same task. Held elsewhere, it says who holds it.
func (in *driveInput) lease(r *driveRun) (string, bool) {
	if in.root == "" {
		return "", true
	}

	key := leaseKey(r.task)
	if _, ok := in.leases[key]; ok {
		return "", true
	}

	l, err := drives.Acquire(in.root, key, in.rec.Number)
	if held := (drives.HeldError{}); errors.As(err, &held) {
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
func (in *driveInput) unlease(r *driveRun, runs []*driveRun) {
	key := leaseKey(r.task)

	l, ok := in.leases[key]
	if !ok {
		return
	}

	if slices.ContainsFunc(runs, func(o *driveRun) bool {
		return o != r && o.end == nil && leaseKey(o.task) == key
	}) {
		return
	}

	l.Release()
	delete(in.leases, key)
}

// releaseAll drops every lease the drive still holds, its own last.
func (in *driveInput) releaseAll() {
	for key, l := range in.leases {
		l.Release()
		delete(in.leases, key)
	}

	in.own.Release()
}

// number is the drive's record number, 0 with no record.
func (in *driveInput) number() int {
	if in.rec == nil {
		return 0
	}

	return in.rec.Number
}

// resumeLine is how the report says to answer the drive.
func (in *driveInput) resumeLine() string {
	if in.rec != nil {
		return "f10 drive --resume " + strconv.Itoa(in.rec.Number)
	}

	return resumeCommand(in.argv)
}

// driveList prints every drive, newest first, and where its tasks stand.
func driveList(w io.Writer, root string, all bool) error {
	recs, err := drives.List(root)
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

	for i, rec := range recs {
		if i > 0 {
			fmt.Fprintln(w)
		}

		holder, running := drives.Probe(root, drives.DriveKey(rec.Number))

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
			nodes[j] = tasktree.Node{Cells: []tasktree.Cell{{Text: t.ID + ":"}, {Text: taskStanding(t, running)}}}
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

// taskStanding is a task's line in the list: its outcome, else what is in
// flight while its drive runs.
func taskStanding(t drives.Task, running bool) string {
	switch {
	case t.Outcome != nil:
		return t.Outcome.Row
	case running && t.Skill != "":
		return "running " + t.Skill
	case t.Skill != "":
		return "stopped at " + t.Skill
	}

	return "not reached"
}

// driveCancel marks drive n cancelled: a live drive stops prompting at its
// next poll; worktrees, branches and agents stay as they are.
func driveCancel(w io.Writer, root string, n int, now time.Time) error {
	rec, err := drives.Update(root, n, func(r *drives.Record) {
		if r.Cancelled == nil {
			r.Cancelled = &now
		}
	})
	if err != nil {
		return err
	}

	if holder, running := drives.Probe(root, drives.DriveKey(n)); running {
		fmt.Fprintf(w, "drive %d cancelled: pid %d stops at its next poll, after any finish it is running\n",
			rec.Number, holder.PID)

		return nil
	}

	fmt.Fprintf(w, "drive %d cancelled: it was not running\n", rec.Number)

	return nil
}
