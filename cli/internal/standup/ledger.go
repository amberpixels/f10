package standup

import (
	"cmp"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/history"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/timing"
)

// LedgerScope is the one value the JSON's ledger note holds: the ledger is
// local, so on a second machine its half of the report is partial.
const LedgerScope = "this machine only"

// Ledger is what the ledger and the drive records add to one checkout's
// report. Events are the project's sessions active in the period (a
// SinceScope read); Group keeps only the person's.
type Ledger struct {
	Events []ledger.Event
	// WorktreeOf is the path f10 start gives a branch's worktree, so rows
	// from a worktree removed since still count
	WorktreeOf func(branch string) string
	Checkouts  []string // the main checkout and its worktrees today
	Drives     []*drivestore.Record
	Until      time.Time // the read time, the period's end
}

// The run statuses this file reads, as state writes them.
const (
	statusDone    = "done"
	statusPrior   = "prior"
	statusFailed  = "failed"
	statusPartial = "partial"
)

// The kinds a blocked task waits on.
const (
	BlockedAsk    = "ask"
	BlockedHalt   = "halt"
	BlockedFailed = "failed"
)

// Blocked is what a task waits on at the end of the period, and since when.
type Blocked struct {
	Kind  string    `json:"kind"`
	Since time.Time `json:"since"`
	Text  string    `json:"text"`
}

// The kinds of activity a step leaves without a commit.
const (
	ActivityPlan   = "plan"
	ActivityDebug  = "debug"
	ActivitySpike  = "spike"
	ActivityReview = "review"
	ActivityJudge  = "judge"
)

// Activity is one step run in the period that left no commit.
type Activity struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

// Drive is one drive active in the period, by its outcome so far.
type Drive struct {
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	Tasks    int    `json:"tasks"`
	Shipped  int    `json:"shipped"`
	NeedsYou int    `json:"needs_you"`
}

// LedgerNote says the ledger half is local, and whether it added anything.
type LedgerNote struct {
	Scope       string `json:"scope"`
	Contributed bool   `json:"contributed"`
}

// owned is the person's rows: from one of their checkouts, from the
// worktree f10 start made for the row's branch (gone once the task
// finished, its rows still naming it), or, for a row no checkout recorded,
// on a task their git names. Another clone's rows never count.
func (l *Ledger) owned(tasks map[string]*Task) []ledger.Event {
	var out []ledger.Event

	for _, e := range l.Events {
		switch {
		case e.Checkout != "" && slices.Contains(l.Checkouts, e.Checkout):
		case e.Checkout != "" && e.Branch != "" && l.WorktreeOf != nil &&
			filepath.Clean(e.Checkout) == filepath.Clean(l.WorktreeOf(e.Branch)):
		case e.Checkout == "" && e.Task != "" && tasks[strings.ToUpper(e.Task)] != nil:
		default:
			continue
		}

		out = append(out, e)
	}

	return out
}

// marksTask reports a kind whose row in the period puts its task in the
// report as work, whether or not the task left a commit.
func marksTask(kind string) bool {
	switch kind {
	case ledger.PlanSaved, ledger.PlanSuperseded, ledger.SpikeSaved, ledger.ReviewRoundOpened,
		ledger.FindingVerdict, ledger.JudgeVerdict, ledger.TaskHalted, ledger.RegressionFound:
		return true
	}

	return strings.HasPrefix(kind, "state_")
}

// addLedger folds the person's rows into the tasks, adding the tasks the
// ledger alone saw in the period. git's tasks come in keyed by upper-cased id.
func addLedger(l *Ledger, tasks map[string]*Task, since time.Time, task func(string) *Task) map[string]bool {
	if l == nil {
		return nil
	}

	events := l.owned(tasks)
	byTask := map[string][]ledger.Event{}

	var order []string

	for _, e := range events {
		if e.Task == "" {
			continue
		}

		key := strings.ToUpper(e.Task)
		if _, seen := byTask[key]; !seen {
			order = append(order, key)
		}

		byTask[key] = append(byTask[key], e)
	}

	minutes := activeMinutes(events, since, l.Until)
	active := map[string]bool{} // the tasks with a row of the person's in the period

	for key, evs := range byTask {
		if slices.ContainsFunc(evs, func(e ledger.Event) bool { return inWindow(e.At, since, l.Until) }) {
			active[key] = true
		}
	}

	for _, key := range order {
		evs := byTask[key]

		t := tasks[key]
		if t == nil {
			if !slices.ContainsFunc(evs, func(e ledger.Event) bool {
				return marksTask(e.Kind) && inWindow(e.At, since, l.Until)
			}) {
				continue
			}

			t = task(evs[0].Task)
			tasks[key] = t
		}

		t.Ledger = true
		t.Blocked = blocked(evs, l.Until)
		t.Activity = activity(evs, since, l.Until)
		t.Stage = stage(evs, l.Until)
		t.Driven = driven(l.Drives, t.ID, since, l.Until, active)
	}

	for key, t := range tasks {
		t.ActiveMinutes = int(math.Round(minutes[key]))
	}

	return active
}

// lastClaim is the task's newest state row before until.
func lastClaim(evs []ledger.Event, until time.Time) (ledger.Event, bool) {
	var last ledger.Event

	found := false

	for _, e := range evs {
		if strings.HasPrefix(e.Kind, "state_") && e.At.Before(until) {
			last, found = e, true
		}
	}

	return last, found
}

// blocked is an open ask, else a halt newer than the run's last word, else a
// run that stopped failed: what the task waits on when the period ends.
func blocked(evs []ledger.Event, until time.Time) *Blocked {
	claim, ok := lastClaim(evs, until)

	var halt *ledger.Event

	for i, e := range evs {
		if e.Kind == ledger.TaskHalted && e.At.Before(until) && (!ok || e.At.After(claim.At)) {
			halt = &evs[i]
		}
	}

	if ok {
		run := ledger.DecodeClaim(claim).Run
		if run.Ask != "" {
			return &Blocked{Kind: BlockedAsk, Since: askedAt(evs, claim), Text: firstLine(run.Ask)}
		}

		if halt == nil {
			if phase, status := current(run); status == statusFailed || status == statusPartial {
				text := cmp.Or(run.Leaf, phase) + " " + status
				if run.Note != "" {
					text += ": " + firstLine(run.Note)
				}

				return &Blocked{Kind: BlockedFailed, Since: claim.At, Text: text}
			}
		}
	}

	if halt != nil {
		return &Blocked{Kind: BlockedHalt, Since: halt.At, Text: firstLine(halt.Skill() + ": " + halt.Reason())}
	}

	return nil
}

// askedAt is when the standing ask was put: the newest state_ask row up to
// the claim, which later claims carry forward unchanged.
func askedAt(evs []ledger.Event, claim ledger.Event) time.Time {
	at := claim.At

	for _, e := range evs {
		if e.Kind == ledger.StateAsk && !e.At.After(claim.At) {
			at = e.At
		}
	}

	return at
}

// current is the run's latest phase and its status: ship over plan over
// capture, the first that started.
func current(r state.Run) (string, string) {
	for _, p := range []struct{ name, status string }{{"ship", r.Ship}, {"plan", r.Plan}, {"capture", r.Capture}} {
		if p.status != "" && p.status != state.Upcoming {
			return p.name, p.status
		}
	}

	return "", ""
}

// activity is the steps the period's rows record, each once a day: a plan
// written five times is one plan saved.
func activity(evs []ledger.Event, since, until time.Time) []Activity {
	out := []Activity{}
	seen := map[string]bool{}
	add := func(at time.Time, kind, text string) {
		key := at.Format(time.DateOnly) + "\x00" + kind + "\x00" + text
		if !seen[key] {
			seen[key] = true

			out = append(out, Activity{At: at, Kind: kind, Text: text})
		}
	}

	type round struct {
		name     string
		at       time.Time
		inPeriod bool
		verdicts map[string]string // finding: newest verdict
	}

	rounds := map[string]*round{}
	series := history.Series{}

	var roundOrder []string

	roundOf := func(name, key string, at time.Time) *round {
		if rounds[key] == nil {
			rounds[key] = &round{name: name, verdicts: map[string]string{}}
			roundOrder = append(roundOrder, key)
		}

		// dated by its first row in the period, so a round opened before it
		// and resolved in it still lands on a day of the report
		r := rounds[key]
		if !r.inPeriod && inWindow(at, since, until) {
			r.at, r.inPeriod = at, true
		}

		return r
	}

	for _, e := range evs {
		switch e.Kind {
		case ledger.ReviewRoundOpened:
			if p, err := ledger.Decode[*ledger.ReviewRoundOpenedPayload](e); err == nil && p.GetRound() != "" {
				roundOf(p.GetRound(), series.Opened(p.GetRound(), p.GetReviewed()), e.At)
			}
		case ledger.FindingVerdict:
			if p, err := ledger.Decode[*ledger.FindingVerdictPayload](e); err == nil {
				word, _, _ := strings.Cut(strings.ToLower(p.GetVerdict()), " ")
				roundOf(p.GetRound(), series.Of(p.GetRound()), e.At).verdicts[p.GetFinding()] = word
			}
		}
	}

	debugAt := time.Time{}
	outcome := ""

	for _, e := range evs {
		if !inWindow(e.At, since, until) {
			continue
		}

		switch e.Kind {
		case ledger.PlanSaved:
			add(e.At, ActivityPlan, "plan saved")
		case ledger.PlanSuperseded:
			add(e.At, ActivityPlan, "plan superseded")
		case ledger.SpikeSaved:
			add(e.At, ActivitySpike, "spike report saved")
		case ledger.JudgeVerdict:
			if p, err := ledger.Decode[*ledger.JudgeVerdictPayload](e); err == nil && p.GetVerdict() != "" {
				add(e.At, ActivityJudge, strings.TrimSpace("judge: "+p.GetVerdict()+" on "+p.GetSubject()))
			}
		case ledger.RegressionFound:
			if p, err := ledger.Decode[*ledger.RegressionFoundPayload](e); err == nil {
				debugAt = cmp.Or(debugAt, e.At)

				switch {
				case p.GetRepro() == "unconfirmed":
					outcome = "not reproduced"
				case p.GetOriginTask() != "":
					outcome = "cause found: regression of " + p.GetOriginTask()
				default:
					outcome = "cause found"
				}
			}
		case ledger.StateSet:
			if run := ledger.DecodeClaim(e).Run; run.Leaf == ActivityDebug && debugAt.IsZero() {
				debugAt = e.At
			}
		}
	}

	if !debugAt.IsZero() {
		add(debugAt, ActivityDebug, "debug: "+cmp.Or(outcome, "run"))
	}

	for _, key := range roundOrder {
		r := rounds[key]
		if !r.inPeriod {
			continue
		}

		var fixed, skipped int

		for _, v := range r.verdicts {
			switch v {
			case "fix":
				fixed++
			case "skip":
				skipped++
			}
		}

		text := "round " + r.name + " opened"
		if fixed+skipped > 0 {
			text = fmt.Sprintf("round %s: %d fixed, %d skipped", r.name, fixed, skipped)
		}

		add(r.at, ActivityReview, text)
	}

	slices.SortStableFunc(out, func(a, b Activity) int { return a.At.Compare(b.At) })

	return out
}

// stage is where the run left the task, in words; "" when the last claim
// has nothing git's state lacks, as after a finished ship.
func stage(evs []ledger.Event, until time.Time) string {
	claim, ok := lastClaim(evs, until)
	if !ok {
		return ""
	}

	run := ledger.DecodeClaim(claim).Run
	phase, status := current(run)

	switch {
	case phase == "plan" && (status == statusDone || status == statusPrior):
		return "planned, not started"
	case phase == "plan":
		return "planning"
	case phase != "ship" || status == statusDone:
		return ""
	case strings.HasPrefix(run.Leaf, "review"):
		return "review round " + strconv.Itoa(max(1, reviewRounds(evs)))
	case run.Leaf == "ci":
		return "waiting on CI"
	case run.Wait != "":
		return "waiting on " + run.Wait
	case run.Leaf != "":
		return "shipping: " + run.Leaf
	default:
		return ""
	}
}

func reviewRounds(evs []ledger.Event) int {
	rounds := map[string]bool{}
	series := history.Series{}

	for _, e := range evs {
		if e.Kind == ledger.ReviewRoundOpened {
			if p, err := ledger.Decode[*ledger.ReviewRoundOpenedPayload](e); err == nil && p.GetRound() != "" {
				rounds[series.Opened(p.GetRound(), p.GetReviewed())] = true
			}
		}
	}

	return len(rounds)
}

// driven is the newest drive that lists the task and ran in the period.
func driven(drives []*drivestore.Record, id string, since, until time.Time, active map[string]bool) int {
	n := 0

	for _, r := range drives {
		if r.Task(id) != nil && r.Number > n && ranIn(r, since, until, active) {
			n = r.Number
		}
	}

	return n
}

// ranIn reports a drive created in the period, or one with a task the
// ledger saw active in it. The record keeps no time a task ended, so its
// final shape alone cannot tell an overnight finish from a drive left for
// weeks with a task that needs the person.
func ranIn(r *drivestore.Record, since, until time.Time, active map[string]bool) bool {
	if inWindow(r.Created, since, until) {
		return true
	}

	return slices.ContainsFunc(r.Tasks, func(t drivestore.Task) bool { return active[strings.ToUpper(t.ID)] })
}

// DriveSummaries is each drive that ran in the period, by its outcome so far.
func DriveSummaries(repo string, drives []*drivestore.Record, since, until time.Time, active map[string]bool) []Drive {
	out := []Drive{}

	for _, r := range drives {
		if !ranIn(r, since, until, active) {
			continue
		}

		d := Drive{Repo: repo, Number: r.Number, Tasks: len(r.Tasks)}

		for _, t := range r.Tasks {
			switch {
			case t.Outcome != nil && t.Outcome.Code == 0:
				d.Shipped++
			case t.Outcome != nil:
				d.NeedsYou++
			}
		}

		out = append(out, d)
	}

	slices.SortFunc(out, func(a, b Drive) int { return a.Number - b.Number })

	return out
}

// activeMinutes is each task's active minutes in windows ending in the
// period, the way f10 stats counts them: waits on the user left out.
func activeMinutes(events []ledger.Event, since, until time.Time) map[string]float64 {
	out := map[string]float64{}

	for _, w := range timing.Windows(events) {
		if w.Task != "" && inWindow(w.To, since, until) {
			out[strings.ToUpper(w.Task)] += w.Active.Minutes()
		}
	}

	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")

	return strings.TrimSpace(line)
}

// Contributed reports a report the ledger added anything to.
func Contributed(s Standup) bool {
	for _, p := range s.Projects {
		if len(p.Drives) > 0 {
			return true
		}

		for _, t := range p.Tasks {
			if t.Ledger && (t.Blocked != nil || len(t.Activity) > 0 || t.Stage != "" || t.Driven != 0) {
				return true
			}

			if t.Ledger && len(t.PRs) == 0 && len(t.Commits) == 0 {
				return true
			}
		}
	}

	return false
}
