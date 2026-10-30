package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
)

const scopedProject = `---
verify:
  - run: just cli::lint
    paths: [cli/**]
  - run: shellcheck bin/*
    paths: [bin/**, "*.sh"]
---
## Verify

Never run ` + "`just eval`" + `.
`

// A repo on a feature branch off main, origin/HEAD pointing at main, so
// verifyDiff finds its base the way it does in a task's worktree.
func verifyRepo(t *testing.T) (*layout.Layout, *facts.Effective) {
	t.Helper()

	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	writeTestFile(t, filepath.Join(repo, "README.md"), "x\n")
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "root")
	git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	git(t, repo, "switch", "-q", "-c", "feature")

	instr := t.TempDir()
	writeTestFile(t, filepath.Join(instr, "project.md"), scopedProject)

	lay := &layout.Layout{
		Project:      "demo",
		CheckoutRoot: repo,
		Layering:     layout.LayeringExtends,
		Layers:       []layout.Layer{{Label: "main", Dir: instr}},
	}

	eff := facts.Build(lay, &probe.Probes{})
	if errs := eff.Errors(); errs != "" {
		t.Fatalf("project.md: %s", errs)
	}

	return lay, eff
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScopedVerify(t *testing.T) {
	cases := []struct {
		name  string
		files []string // written into the tree, left uncommitted and untracked
		want  []string
		not   []string
	}{
		{
			name:  "a markdown-only diff checks nothing",
			files: []string{"README.md", "docs/guide.md"},
			want: []string{
				"verify: nothing to check  the diff against main touches none of cli/**, bin/**, *.sh",
				"  rules from the Verify heading, not commands:\n    Never run `just eval`.",
			},
			not: []string{"just cli::lint", "shellcheck"},
		},
		{
			name:  "a non-ASCII path is read verbatim",
			files: []string{"cli/\u00fc.go"},
			want:  []string{"  just cli::lint  (cli/** matched cli/\u00fc.go)"},
		},
		{
			name:  "a new untracked Go file runs the Go command",
			files: []string{"cli/new.go"},
			want: []string{
				"verify: declared (main), scoped to the diff against main",
				"  just cli::lint  (cli/** matched cli/new.go)",
				"  not run: shellcheck bin/*  (nothing changed under bin/**, *.sh)",
			},
		},
		{
			name:  "a shell file anywhere runs the shell command",
			files: []string{"scripts/x.sh"},
			want:  []string{"  shellcheck bin/*  (*.sh matched scripts/x.sh)", "  not run: just cli::lint"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lay, eff := verifyRepo(t)

			for _, f := range tc.files {
				writeTestFile(t, filepath.Join(lay.CheckoutRoot, f), "y\n")
			}

			base, changed, err := verifyDiff(t.Context(), lay)
			if err != nil {
				t.Fatal(err)
			}

			got := scopedVerifyLine(eff, fieldNamed(eff.Fields, "Verify"), base, changed, nil)

			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in\n%s", w, got)
				}
			}

			for _, n := range tc.not {
				if strings.Contains(got, "  "+n+"  (") {
					t.Errorf("%q runs in\n%s", n, got)
				}
			}
		})
	}
}

// A file renamed out of a scoped directory still calls for that directory's
// command: removing it can break what the command checks.
func TestScopedVerifyReadsBothSidesOfARename(t *testing.T) {
	lay, _ := verifyRepo(t)
	root := lay.CheckoutRoot

	writeTestFile(t, filepath.Join(root, "cli", "a.go"), "package a\n")
	git(t, root, "add", ".")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "a")
	git(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, root, "mv", "cli/a.go", "a.go")

	_, changed, err := verifyDiff(t.Context(), lay)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(changed, "cli/a.go") || !slices.Contains(changed, "a.go") {
		t.Errorf("changed = %q, want both sides of the rename", changed)
	}
}

// A committed change counts as much as one in the tree: the fork point, not
// HEAD, is what the diff is read against.
func TestScopedVerifyReadsCommits(t *testing.T) {
	lay, _ := verifyRepo(t)

	writeTestFile(t, filepath.Join(lay.CheckoutRoot, "bin", "tool"), "#!/bin/sh\n")
	git(t, lay.CheckoutRoot, "add", ".")
	git(t, lay.CheckoutRoot, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "tool")

	_, changed, err := verifyDiff(t.Context(), lay)
	if err != nil {
		t.Fatal(err)
	}

	if len(changed) != 1 || changed[0] != "bin/tool" {
		t.Errorf("changed = %q, want [bin/tool]", changed)
	}
}

func TestScopedVerifyFallsBackToEveryCommand(t *testing.T) {
	_, eff := verifyRepo(t)

	got := scopedVerifyLine(eff, facts.Field{}, "", nil, errors.New("HEAD is detached"))

	want := "verify: declared (main), every command: the diff could not be read (HEAD is detached)\n" +
		"  just cli::lint\n  shellcheck bin/*\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A command with no paths runs on every diff, the empty one included.
func TestUnscopedCommandAlwaysRuns(t *testing.T) {
	run, left := facts.ScopeVerify(
		[]facts.VerifyDecl{{Run: "just test"}, {Run: "just lint", Paths: []string{"cli/**"}}},
		nil,
	)

	if len(run) != 1 || run[0].Run != "just test" || len(left) != 1 {
		t.Errorf("run = %+v, left = %+v", run, left)
	}
}
