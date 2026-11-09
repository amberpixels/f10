package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/timing"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// reviewRound counts one findings file. Ask is a finding still waiting on
// a verdict: an `ask`, or none written yet.
type reviewRound struct {
	Round   string `json:"round"`
	Found   int    `json:"found"`
	Fixed   int    `json:"fixed"`
	Skipped int    `json:"skipped"`
	Ask     int    `json:"ask"`
}

// judgeOutcome is a verdict and, from its file, what the ship run did with
// it: the decision line and how many changes it listed.
type judgeOutcome struct {
	Verdict  string `json:"verdict"`
	Reason   string `json:"reason,omitempty"`
	Decision string `json:"decision,omitempty"`
	Changes  int    `json:"changes,omitempty"`
}

// fileOutcomes reads the task's findings and verdict files: a round is the
// remote half's when its reviewer is not local (a file both halves wrote
// counts once each), and a verdict goes by its subject: task, plan or change.
// Verdict files written before since are dropped: the judge writes them per
// task, so an earlier PR's verdicts share the directory.
func fileOutcomes(storageRoot, task string, since time.Time) outcomes {
	var o outcomes

	for _, rf := range reviewRounds(storageRoot, task) {
		if rf.remote {
			o.remote, o.remoteAt = append(o.remote, rf.round), rf.at
			o.remoteRaised += rf.raised
		} else {
			o.local, o.localAt = append(o.local, rf.round), rf.at
			o.localRaised += rf.raised
		}
	}

	for _, v := range judgeFiles(storageRoot, task) {
		if v.at.Before(since) {
			continue
		}

		switch v.judged {
		case "task":
			o.taskJudge, o.taskAt = &v.outcome, v.at
		case "plan":
			o.planJudge, o.planAt = &v.outcome, v.at
		default:
			o.changeJudge, o.changeAt = &v.outcome, v.at
		}
	}

	return o
}

// shipSteps is the steps the session's last ship run claimed before until.
// Only a phase skill begins a run, so resolve or catchup mid-pipeline does
// not end it; a step claimed again right after itself is one entry.
func shipSteps(events []ledger.Event, session string, until time.Time) []string {
	var steps []string

	inShip := false

	for _, e := range events {
		if e.Session != session || e.At.After(until) {
			continue
		}

		switch e.Kind {
		case ledger.RunStarted:
			skill := e.Skill()
			if !slices.Contains(state.Phases, skill) {
				continue
			}

			inShip = skill == "ship"
			steps = nil
		case ledger.StateSet:
			if !inShip {
				continue
			}

			phase, status, step := timing.Claim(e)
			if phase != "ship" || status != "running" {
				continue
			}

			if step != "" && (len(steps) == 0 || steps[len(steps)-1] != step) {
				steps = append(steps, step)
			}
		}
	}

	return steps
}

// roundFile is one half of a findings file: its counts, whether it is the
// remote half, and when the file was last written.
type roundFile struct {
	round  reviewRound
	raised int // findings new in this round, a prior one reopened not counted
	remote bool
	at     time.Time
}

// reviewRounds counts every findings file of the task, in round order, one
// entry per half the file holds.
func reviewRounds(storageRoot, task string) []roundFile {
	var rounds []roundFile

	for _, path := range findings.Series(storage.Root(storageRoot).Reviews(task)) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		f, err := findings.Parse(string(raw))
		if err != nil {
			continue
		}

		// a half the header names shows even with nothing found, a file
		// naming none the local one
		local, remote := f.Halves()

		for _, isRemote := range []bool{false, true} {
			t := f.TallyHalf(isRemote)

			shown := t.Found > 0 || (isRemote && remote) || (!isRemote && (local || !remote))
			if !shown {
				continue
			}

			rounds = append(rounds, roundFile{
				round: reviewRound{
					Round: f.Round, Found: t.Found, Fixed: t.Fixed, Skipped: t.Skipped, Ask: t.Open,
				},
				raised: t.Raised,
				remote: isRemote,
				at:     modTime(path),
			})
		}
	}

	return rounds
}

// verdictFile is one verdict file that judges the task, not an idea.
type verdictFile struct {
	outcome judgeOutcome
	judged  string // task or plan when its subject is one, else change
	at      time.Time
	path    string
}

// judgeFiles is the task's verdict files that parse, oldest first.
func judgeFiles(storageRoot, task string) []verdictFile {
	var out []verdictFile

	for _, path := range verdict.Series(storage.Root(storageRoot).Verdicts(task)) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		v, err := verdict.Parse(string(raw))
		if err != nil || v.Idea() {
			continue
		}

		judged, _, _ := strings.Cut(v.Subject, " ")
		if judged != "task" && judged != "plan" {
			judged = "change"
		}

		out = append(out, verdictFile{
			outcome: judgeOutcome{Verdict: v.Verdict, Reason: v.Reason, Decision: v.Decision, Changes: v.Changes},
			judged:  judged,
			at:      modTime(path),
			path:    path,
		})
	}

	return out
}

func modTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}

	return fi.ModTime()
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
