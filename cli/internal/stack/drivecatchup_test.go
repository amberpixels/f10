package stack_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestRunVerifyStopsAtTheFirstRedAndLogsIt(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "logs", "v.log")

	res, err := stack.RunVerify(t.Context(), dir, []string{"true", "echo broke; exit 3", "touch never"}, log)
	if err != nil || res.Failed != "echo broke; exit 3" || res.Log != log {
		t.Fatalf("run = %+v, %v", res, err)
	}

	if b, _ := os.ReadFile(log); strings.TrimSpace(string(b)) != "broke" {
		t.Errorf("log = %q", b)
	}

	if _, err := os.Stat(filepath.Join(dir, "never")); err == nil {
		t.Error("a command after the red one ran")
	}

	if res, err := stack.RunVerify(t.Context(), dir, []string{"true"}, log); err != nil || res.Failed != "" {
		t.Errorf("green run = %+v, %v", res, err)
	}
}

// ReadTree and WithBase are the one read of a worktree a catchup acts on:
// each state reads as the wording drive and the watcher print for it.
func TestReadTree(t *testing.T) {
	cases := map[string]struct {
		setup func(r testkit.GitRepo)
		left  string // a substring of left(), "" for nothing left
		lost  bool
	}{
		"held and clean": {setup: func(r testkit.GitRepo) { r.At(testkit.During, "rebase", "-q", "main") }},
		"base lost":      {left: "FT-1/a does not hold main", lost: true},
		"dirty": {
			setup: func(r testkit.GitRepo) {
				if err := os.WriteFile(filepath.Join(r.Dir, "x.go"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			left: "1 uncommitted paths in ",
		},
		"marker": {
			setup: func(r testkit.GitRepo) {
				r.At(testkit.During, "rebase", "-q", "main")
				r.Commit(testkit.During, "a.go", "<<<<<<< HEAD\nx\n", "a marker committed")
			},
			left: "conflict markers left in a.go",
		},
		"merge in progress": {
			setup: func(r testkit.GitRepo) {
				r.Commit(testkit.During, "b.go", "package main // not b\n", "b on the branch")

				cmd := exec.CommandContext(t.Context(), "git", "merge", "-q", "main")
				cmd.Dir = r.Dir
				_ = cmd.Run() // conflicts, leaving the merge in progress
			},
			left: "a merge is still in progress in ",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := testkit.NewCatchupRepo(t)
			if tc.setup != nil {
				tc.setup(r)
			}

			tree, err := stack.ReadTree(t.Context(), r.Dir)
			if err == nil && tree.Busy() == "" {
				tree, err = tree.WithBase(t.Context(), "FT-1/a", stack.CatchupPlan{Base: "main", SHA: r.Sha("main")})
			}

			if err != nil {
				t.Fatal(err)
			}

			switch left := tree.Left(); {
			case tc.left == "" && left != "":
				t.Errorf("left = %q, want nothing", left)
			case !strings.Contains(left, tc.left):
				t.Errorf("left = %q, want %q", left, tc.left)
			}

			if tree.BaseLost() != tc.lost {
				t.Errorf("baseLost = %v, want %v", tree.BaseLost(), tc.lost)
			}
		})
	}
}
