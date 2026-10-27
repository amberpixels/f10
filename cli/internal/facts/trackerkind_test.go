package facts

import "testing"

func TestProseTrackerKind(t *testing.T) {
	cases := []struct {
		name  string
		lower string
		want  string
	}{
		{name: "skill name in a code span", lower: "invoke the `create-notion-task` skill", want: ""},
		{name: "kind alone in a code span", lower: "`notion`", want: ""},
		{name: "fenced block", lower: "fetch with:\n\n```\ngh api https://api.github.com/repos\n```\n", want: ""},
		{name: "host name", lower: "see api.github.com", want: ""},
		{name: "hyphenated word", lower: "github-actions runs the checks", want: ""},
		{name: "url", lower: "https://notion.so/abc", want: ""},
		{name: "cli name", lower: "jira-cli", want: ""},
		{name: "sentence end", lower: "notion. task ids `abc-####`.", want: "Notion"},
		{name: "kind with its noun", lower: "github issues (via gh)", want: "GitHub Issues"},
		{name: "bold", lower: "**jira**. ids look like abc-1234.", want: "Jira"},
		{name: "link text", lower: "the [notion](https://notion.so/x) board", want: "Notion"},
		{name: "possessive", lower: "notion's database", want: "Notion"},
		{name: "typographic possessive", lower: "notion’s database", want: "Notion"},
		{name: "single quotes", lower: "the 'jira' board", want: "Jira"},
		{name: "underscore emphasis", lower: "_jira_. ids look like abc-1234.", want: "Jira"},
		{
			name:  "prose beside a span",
			lower: "notion database; create with the `create-github-task` skill",
			want:  "Notion",
		},
		{name: "first needle wins", lower: "jira, mirrored to notion", want: "Notion"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := proseTrackerKind(c.lower); got != c.want {
				t.Errorf("proseTrackerKind(%q) = %q, want %q", c.lower, got, c.want)
			}
		})
	}
}
