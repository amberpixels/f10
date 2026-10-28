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

type judgeOutcome struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

// fileOutcomes reads the task's findings and verdict files: a round is the
// remote half's when its reviewer is not local (a file both halves wrote
// counts once for each), a verdict the task judge's when its subject is the
// task, the plan judge's when it is a plan.
func fileOutcomes(storageRoot, task string) outcomes {
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

// finishedStorage is where a finished task's findings and verdicts still
// are: the shared root, else the .f10 of the checkout its last run was in,
// while that is on disk.
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
// entry per half the file holds. A prior-round block counts with its round
// only when the round reopened it.
func reviewRounds(storageRoot, task string) []roundFile {
	var rounds []roundFile

	for _, path := range numbered(filepath.Join(storageRoot, "reviews", task)) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		f, err := findings.Parse(string(raw))
		if err != nil {
			continue
		}

		// indexed by remote: the local half, then the remote one
		var halves [2]*roundFile

		half := func(remote bool) *roundFile {
			i := 0
			if remote {
				i = 1
			}

			if halves[i] == nil {
				halves[i] = &roundFile{round: reviewRound{Round: f.Round}, remote: remote, at: modTime(path)}
			}

			return halves[i]
		}

		// a half the header names shows even with nothing found, a file
		// naming none the local one
		local, remote := f.Halves()
		if remote {
			half(true)
		}

		if local || !remote {
			half(false)
		}

		for _, fd := range f.Findings {
			if fd.Prior && !findings.Reopens(fd.Status) {
				continue
			}

			h := half(f.Remote(fd))
			h.round.Found++

			if !fd.Prior {
				h.raised++
			}

			switch v, _, _ := strings.Cut(strings.ToLower(fd.Verdict), " "); v {
			case "fix":
				h.round.Fixed++
			case "skip":
				h.round.Skipped++
			case "ask", "":
				h.round.Ask++
			}
		}

		for _, h := range halves {
			if h != nil {
				rounds = append(rounds, *h)
			}
		}
	}

	return rounds
}

// verdictFile is one verdict file that judges the task, not an idea.
type verdictFile struct {
	outcome judgeOutcome
	judged  string // task or plan when its subject is one, else change
	at      time.Time
}

// judgeFiles is the task's verdict files that parse, oldest first.
func judgeFiles(storageRoot, task string) []verdictFile {
	var out []verdictFile

	for _, path := range numbered(filepath.Join(storageRoot, "verdicts", task)) {
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
			outcome: judgeOutcome{Verdict: v.Verdict, Reason: v.Reason},
			judged:  judged,
			at:      modTime(path),
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
