package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/facts"
)

func TestWrap(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{
			name:  "fits unchanged",
			text:  "short value",
			width: 40,
			want:  []string{"short value"},
		},
		{
			name:  "zero width disables wrapping",
			text:  "a value far longer than any width",
			width: 0,
			want:  []string{"a value far longer than any width"},
		},
		{
			name:  "breaks at spaces",
			text:  "GH-1.md, GH-11.md, GH-14.md, GH-17.md",
			width: 20,
			want:  []string{"GH-1.md, GH-11.md,", "GH-14.md, GH-17.md"},
		},
		{
			name:  "unbreakable token is hard-cut, not overflowed",
			text:  "/a/very/long/path/with/no/spaces/at/all.md",
			width: 20,
			want:  []string{"/a/very/long/path/wi", "th/no/spaces/at/all.", "md"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wrap(tc.text, tc.width)
			if !slices.Equal(got, tc.want) {
				t.Errorf("wrap(%q, %d) = %q, want %q", tc.text, tc.width, got, tc.want)
			}

			for _, line := range got {
				if tc.width > 0 && len(line) > tc.width {
					t.Errorf("line %q exceeds width %d", line, tc.width)
				}
			}
		})
	}
}

func sampleView() *view {
	return &view{
		CheckoutRoot: "/repo",
		StorageRoot:  "/repo/.f10",
		Instructions: "main",
		Facts: []facts.Fact{
			{Key: "visibility", Value: "stealth", Origin: facts.OriginDefault},
			{Key: "merge_method", Value: "squash", Origin: "declared (main, frontmatter)"},
			// the widest row: 12 + 2 + 8 + 2 + 61 = 85 cells
			{Key: "stale_after", Value: strings.Repeat("x", 61), Origin: "declared (main, frontmatter)"},
		},
		Fields: []facts.Field{
			{Name: "Project", Value: "Go.", Origin: "declared (main)"},
			{
				Name:   "Tracker",
				Value:  "- **Fetch** - `gh issue view <n> --comments`\n- **Create** - `gh issue create`",
				Origin: "declared (main)",
			},
			{Name: "Verify", Value: "Run lint.\n- **Then** - tests\n- plain bullet - here", Origin: "declared (main)"},
		},
	}
}

// Every rule ends at the page width: the widest row, clamped to 60..100,
// never past the terminal; piped output draws none.
func TestRenderHumanPageWidth(t *testing.T) {
	for _, tc := range []struct{ limit, want int }{{200, 85}, {50, 50}, {0, 0}} {
		var b bytes.Buffer
		renderHuman(&b, sampleView(), tc.limit)

		rules := 0

		for line := range strings.SplitSeq(b.String(), "\n") {
			if !strings.Contains(line, "─") {
				continue
			}

			rules++

			if got := lipgloss.Width(line); got != tc.want {
				t.Errorf("limit %d: rule %q is %d cells, want %d", tc.limit, line, got, tc.want)
			}
		}

		if tc.want == 0 && rules > 0 || tc.want > 0 && rules == 0 {
			t.Errorf("limit %d: %d rules\n%s", tc.limit, rules, b.String())
		}
	}

	for _, tc := range []struct{ widest, want int }{{130, pageMax}, {20, pageMin}} {
		if got := pageWidth(tc.widest, 200); got != tc.want {
			t.Errorf("a %d-cell row on 200 columns = %d, want %d", tc.widest, got, tc.want)
		}
	}
}

// A body of only bold-label items prints as aligned sub-rows, markup gone;
// a body with anything else keeps its prose.
func TestRenderHumanKeyValues(t *testing.T) {
	var b bytes.Buffer
	renderHuman(&b, sampleView(), 120)
	out := b.String()

	for _, want := range []string{"\n Fetch   gh issue view <n> --comments\n", "\n Create  gh issue create\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	tracker := out[strings.Index(out, "Tracker"):strings.Index(out, "Verify")]
	if strings.ContainsAny(tracker, "*`") {
		t.Errorf("markup left in the Tracker stanza:\n%s", tracker)
	}

	if !strings.Contains(out, " - **Then** - tests\n") {
		t.Errorf("mixed prose was not kept as written:\n%s", out)
	}
}
