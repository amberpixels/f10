package main

import (
	"slices"
	"testing"
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
