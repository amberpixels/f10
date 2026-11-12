package render_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/testkit"
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
		m := testkit.LinkSpan.FindStringSubmatch(render.FileLink(c.path))
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

	testkit.WithTerminal(t, false)

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

	testkit.WithTerminal(t, true)

	if err := render.WriteReport(&term, rows, nil); err != nil {
		t.Fatal(err)
	}

	spans := testkit.LinkSpan.FindAllStringSubmatch(term.String(), -1)
	if len(spans) != 1 || spans[0][1] != "file:///tmp/plans/archive/FT-1.1.md" {
		t.Fatalf("want one link, on the plan row:\n%q", term.String())
	}

	if !strings.Contains(term.String(), "plan:       \x1b]8;;") {
		t.Errorf("the plan row's marker cell is not blank:\n%q", term.String())
	}
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

		if linked := testkit.LinkSpan.MatchString(buf.String()); linked != on {
			t.Errorf("links %v: report linked %v:\n%q", on, linked, buf.String())
		}
	}
}
