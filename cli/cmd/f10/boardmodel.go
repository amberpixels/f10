package main

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// A boardModel is what a renderer draws. board.html builds the same model in
// JavaScript; the JSON tags are the parity test's contract.
type boardModel struct {
	Header string      `json:"header,omitempty"`
	Notes  []boardNote `json:"notes"`
	Empty  string      `json:"empty,omitempty"`
	Rows   []boardLine `json:"rows"`
	Quiet  string      `json:"quiet,omitempty"`
}

type boardNote struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type boardLine struct {
	Group       string    `json:"group"`
	Key         string    `json:"key"`
	URL         string    `json:"url,omitempty"`
	Stage       string    `json:"stage"`
	StageFrom   string    `json:"stage_from"`
	Title       string    `json:"title,omitempty"`
	PR          int       `json:"pr,omitempty"`
	PRURL       string    `json:"pr_url,omitempty"`
	Parts       []boardPR `json:"parts,omitempty"` // a feed row's task's other requests overlapping PR
	Who         string    `json:"who,omitempty"`
	BranchLeft  bool      `json:"branch_left,omitempty"` // a merged row's branch outlived its worktree
	Next        string    `json:"next,omitempty"`
	NextCommand bool      `json:"next_command,omitempty"`
}

type boardPR struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
}

// boardLines reduces the board to its model. The feed skips its local
// source on purpose, so a skip there is no note; a local-only checkout
// notes its host alone.
//
//nolint:lostfield // a reduction by design
func boardLines(b lookaround.Board) boardModel {
	m := boardModel{Notes: []boardNote{}, Rows: []boardLine{}}
	feed := b.View == lookaround.ViewFeed

	for _, s := range b.Sources {
		switch {
		case s.Skipped && slices.Contains(s.Notes, lookaround.NoteLocalOnly):
			m.Notes = append(m.Notes, boardNote{Name: s.Name, Status: lookaround.NoteLocalOnly})
		case s.Skipped && (feed || b.Project.LocalOnly):
		case s.Skipped:
			m.Notes = append(m.Notes, boardNote{Name: s.Name, Status: "skipped"})
		case !s.OK:
			m.Notes = append(m.Notes, boardNote{Name: s.Name, Status: "failed", Error: s.Error})
		}
	}

	if feed {
		return feedLines(b, m)
	}

	if len(b.Rows) == 0 {
		m.Empty = "nothing in flight, nothing next, nothing done since " + localDate(b.Window.Since)

		return m
	}

	for _, r := range b.Rows {
		m.Rows = append(m.Rows, lineOf(r))
	}

	return m
}

func lineOf(r lookaround.Row) boardLine {
	line := boardLine{
		Group:      lookaround.StateGroup(r),
		Key:        r.Key,
		URL:        rowURL(r),
		Stage:      r.Stage,
		StageFrom:  r.StageFrom,
		Who:        boardWho(r),
		BranchLeft: r.Where.BranchLeft,
	}

	if r.Task != nil {
		line.Title = r.Task.Title
	} else if r.Where.PR != nil {
		line.Title = titleAfterKey(r.Where.PR.Title, r.Key)
	}

	if r.Where.PR != nil {
		line.PR, line.PRURL = r.Where.PR.Number, r.Where.PR.URL
	}

	if r.Next.Command != "" {
		line.Next, line.NextCommand = r.Next.Command, true
	} else {
		line.Next = r.Next.Text
	}

	return line
}

//nolint:lostfield // a reduction by design
func feedLines(b lookaround.Board, m boardModel) boardModel {
	m.Header = windowLabel(b.Window)

	var rows []lookaround.FeedRow

	if b.Feed != nil {
		rows = b.Feed.Rows

		if q := b.Feed.Quiet; q.Count > 0 {
			m.Quiet = lookaround.Plural(q.Count, "open request") +
				" with no commits, reviews or status changes in the window"
			if !q.Oldest.IsZero() {
				m.Quiet += ", the oldest opened " + localDate(q.Oldest)
			}
		}
	}

	if len(rows) == 0 {
		m.Empty = "no activity in the window"

		return m
	}

	for _, r := range rows {
		m.Rows = append(m.Rows, feedLine(r))
	}

	return m
}

func feedLine(r lookaround.FeedRow) boardLine {
	line := boardLine{
		Group:     strings.ReplaceAll(r.Group, "-", " "),
		Key:       r.Key,
		Stage:     r.Group,
		StageFrom: r.From,
		Next:      r.Summary,
	}

	switch {
	case r.Abandoned:
		line.Stage = "abandoned"
	case r.Group == lookaround.GroupNewTasks:
		line.Stage = "new"
	}

	if r.Task != nil {
		line.Title, line.URL = r.Task.Title, r.Task.URL
	}

	if r.PR != nil {
		line.PR, line.PRURL = r.PR.Number, r.PR.URL
		line.URL = cmp.Or(line.URL, r.PR.URL)

		if line.Title == "" {
			line.Title = titleAfterKey(r.PR.Title, r.Key)
		}
	}

	for _, p := range r.Related {
		if p.Relation == lookaround.RelationPart {
			line.Parts = append(line.Parts, boardPR{p.Number, p.URL})
		}
	}

	switch {
	case r.Who.Author != "":
		line.Who = r.Who.Author
	case len(r.Who.Assignees) > 0:
		line.Who = r.Who.Assignees[0]
	}

	if r.Owes != "" {
		line.Next += ", " + r.Owes + " owes the next move"
	}

	return line
}

func windowLabel(w lookaround.Window) string {
	from := localDate(w.Since)
	label := cmp.Or(w.Chosen, "since") + " · " + from

	if !w.End.IsZero() {
		if to := localDate(w.End.Add(-time.Second)); to != from {
			label += " to " + to
		}
	}

	if w.Fallback {
		label += " (no activity today)"
	}

	return label
}

func localDate(t time.Time) string { return t.Local().Format(time.DateOnly) }

// titleAfterKey drops the "<key>: " a request's title often starts with,
// since the row already shows the key; FT-12 never matches FT-121.
func titleAfterKey(title, key string) string {
	rest, ok := strings.CutPrefix(title, key)
	if !ok || key == "" || rest == "" || (rest[0] != ':' && rest[0] != ' ') {
		return title
	}

	return cmp.Or(strings.TrimLeft(rest, ": -"), title)
}
