package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// shipment is the facts block a ship report opens with, reprinted from what
// f10 holds locally; only a pushed task with no PR recorded asks the host.
// --json carries it whole under `shipment`.
type shipment struct {
	Task    string `json:"task"`
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	Branch  string `json:"branch,omitempty"`
	PR      string `json:"pr,omitempty"`
	PRState string `json:"pr_state,omitempty"` // when the host was asked
	Commit  string `json:"commit,omitempty"`
	Plan    string `json:"plan,omitempty"`
	Handoff string `json:"handoff,omitempty"`

	Verdicts []verdictPath `json:"verdicts,omitempty"`
	Reviews  []reviewPath  `json:"reviews,omitempty"`

	pulled bool // the host was asked for the PR, so nothing asks it again
}

// verdictPath is a judge verdict file the facts block links, by its subject.
type verdictPath struct {
	Subject string `json:"subject"` // task, plan or change
	Path    string `json:"path"`
}

// reviewPath is a findings file the facts block links, by its round.
type reviewPath struct {
	Round string `json:"round"`
	Path  string `json:"path"`
}

// facts is the block's rows in the ship report's order, empty ones left out.
// link puts a terminal link on the file paths; --plain passes false, since
// writeReport links a path fact on any terminal.
func (s *shipment) facts(link bool) []render.Fact {
	rows := []render.Fact{
		{Label: "task", Value: s.Task},
		{Label: "title", Value: s.Title},
		{Label: "url", Value: s.URL, URL: true},
		{Label: "branch", Value: s.Branch},
		{Label: "pr", Value: s.PR, URL: true},
		{Label: "commit", Value: s.Commit},
		{Label: "plan", Value: s.Plan, Path: link},
		{Label: "handoff", Value: s.Handoff, Path: link},
	}

	// a subject judged twice takes each file's number, so every label names one file
	seen := map[string]int{}
	for _, v := range s.Verdicts {
		seen[v.Subject]++
	}

	for _, v := range s.Verdicts {
		label := "judge " + v.Subject
		if seen[v.Subject] > 1 {
			label += " " + strings.TrimSuffix(filepath.Base(v.Path), ".md")
		}

		rows = append(rows, render.Fact{Label: label, Value: v.Path, Path: link})
	}

	for _, r := range s.Reviews {
		rows = append(rows, render.Fact{Label: "review " + r.Round, Value: r.Path, Path: link})
	}

	return slices.DeleteFunc(rows, func(f render.Fact) bool { return f.Value == "" })
}

// readShipment fills the shipment from the ledger's task ref and PR, git and
// the storage root. The commit row waits for a commit of the task's own:
// before one, HEAD is the base's.
//
//nolint:lostfield // PRState comes from askPR, only for a pushed task
func readShipment(ctx context.Context, in taskStatusInput, events []ledger.Event, known ledger.TaskRef) shipment {
	lt := lookaround.FoldLedger(events, in.id, func(token string) string { return token })

	s := shipment{
		Task:   in.id,
		Title:  known.Title,
		URL:    known.URL,
		Branch: in.branch,
		PR:     lt.PR,
	}

	// a PR opened by a script around the host CLI leaves no pr_opened; only a
	// pushed task asks the host, so a status before the PR stays local
	if s.PR == "" && lt.Pushed {
		s.askPR(ctx, in)
	}

	if in.storage != "" {
		s.Verdicts, s.Reviews = shipFiles(in.storage, in.id)
	}

	if in.main != "" && in.branch != "" {
		if def, err := stack.LocalDefault(ctx, in.main); err == nil && stack.BaseHasCode(ctx, in.main, in.branch, def) {
			s.Commit = gitx.Out(ctx, in.main, "rev-parse", "--short", "refs/heads/"+in.branch)
		}
	}

	if fsx.Exists(in.plan) {
		s.Plan = in.plan
	}

	if fsx.Exists(in.handoff) {
		s.Handoff = in.handoff
	}

	return s
}

// askPR reads the branch's PR from the host, once: a PR the host does not
// know leaves the ledger's url standing.
func (s *shipment) askPR(ctx context.Context, in taskStatusInput) {
	if s.pulled || in.host == nil || in.branch == "" {
		return
	}

	s.pulled = true
	if p, err := in.host.PR(ctx, in.branch); err == nil && p.URL != "" {
		s.PR, s.PRState = p.URL, p.Status
	}
}

// shipFiles is the task's verdict and findings files. Verdicts from before
// the open series are an earlier PR's, dropped as the judge row drops them.
func shipFiles(root, id string) ([]verdictPath, []reviewPath) {
	reviews := storage.Root(root).Reviews(id)
	since := seriesStart(reviews)

	var (
		verdicts []verdictPath
		rounds   []reviewPath
	)

	for _, v := range judgeFiles(root, id) {
		if !v.at.Before(since) {
			verdicts = append(verdicts, verdictPath{Subject: v.judged, Path: v.path})
		}
	}

	for _, path := range findings.Series(reviews) {
		rounds = append(rounds, reviewPath{Round: strings.TrimSuffix(filepath.Base(path), ".md"), Path: path})
	}

	return verdicts, rounds
}
