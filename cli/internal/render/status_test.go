package render_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/statusview"
)

// The ship report's rows in its order, each only when it has a value, the
// marker on the two urls alone.
func TestShipmentFacts(t *testing.T) {
	s := statusview.Shipment{
		Task:   "GH-1",
		Title:  "Add a retry",
		URL:    "https://gh/issues/1",
		Branch: "GH-1/add-a-retry",
		PR:     "https://gh/pr/9",
		Plan:   "/store/plans/GH-1.md",
	}

	var out bytes.Buffer
	if err := render.WriteReport(&out, s.Facts(false), nil); err != nil {
		t.Fatal(err)
	}

	want := "task:      GH-1\n" +
		"title:     Add a retry\n" +
		"url:     ↗ https://gh/issues/1\n" +
		"branch:    GH-1/add-a-retry\n" +
		"pr:      ↗ https://gh/pr/9\n" +
		"plan:      /store/plans/GH-1.md\n"
	if out.String() != want {
		t.Errorf("block =\n%s\nwant\n%s", out.String(), want)
	}
}

// A long run value wraps under the value column, never under the labels.
func TestWriteFactsWrapsUnderTheValueColumn(t *testing.T) {
	rows := []render.Fact{
		{Label: "url", Value: "https://gh/issues/1", URL: true},
		statusview.RunFact(
			"note",
			"stop: the change patches the symptom and the cause is the retry loop",
			lipgloss.NewStyle(),
		),
	}

	var out bytes.Buffer
	if err := render.WriteFacts(&out, rows, render.LabelWidth(rows), 40); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("the note did not wrap at 40 columns:\n%s", out.String())
	}

	col := strings.Index(lines[1], "stop:")
	for _, l := range lines[2:] {
		if len(l)-len(strings.TrimLeft(l, " ")) != col {
			t.Errorf("continuation %q does not start at column %d:\n%s", l, col, out.String())
		}

		if len(l) > 40 {
			t.Errorf("continuation %q runs past 40 columns", l)
		}
	}
}
