package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// runHistory is what a finished ship run went through: its pipeline steps
// as the ledger's claims recorded them, its review rounds and its latest
// judge verdict, each empty where its source is missing.
type runHistory struct {
	Steps  []string      `json:"steps,omitempty"`
	Review []reviewRound `json:"review,omitempty"`
	Judge  *judgeOutcome `json:"judge,omitempty"`
}

// reviewRound counts one findings file. Ask is a finding still waiting on
// a verdict: an `ask`, or none written yet.
type reviewRound struct {
	Round   string `json:"round"`
	Found   int    `json:"found"`
	Fixed   int    `json:"fixed"`
	Skipped int    `json:"skipped"`
	Ask     int    `json:"ask"`
}

type judgeOutcome struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

// historyFor gathers a run's history: steps from the session's claims up
// to until, review and judge from the task's files under storageRoot ("" for
// none reachable). Nothing found is nil, so an older run prints as before.
func historyFor(events []ledger.Event, session string, until time.Time, storageRoot, task string) *runHistory {
	h := runHistory{Steps: shipSteps(events, session, until)}

	if storageRoot != "" {
		h.Review = reviewRounds(storageRoot, task)
		h.Judge = latestJudge(storageRoot, task)
	}

	if h.Steps == nil && h.Review == nil && h.Judge == nil {
		return nil
	}

	return &h
}

// doneHistory is a live run's history once its ship phase is done.
func doneHistory(events []ledger.Event, r *state.Run, storageRoot string) *runHistory {
	if r.Ship != "done" {
		return nil
	}

	return historyFor(events, r.Session, r.Updated, storageRoot, r.Task)
}

// claimHistory is a finished task's history from the ledger's last claim
// on it; shared is the out-of-tree storage root, "" with in-repo storage.
func claimHistory(events []ledger.Event, claim *state.Run, shared string) *runHistory {
	if claim == nil {
		return nil
	}

	return historyFor(events, claim.Session, claim.Updated, finishedStorage(shared, claim.Root), claim.Task)
}

// finishedStorage is where a finished task's findings and verdicts still
// are: the shared root with out-of-tree storage, else the .f10 of the
// checkout its last run was in, while that is on disk.
func finishedStorage(shared, checkout string) string {
	if shared != "" {
		return shared
	}

	if checkout == "" {
		return ""
	}

	if root := filepath.Join(checkout, ".f10"); exists(root) {
		return root
	}

	return ""
}

// shipSteps is the steps the session's last ship run up to until entered,
// in order: each `set ship running <step>` claim after the ship skill
// started. Only a phase skill begins a run; one a step calls mid-pipeline
// (resolve, catchup) does not end it. A step claimed again right after itself is one entry (a resent
// badge, a driven run resuming); one that comes back after another step is
// listed again.
func shipSteps(events []ledger.Event, session string, until time.Time) []string {
	var steps []string

	inShip := false

	for _, e := range events {
		if e.Session != session || e.At.After(until) {
			continue
		}

		switch e.Kind {
		case ledger.RunStarted:
			skill := payloadString(e.Payload.Val, "skill")
			if !slices.Contains(state.Phases, skill) {
				continue
			}

			inShip = skill == "ship"
			steps = nil
		case ledger.StateSet:
			if !inShip {
				continue
			}

			args, _ := e.Payload.Val["args"].([]any)
			if len(args) < 3 || args[0] != "ship" || args[1] != "running" {
				continue
			}

			step, _ := args[2].(string)
			if step != "" && (len(steps) == 0 || steps[len(steps)-1] != step) {
				steps = append(steps, step)
			}
		}
	}

	return steps
}

// reviewRounds counts every findings file of the task, in round order. A
// prior-round block counts with its round only when the round reopened it.
func reviewRounds(storageRoot, task string) []reviewRound {
	var rounds []reviewRound

	for _, path := range numbered(filepath.Join(storageRoot, "reviews", task)) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		f, err := findings.Parse(string(raw))
		if err != nil {
			continue
		}

		r := reviewRound{Round: f.Round}

		for _, fd := range f.Findings {
			if fd.Prior && !findings.Reopens(fd.Status) {
				continue
			}

			r.Found++

			switch v, _, _ := strings.Cut(strings.ToLower(fd.Verdict), " "); v {
			case "fix":
				r.Fixed++
			case "skip":
				r.Skipped++
			case "ask", "":
				r.Ask++
			}
		}

		rounds = append(rounds, r)
	}

	return rounds
}

// latestJudge is the newest verdict file of the task that parses and judges
// the task, not an idea.
func latestJudge(storageRoot, task string) *judgeOutcome {
	files := numbered(filepath.Join(storageRoot, "verdicts", task))

	for _, path := range slices.Backward(files) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		if v, err := verdict.Parse(string(raw)); err == nil && !v.Idea() {
			return &judgeOutcome{Verdict: v.Verdict, Reason: v.Reason}
		}
	}

	return nil
}

// numbered is dir's `<n>.md` files by n, so 10.md sorts after 9.md.
func numbered(dir string) []string {
	type file struct {
		n    int
		path string
	}

	var files []file

	for _, e := range dirEntries(dir) {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || e.IsDir() {
			continue
		}

		if n, err := strconv.Atoi(name); err == nil {
			files = append(files, file{n, filepath.Join(dir, e.Name())})
		}
	}

	slices.SortFunc(files, func(a, b file) int { return cmp.Compare(a.n, b.n) })

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}

	return paths
}

func dirEntries(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	return entries
}

// lines is the history as label and value, only the parts it holds:
// `steps implement, pr`, `review 3 found (2 fixed, 1 skipped)`,
// `judge proceed - <reason>`.
func (h runHistory) lines() [][2]string {
	var out [][2]string

	if len(h.Steps) > 0 {
		out = append(out, [2]string{"steps", strings.Join(h.Steps, ", ")})
	}

	if len(h.Review) > 0 {
		out = append(out, [2]string{"review", reviewText(h.Review)})
	}

	if h.Judge != nil {
		text := h.Judge.Verdict
		if h.Judge.Reason != "" {
			text += " - " + h.Judge.Reason
		}

		out = append(out, [2]string{"judge", text})
	}

	return out
}

// reviewText is one round's counts, or each round's, named, when there are
// several.
func reviewText(rounds []reviewRound) string {
	if len(rounds) == 1 {
		return roundText(rounds[0])
	}

	parts := make([]string, len(rounds))
	for i, r := range rounds {
		parts[i] = fmt.Sprintf("round %s: %s", r.Round, roundText(r))
	}

	return strings.Join(parts, "; ")
}

func roundText(r reviewRound) string {
	if r.Found == 0 {
		return "clean"
	}

	var counts []string

	for _, c := range []struct {
		n    int
		word string
	}{{r.Fixed, "fixed"}, {r.Skipped, "skipped"}, {r.Ask, "ask"}} {
		if c.n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", c.n, c.word))
		}
	}

	text := fmt.Sprintf("%d found", r.Found)
	if len(counts) > 0 {
		text += " (" + strings.Join(counts, ", ") + ")"
	}

	return text
}
