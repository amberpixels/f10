package main

import (
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// outcomes is what the reviews and judges of a task left, with the time
// of the newest of each, so a later call reads as running.
type outcomes struct {
	local, remote                     []reviewRound
	taskJudge, planJudge, changeJudge *archive.JudgeOutcome

	localAt, remoteAt, taskAt, planAt, changeAt time.Time

	// every finding the files raised, each counted in its first round only
	localRaised, remoteRaised int
}

// readOutcomes reads lore first and the findings and verdict files for
// whatever lore holds nothing of: runs from before the steps wrote lore.
func readOutcomes(store *lore.Store, storageRoot, task string) outcomes {
	return seriesOutcomes(store, storageRoot, task, true)
}

// seriesOutcomes reads the open series of a task with a branch, so a
// follow-up PR never shows the first one's gates; a task with no branch
// left reads the newest series lore holds, its open one being empty.
func seriesOutcomes(store *lore.Store, storageRoot, task string, open bool) outcomes {
	var (
		o      outcomes
		change = task
		since  time.Time
	)

	if storageRoot != "" {
		dir := storage.Root(storageRoot).Reviews(task)
		change, since = archive.ChangeOf(dir), archive.SeriesStart(dir)
	}

	if !open {
		since = time.Time{}
	}

	if storageRoot != "" {
		o = fileOutcomes(storageRoot, task, since)
	}

	if store == nil {
		return o
	}

	if !open {
		change = newestSeries(store, task)
	}

	entries, err := store.Effective(change)
	if err != nil {
		return o
	}

	l := loreOutcomes(entries)

	if current(l.local, o.localRaised, l.localAt, o.localAt) {
		o.local = roundsFromLore(l.local, o.local)
	}

	if current(l.remote, o.remoteRaised, l.remoteAt, o.remoteAt) {
		o.remote = roundsFromLore(l.remote, o.remote)
	}

	o.localAt, o.remoteAt = latest(o.localAt, l.localAt), latest(o.remoteAt, l.remoteAt)

	if l.taskJudge != nil && !l.taskAt.Add(time.Second).Before(o.taskAt) {
		o.taskJudge = withFileDecision(l.taskJudge, o.taskJudge)
	}

	if l.planJudge != nil && !l.planAt.Add(time.Second).Before(o.planAt) {
		o.planJudge = withFileDecision(l.planJudge, o.planJudge)
	}

	if l.changeJudge != nil && !l.changeAt.Add(time.Second).Before(o.changeAt) {
		o.changeJudge = withFileDecision(l.changeJudge, o.changeJudge)
	}

	o.taskAt = latest(o.taskAt, l.taskAt)
	o.planAt, o.changeAt = latest(o.planAt, l.planAt), latest(o.changeAt, l.changeAt)

	return o
}

// withFileDecision gives a lore verdict its file's decision, which lore does
// not record; a file with another verdict is older and describes that one.
func withFileDecision(fromLore, file *archive.JudgeOutcome) *archive.JudgeOutcome {
	if file == nil || file.Verdict != fromLore.Verdict {
		return fromLore
	}

	j := *fromLore
	j.Decision, j.Changes = file.Decision, file.Changes

	return &j
}

// newestSeries is the task's newest change that holds entries, else the
// task id.
func newestSeries(store *lore.Store, task string) string {
	changes, err := store.Changes()
	if err != nil {
		return task
	}

	for _, c := range loreSeries(changes, task) {
		if _, err := store.Effective(c); err == nil {
			return c
		}
	}

	return task
}

// current is lore's counts answering for a half: as new as the newest
// findings file (lore keeps whole seconds) and holding every finding the
// files raised, so a lore write that failed leaves the files the answer.
func current(fromLore []reviewRound, raised int, loreAt, fileAt time.Time) bool {
	switch {
	case fromLore == nil:
		return false
	case fileAt.IsZero():
		return true
	}

	return !loreAt.Add(time.Second).Before(fileAt) && fromLore[0].Found >= raised
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

// roundsFromLore is lore's counts as the latest round, numbered after the
// files when they hold rounds: lore keeps findings, not rounds.
func roundsFromLore(counts, files []reviewRound) []reviewRound {
	r := counts[0]
	r.Round = "1"

	if len(files) > 0 {
		r.Round = files[len(files)-1].Round
	}

	return []reviewRound{r}
}

func loreOutcomes(entries []lore.Entry) outcomes {
	var (
		o                      outcomes
		local, remote          *reviewRound
		taskAt, planAt, change time.Time
	)

	for _, e := range entries {
		switch e.Kind {
		case lore.KindFinding:
			r, at := &local, &o.localAt
			if e.Source == "remote" {
				r, at = &remote, &o.remoteAt
			}

			if *r == nil {
				*r = &reviewRound{}
			}

			(*r).Found++

			switch e.Status {
			case "fixed":
				(*r).Fixed++
			case "open":
				(*r).Ask++
			default:
				(*r).Skipped++
			}

			if e.At.After(*at) {
				*at = e.At
			}
		case lore.KindVerdict:
			j := &archive.JudgeOutcome{Verdict: e.Outcome, Reason: firstLine(strings.TrimSpace(e.Body))}

			switch {
			case e.Judged == "task":
				if !e.At.Before(taskAt) {
					o.taskJudge, taskAt = j, e.At
				}
			case e.Judged == "plan":
				if !e.At.Before(planAt) {
					o.planJudge, planAt = j, e.At
				}
			case !e.At.Before(change):
				o.changeJudge, change = j, e.At
			}
		case lore.KindIntent, lore.KindDecision, lore.KindLesson:
		}
	}

	if local != nil {
		o.local = []reviewRound{*local}
	}

	if remote != nil {
		o.remote = []reviewRound{*remote}
	}

	o.taskAt, o.planAt, o.changeAt = taskAt, planAt, change

	return o
}

func firstErrLine(err error) string {
	return firstLine(err.Error())
}
