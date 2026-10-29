// Package timing turns ledger events into the active time a run spent in a
// step: the window between two step boundaries, minus every wait on the user
// inside it. It never reports a wall duration: a window it cannot bound, or
// one an unended wait runs into, yields nothing rather than a guess.
package timing

import (
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The steps named here; any other flow entry is measured under its own name.
const (
	Plan      = "plan"
	Implement = "implement"
	Review    = "review"
	Resolve   = "resolve"
	Judge     = "judge"
	PR        = "pr"
	Cleanup   = "cleanup"
	Glue      = "glue" // a run's time outside every step
)

// Window is one stretch of a step in one session.
type Window struct {
	Task    string
	Session string
	Step    string
	From    time.Time
	To      time.Time
	Active  time.Duration
}

// span is a wait; a zero end is a wait nothing ended.
type span struct{ from, to time.Time }

// Claim reads a `state_set` row's arguments: `ship running implement` is
// phase ship, status running, leaf implement. Empty for any other row.
func Claim(e ledger.Event) (string, string, string) {
	if e.Kind != ledger.StateSet {
		return "", "", ""
	}

	args, _ := e.Payload.Val["args"].([]any)

	var words [3]string

	for i := range min(len(args), 3) {
		words[i], _ = args[i].(string)
	}

	return words[0], words[1], words[2]
}

// Windows is every bounded step window and glue window, oldest first by end.
// A plan run counts to the plan it saves; a ship step from the claim entering
// it to the claim leaving it (next step, stop, backstop, next run). A plan
// claimed `prior` or `skipped` was never planned here. Glue is a run's
// segments minus every step window inside them.
func Windows(events []ledger.Event) []Window {
	var out []Window

	for _, evs := range bySession(events) {
		// a session with no wait at all would measure wall time
		waits := waitSpans(evs)
		if len(waits) == 0 {
			continue
		}

		steps := windows(evs)
		busy := slices.Clone(waits)

		for _, w := range steps {
			busy = append(busy, span{w.From, w.To})

			if active, ok := activeTime(w.From, w.To, waits); ok {
				w.Active = active
				out = append(out, w)
			}
		}

		for _, w := range segments(evs) {
			if active, ok := activeTime(w.From, w.To, busy); ok {
				w.Active = active
				out = append(out, w)
			}
		}
	}

	slices.SortStableFunc(out, func(a, b Window) int { return a.To.Compare(b.To) })

	return out
}

func bySession(events []ledger.Event) [][]ledger.Event {
	var order []string

	groups := map[string][]ledger.Event{}

	for _, e := range events {
		if e.Session == "" {
			continue
		}

		if _, ok := groups[e.Session]; !ok {
			order = append(order, e.Session)
		}

		groups[e.Session] = append(groups[e.Session], e)
	}

	out := make([][]ledger.Event, 0, len(order))
	for _, s := range order {
		out = append(out, groups[s])
	}

	return out
}

func windows(evs []ledger.Event) []Window {
	var (
		out     []Window
		step    *Window // the ship step open now
		entry   string  // the flow entry step was claimed as
		split   bool    // the review entry open now reached its resolve
		planRun *Window // a /f10:plan run not yet saved
		inShip  bool
	)

	open := func(e ledger.Event, name string) *Window {
		return &Window{Task: e.Task, Session: e.Session, Step: name, From: e.At}
	}

	end := func(w *Window, e ledger.Event) {
		w.To = e.At
		if e.Task != "" {
			w.Task = e.Task
		}

		out = append(out, *w)
	}

	leave := func(e ledger.Event) {
		if step != nil {
			end(step, e)
			step = nil
		}
	}

	for _, e := range evs {
		switch e.Kind {
		case ledger.RunStarted:
			skill, _ := e.Payload.Val["skill"].(string)
			if !slices.Contains(state.Phases, skill) {
				continue // a step's own skill, resolve or catchup, runs inside the run
			}

			leave(e)

			planRun, inShip = nil, skill == "ship"
			if skill == "plan" {
				planRun = open(e, Plan)
			}
		case ledger.PlanSaved:
			if planRun != nil {
				end(planRun, e)
				planRun = nil
			}
		case ledger.ReviewRoundOpened:
			// review ends at the findings file; resolve settles it under the same entry
			if step != nil && step.Step == Review && !split {
				end(step, e)
				step, split = open(e, Resolve), true
			}
		case ledger.SessionStopped:
			if e.Payload.Val["backstop"] == true {
				leave(e)
			}
		case ledger.StateSet:
			if !inShip {
				continue
			}

			phase, status, leaf := Claim(e)

			switch {
			case phase == "plan" && status == "running" && step == nil:
				step, entry = open(e, Plan), Plan
			case phase == "plan" && (status == "prior" || status == "skipped") && step != nil && step.Step == Plan:
				step = nil
			case phase == "ship" && status == "running":
				if step != nil && entry == leaf {
					continue // the badge resent, or a driven run resuming its own step
				}

				leave(e)

				if leaf != "" {
					// `review:local` is review, `cleanup:comments` cleanup
					name, _, _ := strings.Cut(leaf, ":")
					step, entry, split = open(e, name), leaf, false
				}
			case phase == "ship" && slices.Contains([]string{"done", "failed", "partial", "blocked"}, status):
				leave(e)
			}
		}
	}

	return out
}

// segments is every closed stretch of a plan or ship run, as glue windows. It
// closes at the first turn end after the run's last row, so the report after
// it counts too, else at the next run or a backstop.
func segments(evs []ledger.Event) []Window {
	var (
		out  []Window
		seg  *Window
		last string // the skill of the session's last run
		over bool   // the run wrote its last row; its turn ending closes it
	)

	closeAt := func(e ledger.Event) {
		if seg != nil {
			seg.To = e.At
			out = append(out, *seg)
			seg = nil
		}
	}

	openAt := func(e ledger.Event) {
		seg, over = &Window{Task: e.Task, Session: e.Session, Step: Glue, From: e.At}, false
	}

	for _, e := range evs {
		if seg != nil && e.Task != "" {
			seg.Task = e.Task
		}

		switch e.Kind {
		case ledger.RunStarted:
			skill, _ := e.Payload.Val["skill"].(string)
			if !slices.Contains(state.Phases, skill) {
				continue
			}

			closeAt(e)

			last = skill
			if skill == "plan" || skill == "ship" {
				openAt(e)
			}
		case ledger.PlanSaved:
			over = over || seg != nil && last == "plan"
		case ledger.WaitStarted:
			if over && e.Payload.Val["reason"] == "turn" {
				closeAt(e)
			}
		case ledger.SessionStopped:
			if e.Payload.Val["backstop"] == true {
				closeAt(e)
			}
		case ledger.StateSet:
			if last != "ship" {
				continue
			}

			phase, status, _ := Claim(e)

			switch {
			case phase != "ship":
			case status == "running" && seg == nil:
				openAt(e)
			case status == "running":
				over = false
			case slices.Contains([]string{"done", "failed", "partial", "blocked"}, status):
				over = seg != nil
			}
		}
	}

	return out
}

// waitSpans pairs each wait with its end; a start followed by a start lost
// its end and ends there. A turn also ends at the next claim or run start,
// since Stop and the next prompt are async hooks: a prompt hook racing Stop
// would leave the turn open, and an observed row can land after Stop.
func waitSpans(evs []ledger.Event) []span {
	var out []span

	for i, e := range evs {
		if e.Kind != ledger.WaitStarted {
			continue
		}

		turn := e.Payload.Val["reason"] == "turn"
		s := span{from: e.At}

		for _, next := range evs[i+1:] {
			if next.Kind == ledger.WaitEnded || next.Kind == ledger.WaitStarted ||
				turn && (next.Source == ledger.Claimed || next.Kind == ledger.RunStarted) && next.At.After(e.At) {
				s.to = next.At

				break
			}
		}

		out = append(out, s)
	}

	return out
}

// activeTime is the window's length minus the union of the waits inside
// it; false when a wait with no end starts before the window ends.
func activeTime(from, to time.Time, waits []span) (time.Duration, bool) {
	var clipped []span

	for _, w := range waits {
		if w.to.IsZero() {
			if w.from.Before(to) {
				return 0, false
			}

			continue
		}

		lo, hi := maxTime(w.from, from), minTime(w.to, to)
		if lo.Before(hi) {
			clipped = append(clipped, span{lo, hi})
		}
	}

	slices.SortFunc(clipped, func(a, b span) int { return a.from.Compare(b.from) })

	active := to.Sub(from)

	var reach time.Time // the latest wait end subtracted so far

	for _, w := range clipped {
		lo := maxTime(w.from, reach)
		if lo.Before(w.to) {
			active -= w.to.Sub(lo)
			reach = w.to
		}
	}

	return active, true
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}

	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}

	return b
}
