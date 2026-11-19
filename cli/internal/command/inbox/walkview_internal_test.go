package inbox

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/render"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func viewAt(width int, links bool) (walkView, *bytes.Buffer) {
	var out bytes.Buffer

	v := newWalkView(&out, links)
	v.width = width

	return v, &out
}

const (
	longURL = "https://github.com/amberpixels/app/actions/runs/4455/job/1356"
	// a sample commit sha longer than the 8 characters the walk shows
	sampleSHA = "8057a977c1d2"
)

// At any width no word is split and every line fits unless it holds a
// single word wider than the line; continuation lines hang under the
// question's text and the option's.
func TestWalkViewWraps(t *testing.T) {
	t.Parallel()

	a := asks.Ask{
		Task: "FT-1", AskedAt: time.Now(),
		Context: "Tests are red on the pull request for a reason that comes from main, not this change. " +
			"Log: " + longURL + ". Main: red at " + sampleSHA + " and 20261119.",
		Questions: []asks.Question{{
			N: 1, Text: "What should happen with the red tests on this branch before it merges?",
			Options: []string{"leave it red until main has its own fix", "fix it here"},
		}},
	}

	for _, width := range []int{40, 80} {
		v, out := viewAt(width, false)
		v.ask(a, shownQuestions(a), "FT-1", time.Now())

		text := out.String()
		words := strings.Fields(a.Context + " " + a.Questions[0].Text + " " + strings.Join(a.Questions[0].Options, " "))

		for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
			// only the url, a word wider than any line, may overflow
			if render.VisibleWidth(line) > width && !strings.Contains(line, longURL) {
				t.Errorf("width %d: line overflows: %q", width, line)
			}
		}

		for _, w := range words {
			switch {
			case strings.HasSuffix(w, ":"): // a label, shown as a row
				continue
			case w == sampleSHA:
				w = sampleSHA[:shaShown]
			}

			if !strings.Contains(text, w) {
				t.Errorf("width %d: word %q split or lost:\n%s", width, w, text)
			}
		}

		if strings.Contains(text, sampleSHA) || !strings.Contains(text, "20261119") {
			t.Errorf("width %d: sha not shortened or a number cut:\n%s", width, text)
		}

		if width == 40 && !strings.Contains(text, "\n    ") {
			t.Errorf("width 40: no hanging indent:\n%s", text)
		}
	}
}

// Labelled context sentences become rows with their labels in a column.
func TestWalkViewLabelRows(t *testing.T) {
	t.Parallel()

	v, out := viewAt(0, false)
	v.context("Red for a reason from main. Cause: the key is gone. Log: see the run.")

	want := "   Red for a reason from main.\n" +
		"   · cause  the key is gone.\n" +
		"   · log    see the run.\n"
	if got := out.String(); got != want {
		t.Errorf("context printed:\n%q\nwant\n%q", got, want)
	}
}

// Linked, a long url shows as its last path segments inside the link;
// piped, it prints whole.
func TestWalkViewShortensLinkedURL(t *testing.T) {
	t.Parallel()

	v, out := viewAt(0, true)
	v.context("Log: " + longURL)

	got := out.String()
	plain := ansi.ReplaceAllString(got, "")
	if !strings.Contains(got, "\x1b]8;;"+longURL+"\x1b\\") || !strings.Contains(plain, "↗ job/1356") {
		t.Errorf("linked url printed %q", got)
	}

	v, out = viewAt(0, false)
	v.context("Log: " + longURL)

	if !strings.Contains(out.String(), longURL) {
		t.Errorf("piped url printed %q", out.String())
	}
}
