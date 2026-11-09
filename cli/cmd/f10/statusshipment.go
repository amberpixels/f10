package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// shipment is the facts block a ship report opens with, reprinted from what
// f10 holds locally; only a pushed task with no PR recorded asks the host.
type shipment struct {
	task, title, url, branch, pr, commit, plan, handoff string

	prState string // the PR's state, when the host was asked
	pulled  bool   // the host was asked for the PR, so the fallback asks no more

	verdicts []verdictPath
	reviews  []reviewPath

	link bool // the file paths link on a terminal; off for --plain
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
func (s shipment) facts() []fact {
	rows := []fact{
		{label: "task", value: s.task},
		{label: "title", value: s.title},
		{label: "url", value: s.url, url: true},
		{label: "branch", value: s.branch},
		{label: "pr", value: s.pr, url: true},
		{label: "commit", value: s.commit},
		{label: "plan", value: s.plan, path: s.link},
		{label: "handoff", value: s.handoff, path: s.link},
	}

	// a subject judged twice takes each file's number, so every label names one file
	seen := map[string]int{}
	for _, v := range s.verdicts {
		seen[v.Subject]++
	}

	for _, v := range s.verdicts {
		label := "judge " + v.Subject
		if seen[v.Subject] > 1 {
			label += " " + strings.TrimSuffix(filepath.Base(v.Path), ".md")
		}

		rows = append(rows, fact{label: label, value: v.Path, path: s.link})
	}

	for _, r := range s.reviews {
		rows = append(rows, fact{label: "review " + r.Round, value: r.Path, path: s.link})
	}

	return slices.DeleteFunc(rows, func(f fact) bool { return f.value == "" })
}

// readShipment fills the shipment from the ledger's task ref and PR, git and
// the storage root. The commit row waits for a commit of the task's own:
// before one, HEAD is the base's.
func readShipment(ctx context.Context, in taskStatusInput, events []ledger.Event, known ledger.TaskRef) shipment {
	lt := ledgerFor(events, in.id, func(token string) string { return token })

	s := shipment{
		task:   in.id,
		title:  known.Title,
		url:    known.URL,
		branch: in.branch,
		pr:     lt.pr,
	}

	// a PR opened by a script around the host CLI leaves no pr_opened; only a
	// pushed task asks the host, so a status before the PR stays local
	if s.pr == "" && lt.pushed && in.host != nil && in.branch != "" {
		s.pulled = true
		if p, err := in.host.pull(ctx, in.branch); err == nil {
			s.pr, s.prState = p.url(), p.status()
		}
	}

	if in.storage != "" {
		s.verdicts, s.reviews = shipFiles(in.storage, in.id)
	}

	if in.main != "" && in.branch != "" {
		if def, err := localDefault(ctx, in.main); err == nil && baseHasCode(ctx, in.main, in.branch, def) {
			s.commit = gitx.Out(ctx, in.main, "rev-parse", "--short", "refs/heads/"+in.branch)
		}
	}

	if exists(in.plan) {
		s.plan = in.plan
	}

	if exists(in.handoff) {
		s.handoff = in.handoff
	}

	return s
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
