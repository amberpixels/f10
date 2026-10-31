package main

import (
	"context"
	"io"
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// taskLinks prints task ids for people: an id becomes a terminal link when
// linking is on and its url is known. Urls come from the ledger only, so no
// command waits on the tracker to print. A nil one prints every id plain.
type taskLinks struct {
	on   bool
	urls map[string]string // by linkKey(project, id)
}

// linkTerminal is swapped in tests, which write to a buffer.
var linkTerminal = isTerminal

// linksFor links only on a terminal: piped output, --plain and --json are
// parsed by agents and scripts, which escape codes would break.
func linksFor(w io.Writer, plain bool) *taskLinks {
	return linksOn(!plain && linkTerminal(w))
}

func linksOn(on bool) *taskLinks {
	return &taskLinks{on: on, urls: map[string]string{}}
}

func linkKey(project, id string) string { return project + " " + id }

// Load reads the urls the ledger holds for ids under project, in one query.
// The ledger is never opened when linking is off.
func (l *taskLinks) Load(ctx context.Context, project string, ids ...string) {
	l.LoadAs(ctx, project, project, ids...)
}

// LoadAs is Load for a caller that names the project by key rather than by
// its ledger project, as standup does by project name.
func (l *taskLinks) LoadAs(ctx context.Context, project, key string, ids ...string) {
	if l == nil || !l.on || project == "" || len(ids) == 0 {
		return
	}

	var members []string
	for _, id := range ids {
		members = append(members, strings.Split(id, ref.Separator)...)
	}

	for id, r := range ledger.LookupRefs(ctx, project, members) {
		l.Known(key, id, r.URL)
	}
}

// Known records a url the caller already holds, so it needs no ledger read.
func (l *taskLinks) Known(project, id, url string) {
	if l == nil || !l.on || url == "" {
		return
	}

	l.urls[linkKey(project, id)] = url
}

// ID is id as printed for people; a bundle links each member on its own.
func (l *taskLinks) ID(project, id string) string {
	if l == nil || !l.on {
		return id
	}

	if url, ok := l.urls[linkKey(project, id)]; ok {
		return hyperlink(url, id)
	}

	if !strings.Contains(id, ref.Separator) {
		return id
	}

	members := strings.Split(id, ref.Separator)
	for i, m := range members {
		members[i] = l.ID(project, m)
	}

	return strings.Join(members, ref.Separator)
}

// hyperlink is an OSC 8 link, which a terminal without support shows as text.
func hyperlink(url, text string) string {
	if url == "" {
		return text
	}

	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

var osc8 = regexp.MustCompile("\x1b\\]8;;[^\x1b]*\x1b\\\\")

// visibleWidth is the columns s takes on screen, its links' escapes not counted.
func visibleWidth(s string) int {
	return runewidth.StringWidth(osc8.ReplaceAllString(s, ""))
}

// padRight pads s to width visible columns, which %-*s gets wrong for a link.
func padRight(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-visibleWidth(s)))
}

// taskFact is a report's task row, a bundle's ids linked one by one.
func taskFact(ctx context.Context, w io.Writer, dir, id string) fact {
	links := linksFor(w, false)

	project := ""
	if links.on {
		project = projectOf(ctx, dir)
	}

	links.Load(ctx, project, id)

	return fact{label: "task", value: links.ID(project, id)}
}
