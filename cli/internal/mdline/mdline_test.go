package mdline

import "testing"

func TestItem(t *testing.T) {
	cases := []struct {
		line, want string
		ok         bool
	}{
		{"- local: implement", "local: implement", true},
		{"  * local: implement", "local: implement", true},
		{"+ local: implement", "local: implement", true},
		{"**local**: implement", "**local**: implement", false},
		{"local: implement", "local: implement", false},
	}

	for _, c := range cases {
		if got, ok := Item(c.line); got != c.want || ok != c.ok {
			t.Errorf("Item(%q) = %q, %v; want %q, %v", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestCut(t *testing.T) {
	cases := []struct {
		line       string
		seps       []string
		key, value string
		ok         bool
	}{
		{"local: implement → commit", []string{":"}, "local", "implement → commit", true},
		{"**local**: `implement → commit`", []string{":"}, "local", "`implement → commit`", true},
		{"**local:** implement", []string{":"}, "local", "implement", true},
		{"`local`: implement", []string{":"}, "local", "implement", true},
		{"_local_: implement", []string{":"}, "local", "implement", true},
		{"**After:** FT-12", []string{":"}, "After", "FT-12", true},
		{"After:#12", []string{":"}, "After", "#12", true},
		{"**Reviewer** - bot `claude[bot]`", []string{" - ", ":"}, "Reviewer", "bot `claude[bot]`", true},
		{"**Reviewer** bot `claude[bot]`", []string{" - ", ":"}, "Reviewer", "bot `claude[bot]`", true},
		{"Reviewer - bot `claude[bot]`", []string{" - ", ":"}, "Reviewer", "bot `claude[bot]`", true},
		{"Trigger: manual - comment `@claude`", []string{" - ", ":"}, "Trigger", "manual - comment `@claude`", true},
		{
			"Done, handled - posted, reaction `eyes`",
			[]string{" - ", ":"},
			"Done, handled",
			"posted, reaction `eyes`",
			true,
		},
		{"**direct** (planless): `implement → push`", []string{":"}, "direct (planless)", "`implement → push`", true},
		{"no separator here", []string{":"}, "", "", false},
		{": no key", []string{":"}, "", "no key", false},
	}

	for _, c := range cases {
		key, value, ok := Cut(c.line, c.seps...)
		if key != c.key || value != c.value || ok != c.ok {
			t.Errorf("Cut(%q) = %q, %q, %v; want %q, %q, %v", c.line, key, value, ok, c.key, c.value, c.ok)
		}
	}
}

func TestBareAndUnmark(t *testing.T) {
	if got := Bare(" **FT-12** "); got != "FT-12" {
		t.Errorf("Bare = %q", got)
	}

	if got := Bare("`squash`"); got != "squash" {
		t.Errorf("Bare = %q", got)
	}

	if got := Unmark("Open PRs; **merge method**: `squash`."); got != "Open PRs; merge method: squash." {
		t.Errorf("Unmark = %q", got)
	}
}

func TestProse(t *testing.T) {
	cases := []struct{ in, want string }{
		{in: "invoke the `create-notion-task` skill", want: "invoke the   skill"},
		{in: "before\n```sh\ngh issue view\n```\nafter", want: "before\n \n \n \nafter"},
		{in: "a lone ` backtick", want: "a lone ` backtick"},
	}

	for _, c := range cases {
		if got := Prose(c.in); got != c.want {
			t.Errorf("Prose(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
