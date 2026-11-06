package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
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
		m := linkSpan.FindStringSubmatch(fileLink(c.path))
		if m == nil {
			t.Fatalf("%s: no link in %q", c.path, fileLink(c.path))
		}

		if m[1] != c.target || m[2] != c.path {
			t.Errorf("%s: linked %q as %q, want %q as the path", c.path, m[2], m[1], c.target)
		}
	}

	if got := fileLink("/tmp/reviews/FT-1"); got != "/tmp/reviews/FT-1" {
		t.Errorf("a non-markdown path linked: %q", got)
	}
}

func TestWriteReportLinksPaths(t *testing.T) {
	rows := []fact{
		{label: "pr", value: "https://github.com/o/r/pull/1", url: true},
		{label: "plan", value: "/tmp/plans/archive/FT-1.1.md", path: true},
		{label: "reviews", value: "/tmp/reviews/FT-1"},
	}

	var plain bytes.Buffer

	withTerminal(t, false)

	if err := writeReport(&plain, rows, nil); err != nil {
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

	if err := writeReport(&term, rows, nil); err != nil {
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

func TestStatusFallbackLinksPlan(t *testing.T) {
	fb := fallback{Task: "GH-1", Root: "/tmp", Plan: "/tmp/.f10/plans/GH-1.md"}

	var out bytes.Buffer
	fb.render(&out, 30, true)

	spans := linkSpan.FindAllStringSubmatch(out.String(), -1)
	if len(spans) < 2 {
		t.Fatalf("a wrapped plan path should link every line:\n%q", out.String())
	}

	for _, s := range spans {
		if s[1] != "file:///tmp/.f10/plans/GH-1.md" {
			t.Errorf("plan line links to %q", s[1])
		}
	}

	out.Reset()
	fb.render(&out, 0, false)

	if strings.Contains(out.String(), "\x1b]8;;") {
		t.Errorf("unlinked render carries a link:\n%q", out.String())
	}
}

func withTerminal(t *testing.T, on bool) {
	t.Helper()

	prev := linkTerminal
	t.Cleanup(func() { linkTerminal = prev })

	linkTerminal = func(io.Writer) bool { return on }
}
