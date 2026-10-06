package probe

import (
	"os"
	"path/filepath"
	"testing"
)

const automaticWorkflow = `name: Claude review
on:
  pull_request:
    types: [opened, synchronize]
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: anthropics/claude-code-action@v1
        with:
          anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}
          prompt: review this
`

const manualWorkflow = `name: Claude
on:
  issue_comment:
    types: [created]
jobs:
  claude:
    if: contains(github.event.comment.body, '@reviewbot')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: anthropics/claude-code-action@beta
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
`

const bothWorkflow = `on: [pull_request, issue_comment]
jobs:
  r:
    steps:
      - uses: anthropics/claude-code-action@v1
`

const otherWorkflow = `on: pull_request
jobs:
  ci:
    steps:
      - uses: actions/checkout@v7
      - run: just ci
`

func TestParseWorkflow(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want *Review
	}{
		{
			name: "automatic on pull_request",
			yaml: automaticWorkflow,
			want: &Review{Reviewer: "claude[bot]", Trigger: "automatic"},
		},
		{
			name: "manual with a custom mention and the runner token",
			yaml: manualWorkflow,
			want: &Review{Reviewer: "github-actions[bot]", Trigger: "manual", Mention: "@reviewbot"},
		},
		{
			name: "both events in list form",
			yaml: bothWorkflow,
			want: &Review{Reviewer: "claude[bot]", Trigger: "automatic, manual", Mention: "@claude"},
		},
		{name: "another action", yaml: otherWorkflow, want: nil},
		{name: "not yaml", yaml: "on: [\njobs: {", want: nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseWorkflow([]byte(c.yaml))

			if (got == nil) != (c.want == nil) {
				t.Fatalf("parseWorkflow = %+v, want %+v", got, c.want)
			}

			if got != nil && *got != *c.want {
				t.Errorf("parseWorkflow = %+v, want %+v", *got, *c.want)
			}
		})
	}
}

func TestReviewWorkflowPicksTheReviewFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	// ci.yml sorts first and is not a review; the review workflow is found
	// past it, and the evidence names the file
	writeFile(t, filepath.Join(dir, "ci.yml"), otherWorkflow)
	writeFile(t, filepath.Join(dir, "review.yaml"), automaticWorkflow)

	got := reviewWorkflow(root)
	if got == nil {
		t.Fatal("no review workflow found")
	}

	if got.Workflow != filepath.Join(".github", "workflows", "review.yaml") {
		t.Errorf("evidence = %q", got.Workflow)
	}

	if reviewWorkflow(t.TempDir()) != nil {
		t.Error("a checkout without workflows declared a review")
	}
}
