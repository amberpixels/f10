package observe

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSegments(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want [][]string
	}{
		{"and-chain", "git add -A && git commit -m wip", [][]string{{"git", "add", "-A"}, {"git", "commit", "-m", "wip"}}},
		{"quoted operators stay in the word", `git commit -m "a && b; c | d"`, [][]string{{"git", "commit", "-m", "a && b; c | d"}}},
		{"single quotes", `echo 'it''s' "x\"y"`, [][]string{{"echo", "its", `x"y`}}},
		{"redirections dropped", "f10-load-project.sh fetch plan 2>&1 >/tmp/out < in", [][]string{{"f10-load-project.sh", "fetch", "plan"}}},
		{"pipe and or", "a | b || c; d & e", [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}},
		{"cd prefix", "cd /work/acme && git push", [][]string{{"cd", "/work/acme"}, {"git", "push"}}},
		{
			"heredoc body skipped",
			"cat <<'EOF' > msg.txt\ngit push --force\nEOF\ngit commit -F msg.txt",
			[][]string{{"cat"}, {"git", "commit", "-F", "msg.txt"}},
		},
		{
			"substitution kept whole",
			"git commit -m \"$(cat <<'EOF'\nsubject && git push\nEOF\n)\"",
			[][]string{{"git", "commit", "-m", "$(cat <<'EOF'\nsubject && git push\nEOF\n)"}},
		},
		{"unquoted substitution", "echo $(git rev-parse HEAD) done", [][]string{{"echo", "$(git rev-parse HEAD)", "done"}}},
		{"comment", "git push # && git commit", [][]string{{"git", "push"}}},
		{"line continuation", "gh pr create \\\n  --fill", [][]string{{"gh", "pr", "create", "--fill"}}},
		{"subshell", "(cd x && make)", [][]string{{"cd", "x"}, {"make"}}},
		{"here-string", "grep x <<< \"$v\" && ls", [][]string{{"grep", "x"}, {"ls"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Segments(tc.line); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Segments(%q)\n got %q\nwant %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestFromBashCommandShapes(t *testing.T) {
	commit := "[main 1a2b3c4] subject\n 1 file changed\n"

	for _, tc := range []struct {
		name    string
		command string
		stdout  string
		kinds   []string
	}{
		{"env prefix", "GIT_AUTHOR_NAME=x git commit -m subject", commit, []string{"committed"}},
		{"git -C", "git -C /work/acme commit -m subject", commit, []string{"committed"}},
		{"git -c option", "git -c core.hooksPath=/dev/null commit -m subject", commit, []string{"committed"}},
		{"path to the script", "/plugins/f10/bin/f10-load-project.sh implement", "=== f10 project @ /x ===\n", []string{"step_started"}},
		{"commit text in an echo is not a commit", "echo git commit", commit, nil},
		{"gh issue view is not a create", "gh issue view 12", "https://github.com/acme/app/issues/12\n", nil},
		{"glab pr is not glab's word", "glab pr create", "https://gitlab.com/acme/app/-/merge_requests/1\n", nil},
		{"f10 without finish", "f10 status FT-12", "", nil},
		{"f10 pr create", "f10 pr create --title T --body-file b.md", "https://github.com/acme/app/pull/4\n", []string{"pr_created"}},
		{"f10 task create", "f10 task create --label bug < t.md", "FT-12\n", []string{"issue_created"}},
		{"f10 task create that failed", "f10 task create < t.md", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var kinds []string
			for _, o := range FromBash(tc.command, tc.stdout, "") {
				kinds = append(kinds, o.Kind())
			}

			if !reflect.DeepEqual(kinds, tc.kinds) {
				t.Errorf("kinds %v, want %v", kinds, tc.kinds)
			}
		})
	}
}

func TestFromWrite(t *testing.T) {
	for _, tc := range []struct {
		name, path, root string
		want             Observation
	}{
		{"in-repo plan", "/w/.f10/plans/FT-1.md", "", &PlanWritten{Path: "/w/.f10/plans/FT-1.md", Task: "FT-1"}},
		{"in-repo findings", "/w/.f10/reviews/FT-1/2.md", "/w/.f10", &FindingsWritten{Path: "/w/.f10/reviews/FT-1/2.md", Task: "FT-1", Round: "2"}},
		{"in-repo verdict", "/w/.f10/verdicts/FT-1/1.md", "/w/.f10", &VerdictWritten{Path: "/w/.f10/verdicts/FT-1/1.md", Key: "FT-1", N: "1"}},
		{"idea verdict, root from the path", "/w/.f10/verdicts/rate-limit/2.md", "", &VerdictWritten{Path: "/w/.f10/verdicts/rate-limit/2.md", Key: "rate-limit", N: "2"}},
		{"out-of-tree verdict", "/h/.f10/w/verdicts/FT-1/1.md", "/h/.f10/w", &VerdictWritten{Path: "/h/.f10/w/verdicts/FT-1/1.md", Key: "FT-1", N: "1"}},
		{"verdict too shallow", "/w/.f10/verdicts/FT-1.md", "/w/.f10", nil},
		{"out-of-tree plan", "/h/.f10/w/plans/FT-1.md", "/h/.f10/w", &PlanWritten{Path: "/h/.f10/w/plans/FT-1.md", Task: "FT-1"}},
		{"out-of-tree without its root", "/h/.f10/w/plans/FT-1.md", "", nil},
		{"archive", "/w/.f10/plans/archive/FT-1.1.md", "/w/.f10", nil},
		{"instructions", "/w/.f10/instructions/project.md", "/w/.f10", nil},
		{"not markdown", "/w/.f10/plans/FT-1.txt", "/w/.f10", nil},
		{"elsewhere", "/w/docs/plans/FT-1.md", "/w/.f10", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromWrite(tc.path, tc.root); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("FromWrite = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFromMovesNeedsTheArchivedFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj", ".f10")
	archive := filepath.Join(root, "plans", "archive")

	if err := os.MkdirAll(archive, 0o750); err != nil {
		t.Fatal(err)
	}

	cmd := "mkdir -p " + archive + " && mv " + filepath.Join(
		root,
		"plans",
		"FT-3.md",
	) + " " + filepath.Join(
		archive,
		"FT-3.1.md",
	)

	if got := FromMoves(cmd, "", ""); len(got) != 0 {
		t.Errorf("a move that left nothing counted: %+v", got)
	}

	if err := os.WriteFile(filepath.Join(archive, "FT-3.1.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got := FromMoves(cmd, "", "")
	want := []Observation{&PlanArchived{
		From: filepath.Join(root, "plans", "FT-3.md"), To: filepath.Join(archive, "FT-3.1.md"), Task: "FT-3",
	}}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	// relative, into the directory, with an out-of-tree root
	oot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(oot, "plans", "archive"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(oot, "plans", "archive", "FT-4.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := FromMoves(
		"mv -f plans/FT-4.md plans/archive/",
		oot,
		oot,
	); len(got) != 1 ||
		got[0].(*PlanArchived).Task != "FT-4" {
		t.Errorf("relative move: %+v", got)
	}

	if got := FromMoves("mv notes.md elsewhere/", oot, oot); len(got) != 0 {
		t.Errorf("unrelated move: %+v", got)
	}
}
