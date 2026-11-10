package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/statusview"
)

func TestFileLink(t *testing.T) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ path, target string }{
		{"/tmp/plans/FT-1.md", "file:///tmp/plans/FT-1.md"},
		{"plans/FT-1.md", "file://" + filepath.Join(cwd, "plans", "FT-1.md")},
		{"/tmp/my plans/FT-1.md", "file:///tmp/my%20plans/FT-1.md"},
	} {
		m := linkSpan.FindStringSubmatch(render.FileLink(c.path))
		if m == nil {
			t.Fatalf("%s: no link in %q", c.path, render.FileLink(c.path))
		}

		if m[1] != c.target || m[2] != c.path {
			t.Errorf("%s: linked %q as %q, want %q as the path", c.path, m[2], m[1], c.target)
		}
	}

	if got := render.FileLink("/tmp/reviews/FT-1"); got != "/tmp/reviews/FT-1" {
		t.Errorf("a non-markdown path linked: %q", got)
	}
}

func TestWriteReportLinksPaths(t *testing.T) {
	rows := []render.Fact{
		{Label: "pr", Value: "https://github.com/o/r/pull/1", URL: true},
		{Label: "plan", Value: "/tmp/plans/archive/FT-1.1.md", Path: true},
		{Label: "reviews", Value: "/tmp/reviews/FT-1"},
	}

	var plain bytes.Buffer

	withTerminal(t, false)

	if err := render.WriteReport(&plain, rows, nil); err != nil {
		t.Fatal(err)
	}

	want := "pr:       ↗ https://github.com/o/r/pull/1\n" +
		"plan:       /tmp/plans/archive/FT-1.1.md\n" +
		"reviews:    /tmp/reviews/FT-1\n"
	if plain.String() != want {
		t.Errorf("piped report changed:\n%q\nwant\n%q", plain.String(), want)
	}

	var term bytes.Buffer

	withTerminal(t, true)

	if err := render.WriteReport(&term, rows, nil); err != nil {
		t.Fatal(err)
	}

	spans := linkSpan.FindAllStringSubmatch(term.String(), -1)
	if len(spans) != 1 || spans[0][1] != "file:///tmp/plans/archive/FT-1.1.md" {
		t.Fatalf("want one link, on the plan row:\n%q", term.String())
	}

	if !strings.Contains(term.String(), "plan:       \x1b]8;;") {
		t.Errorf("the plan row's marker cell is not blank:\n%q", term.String())
	}
}

// The plan row sits in the facts block, which links it once and never wraps.
func TestStatusFallbackLinksPlan(t *testing.T) {
	fb := statusview.TaskFallback(
		statusview.TaskStatusInput{ID: "GH-1", Root: "/tmp"},
		nil,
		statusview.Shipment{Task: "GH-1", Plan: "/tmp/.f10/plans/GH-1.md"},
	)

	withTerminal(t, true)

	var out bytes.Buffer
	fb.Render(&out, 30, true)

	spans := linkSpan.FindAllStringSubmatch(out.String(), -1)
	if len(spans) != 1 || spans[0][1] != "file:///tmp/.f10/plans/GH-1.md" {
		t.Fatalf("want one link, on the plan row:\n%q", out.String())
	}

	out.Reset()
	fb.Render(&out, 0, false)

	if strings.Contains(out.String(), "\x1b]8;;") {
		t.Errorf("unlinked render carries a link:\n%q", out.String())
	}
}

func withTerminal(t *testing.T, on bool) {
	t.Helper()

	prev := render.LinkTerminal
	t.Cleanup(func() { render.LinkTerminal = prev })

	render.LinkTerminal = func(io.Writer) bool { return on }
}

// A drive copies finish's report out of a buffer, which links as the drive's
// own writer would.
func TestLinkBufferLinksAsItsWriter(t *testing.T) {
	rows := []render.Fact{{Label: "plan", Value: "/tmp/plans/FT-1.md", Path: true}}

	for _, on := range []bool{true, false} {
		buf := &render.LinkBuffer{Links: on}
		if err := render.WriteReport(buf, rows, nil); err != nil {
			t.Fatal(err)
		}

		if linked := linkSpan.MatchString(buf.String()); linked != on {
			t.Errorf("links %v: report linked %v:\n%q", on, linked, buf.String())
		}
	}
}
