package facts

import "testing"

func TestIDPrefix(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		want     string
	}{
		{
			// how a project.md actually writes it: the placeholders sit
			// inside backticks and bold, so nothing word-like follows them
			name:     "placeholder format in markup",
			declared: "Notion. Task id format: **`ABC-####`** (uppercase - plans go to `.f10/plans/ABC-####.md`).",
			want:     "ABC",
		},
		{name: "bare placeholder format", declared: "Task id format: GH-###", want: "GH"},
		{name: "literal sample", declared: "Jira. Ids look like ABC-1234.", want: "ABC"},
		{
			// a placeholder anywhere beats a sample-shaped token that was
			// never an id at all
			name:     "placeholder wins over an incidental dash-number",
			declared: "Bodies are UTF-8. Task id format: **`ABC-####`**.",
			want:     "ABC",
		},
		{name: "a kind alone declares no format", declared: "GitHub Issues (via gh)", want: ""},
		{name: "no format says nothing", declared: "some tracker", want: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := idPrefix(c.declared); got != c.want {
				t.Errorf("idPrefix(%q) = %q, want %q", c.declared, got, c.want)
			}
		})
	}
}

func TestProjectPrefix(t *testing.T) {
	cases := []struct{ name, want string }{
		// a short name with a digit is already an abbreviation
		{name: "f10", want: "F10"},
		{name: "r3", want: "R3"},
		{name: "k1", want: "K1"},
		// segments give initials
		{name: "git-undo", want: "GU"},
		{name: "notion-sdk-go", want: "NSG"},
		{name: "obsidian_daily.orbit", want: "ODO"},
		{name: "My Project", want: "MP"},
		// one word gives its first three characters, digit or not
		{name: "runwell", want: "RUN"},
		{name: "herdr", want: "HER"},
		{name: "d3rtyjson", want: "D3R"},
		{name: "go", want: "GO"},
		// non-ASCII letters are not part of the alphabet an id uses, so a
		// segment made of them drops out
		{name: "f10-плагин", want: "F10"},
		// nothing usable derives nothing: too short, or a leading digit
		{name: "x", want: ""},
		{name: "3d-tool", want: ""},
		{name: "", want: ""},
		{name: "---", want: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := projectPrefix(c.name); got != c.want {
				t.Errorf("projectPrefix(%q) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}
