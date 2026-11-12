package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The refusals, before any host call: no remote reviewer declared, a fact
// outside the vocabulary, a marker that cannot apply where the review lands.
func TestReviewRefusesOnTheDeclaration(t *testing.T) {
	testkit.IsolateEnv(t)

	seam.Swap(t, &shell.Capture, func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		t.Errorf("host CLI reached before the refusal: %s %s", name, strings.Join(args, " "))

		return shell.Result{}, nil
	})

	cases := []struct{ name, front, review, want string }{
		{name: "no Review section", review: "", want: "declares no remote review"},
		{
			name:   "local-only section",
			review: "## Review\n\n- **Never flag** - vendored code\n",
			want:   "declares no remote review",
		},
		{
			name:  "a fact outside the vocabulary",
			front: "---\nreview:\n  reviewer: bot `claude[bot]`\n  arrives: by pigeon\n  trigger: automatic\n  done: posted, reaction `eyes`\n---\n",
			want:  "Arrives as line(s) could not be read",
		},
		{
			name:  "threads on a comment",
			front: "---\nreview:\n  reviewer: bot `claude[bot]`\n  arrives: issue comment\n  trigger: automatic\n  done: posted, thread resolved\n---\n",
			want:  "does not arrive inline",
		},
		{
			name:  "a settled mark outside the vocabulary",
			front: "---\nreview:\n  reviewer: bot `claude[bot]`\n  arrives: issue comment\n  trigger: automatic\n  done: posted, reaction `eyes`\n  settled: checkbox\n---\n",
			want:  "settled line(s) could not be read",
		},
		{
			name:  "a settled mark the same as handled",
			front: "---\nreview:\n  reviewer: bot `claude[bot]`\n  arrives: issue comment\n  trigger: automatic\n  done: posted, reaction `eyes`\n  settled: reaction `eyes`\n---\n",
			want:  "settled mark is the handled mark",
		},
		{
			name:  "settled threads on a comment",
			front: "---\nreview:\n  reviewer: bot `claude[bot]`\n  arrives: issue comment\n  trigger: automatic\n  done: posted, reaction `eyes`\n  settled: thread resolved\n---\n",
			want:  "settled line(s) could not be read",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := testkit.MkRepo(t)
			dir := filepath.Join(repo, ".f10", "instructions")

			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}

			md := c.front + "# demo\n\n## Tracker\n\nGitHub Issues. Task ids `GH-###`.\n\n" + c.review
			if err := os.WriteFile(filepath.Join(dir, "project.md"), []byte(md), 0o600); err != nil {
				t.Fatal(err)
			}

			err := runApp(t, "-C", repo, "review", "pick").err
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}
