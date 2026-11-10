package statusview

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/timing"
)

// ReviewRound counts one findings file. Ask is a finding still waiting on
// a verdict: an `ask`, or none written yet.
type ReviewRound struct {
	Round   string `json:"round"`
	Found   int    `json:"found"`
	Fixed   int    `json:"fixed"`
	Skipped int    `json:"skipped"`
	Ask     int    `json:"ask"`
}

// FileOutcomes reads the task's findings and verdict files: a round is the
// remote half's when its reviewer is not local (a file both halves wrote
// counts once each), and a verdict goes by its subject: task, plan or change.
// Verdict files written before since are dropped: the judge writes them per
// task, so an earlier PR's verdicts share the directory.
func FileOutcomes(storageRoot, task string, since time.Time) Outcomes {
	var o Outcomes

	for _, rf := range reviewRounds(storageRoot, task) {
		if rf.remote {
			o.Remote, o.RemoteAt = append(o.Remote, rf.round), rf.at
			o.RemoteRaised += rf.raised
		} else {
			o.Local, o.LocalAt = append(o.Local, rf.round), rf.at
			o.LocalRaised += rf.raised
		}
	}

	for _, v := range archive.JudgeFiles(storageRoot, task) {
		if v.At.Before(since) {
			continue
		}

		switch v.Judged {
		case "task":
			o.TaskJudge, o.taskAt = &v.Outcome, v.At
		case "plan":
			o.PlanJudge, o.planAt = &v.Outcome, v.At
		default:
			o.ChangeJudge, o.changeAt = &v.Outcome, v.At
		}
	}

	return o
}

// ShipSteps is the steps the session's last ship run claimed before until.
// Only a phase skill begins a run, so resolve or catchup mid-pipeline does
// not end it; a step claimed again right after itself is one entry.
func ShipSteps(events []ledger.Event, session string, until time.Time) []string {
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
	round  ReviewRound
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
				round: ReviewRound{
					Round: f.Round, Found: t.Found, Fixed: t.Fixed, Skipped: t.Skipped, Ask: t.Open,
				},
				raised: t.Raised,
				remote: isRemote,
				at:     archive.ModTime(path),
			})
		}
	}

	return rounds
}

// reviewText is one round's counts, or each round's, named, when there are
// several.
func reviewText(rounds []ReviewRound) string {
	if len(rounds) == 1 {
		return roundText(rounds[0])
	}

	parts := make([]string, len(rounds))
	for i, r := range rounds {
		parts[i] = fmt.Sprintf("round %s: %s", r.Round, roundText(r))
	}

	return strings.Join(parts, "; ")
}

func roundText(r ReviewRound) string {
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
