package statusview

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// Shipment is the facts block a ship report opens with, reprinted from what
// f10 holds locally; only a pushed task with no PR recorded asks the host.
// --json carries it whole under `shipment`.
type Shipment struct {
	Task    string `json:"task"`
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	Branch  string `json:"branch,omitempty"`
	PR      string `json:"pr,omitempty"`
	PRState string `json:"pr_state,omitempty"` // when the host was asked
	Commit  string `json:"commit,omitempty"`
	Plan    string `json:"plan,omitempty"`
	Handoff string `json:"handoff,omitempty"`
	Demo    string `json:"demo,omitempty"`

	Verdicts []verdictPath `json:"verdicts,omitempty"`
	Reviews  []reviewPath  `json:"reviews,omitempty"`

	pulled bool // the host was asked for the PR, so nothing asks it again
}

// Facts is the block's rows in the ship report's order, empty ones left out.
// link puts a terminal link on the file paths; --plain passes false, since
// writeReport links a path fact on any terminal.
func (s *Shipment) Facts(link bool) []render.Fact {
	rows := []render.Fact{
		{Label: "task", Value: s.Task},
		{Label: "title", Value: s.Title},
		{Label: "url", Value: s.URL, URL: true},
		{Label: "branch", Value: s.Branch},
		{Label: "pr", Value: s.PR, URL: true},
		{Label: "commit", Value: s.Commit},
		{Label: "plan", Value: s.Plan, Path: link},
		{Label: "handoff", Value: s.Handoff, Path: link},
		{Label: "demo", Value: s.Demo, Path: link},
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

// AskPR reads the branch's PR from the host, once: a PR the host does not
// know leaves the ledger's url standing.
func (s *Shipment) AskPR(ctx context.Context, in taskStatusInput) {
	if s.pulled || in.Host == nil || in.Branch == "" {
		return
	}

	s.pulled = true
	if p, err := in.Host.PR(ctx, in.Branch); err == nil && p.URL != "" {
		s.PR, s.PRState = p.URL, p.Status
	}
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

// readShipment fills the shipment from the ledger's task ref and PR, git and
// the storage root. The commit row waits for a commit of the task's own:
// before one, HEAD is the base's.
//
//nolint:lostfield // PRState comes from AskPR, only for a pushed task
func readShipment(ctx context.Context, in taskStatusInput, events []ledger.Event, known ledger.TaskRef) Shipment {
	lt := lookaround.FoldLedger(events, in.ID, func(token string) string { return token })

	s := Shipment{
		Task:   in.ID,
		Title:  known.Title,
		URL:    known.URL,
		Branch: in.Branch,
		PR:     lt.PR,
	}

	// a PR opened by a script around the host CLI leaves no pr_opened; only a
	// pushed task asks the host, so a status before the PR stays local
	if s.PR == "" && lt.Pushed {
		s.AskPR(ctx, in)
	}

	if in.storage != "" {
		s.Verdicts, s.Reviews = ShipFiles(in.storage, in.ID)
	}

	if in.Main != "" && in.Branch != "" {
		if def, err := stack.LocalDefault(ctx, in.Main); err == nil && stack.BaseHasCode(ctx, in.Main, in.Branch, def) {
			s.Commit = gitx.Out(ctx, in.Main, "rev-parse", "--short", "refs/heads/"+in.Branch)
		}
	}

	if fsx.Exists(in.Plan) {
		s.Plan = in.Plan
	}

	if fsx.Exists(in.Handoff) {
		s.Handoff = in.Handoff
	}

	if in.storage != "" {
		s.Demo = demoReport(storage.Root(in.storage), in.ID)
	}

	return s
}

// demoReport is the task's demo report, "" before one is written.
func demoReport(root storage.Root, id string) string {
	if path := filepath.Join(root.Demo(id), "report.html"); fsx.Exists(path) {
		return path
	}

	return ""
}

// finishedShipment is a finished task's block, read from what f10 finish
// archived: the plan, the handoff and the series its last PR closed.
func finishedShipment(root storage.Root, row TaskRow, known ledger.TaskRef) Shipment {
	s := Shipment{
		Task:    row.ID,
		Title:   cmp.Or(known.Title, tasktitle.Clean(row.Title, row.ID)),
		URL:     cmp.Or(known.URL, row.URL),
		PR:      row.PR,
		PRState: row.PRState,
		Plan:    archive.NewestArchived(root.Archive(), row.ID),
		Handoff: archive.NewestArchived(storage.ArchiveOf(root.Handoffs()), row.ID),
		Demo:    demoReport(root, row.ID),
	}

	s.Verdicts, s.Reviews = FinishedShipFiles(root.Path(), row.ID)

	return s
}

// ShipFiles is the task's verdict and findings files. Verdicts from before
// the open series are an earlier PR's, dropped as the judge row drops them.
func ShipFiles(root, id string) ([]verdictPath, []reviewPath) {
	reviews := storage.Root(root).Reviews(id)
	since := archive.SeriesStart(reviews)

	var verdicts []verdictPath

	for _, v := range archive.JudgeFiles(root, id) {
		if !v.At.Before(since) {
			verdicts = append(verdicts, verdictPath{Subject: v.Judged, Path: v.Path})
		}
	}

	return verdicts, roundPaths(reviews)
}

// FinishedShipFiles is ShipFiles for a finished task: f10 finish moved its
// findings to the archive, so the rounds are the newest closed series', and
// the verdicts those written since the series before it closed. Rounds still
// in the live directory are the last PR's, closed without f10 finish.
func FinishedShipFiles(root, id string) ([]verdictPath, []reviewPath) {
	live := storage.Root(root).Reviews(id)

	closed := archive.ClosedPRs(live)
	if len(closed) == 0 || len(findings.Series(live)) > 0 {
		return ShipFiles(root, id)
	}

	var since time.Time
	if len(closed) > 1 {
		since = archive.SeriesEndAt(closed[len(closed)-2])
	}

	var verdicts []verdictPath

	for _, v := range archive.JudgeFiles(root, id) {
		if !v.At.Before(since) {
			verdicts = append(verdicts, verdictPath{Subject: v.Judged, Path: v.Path})
		}
	}

	return verdicts, roundPaths(closed[len(closed)-1])
}

// roundPaths is the findings files in dir, a row each by its round.
func roundPaths(dir string) []reviewPath {
	var rounds []reviewPath

	for _, path := range findings.Series(dir) {
		rounds = append(rounds, reviewPath{Round: strings.TrimSuffix(filepath.Base(path), ".md"), Path: path})
	}

	return rounds
}
