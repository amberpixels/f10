package render

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// TaskLinks prints task ids for people: an id becomes a terminal link when
// linking is on and its url is known. Urls come from the ledger only, so no
// command waits on the tracker to print. A nil one prints every id plain.
type TaskLinks struct {
	On   bool
	urls map[string]string // by linkKey(project, id)
}

// Load reads the urls the ledger holds for ids under project, in one query.
// The ledger is never opened when linking is off.
func (l *TaskLinks) Load(ctx context.Context, project string, ids ...string) {
	l.LoadAs(ctx, project, project, ids...)
}

// LoadAs is Load for a caller that names the project by key rather than by
// its ledger project, as standup does by project name.
func (l *TaskLinks) LoadAs(ctx context.Context, project, key string, ids ...string) {
	if l == nil || !l.On || project == "" || len(ids) == 0 {
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
func (l *TaskLinks) Known(project, id, url string) {
	if l == nil || !l.On || url == "" {
		return
	}

	l.urls[linkKey(project, id)] = url
}

// ID is id as printed for people; a bundle links each member on its own.
func (l *TaskLinks) ID(project, id string) string {
	if l == nil || !l.On {
		return id
	}

	if url, ok := l.urls[linkKey(project, id)]; ok {
		return Hyperlink(url, id)
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

// LinkTerminal is swapped in tests, which write to a buffer.
var LinkTerminal = ui.IsTerminal

// LinkBuffer holds output its caller copies on to another writer, so it
// links when that writer would.
type LinkBuffer struct {
	bytes.Buffer

	Links bool
}

// Terminal makes the buffer read as the writer it is copied on to.
func (b *LinkBuffer) Terminal() bool { return b.Links }

// LinksFor links only on a terminal: piped output, --plain and --json are
// parsed by agents and scripts, which escape codes would break.
func LinksFor(w io.Writer, plain bool) *TaskLinks {
	return LinksOn(!plain && LinkTerminal(w))
}

func LinksOn(on bool) *TaskLinks {
	return &TaskLinks{On: on, urls: map[string]string{}}
}

func linkKey(project, id string) string { return project + " " + id }

// Hyperlink is an OSC 8 link, which a terminal without support shows as text.
func Hyperlink(url, text string) string {
	if url == "" {
		return text
	}

	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

var osc8 = regexp.MustCompile("\x1b\\]8;;[^\x1b]*\x1b\\\\")

// VisibleWidth is the columns s takes on screen, its links' escapes not counted.
func VisibleWidth(s string) int {
	return runewidth.StringWidth(osc8.ReplaceAllString(s, ""))
}

// PadRight pads s to width visible columns, which %-*s gets wrong for a link.
func PadRight(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-VisibleWidth(s)))
}

// TaskFact is a report's task row, a bundle's ids linked one by one.
func TaskFact(ctx context.Context, w io.Writer, dir, id string) Fact {
	links := LinksFor(w, false)

	project := ""
	if links.On {
		project = target.ProjectOf(ctx, dir)
	}

	links.Load(ctx, project, id)

	return Fact{Label: "task", Value: links.ID(project, id)}
}

// Wrap breaks text at spaces; width 0 or below disables it, and a token
// longer than the width is hard-cut rather than overflowed.
func Wrap(text string, width int) []string {
	if width <= 0 || len(text) <= width {
		return []string{text}
	}

	var lines []string

	for len(text) > width {
		cut := strings.LastIndex(text[:width+1], " ")
		if cut <= 0 {
			cut = width
		}

		lines = append(lines, strings.TrimRight(text[:cut], " "))
		text = strings.TrimLeft(text[cut:], " ")
	}

	return append(lines, text)
}
