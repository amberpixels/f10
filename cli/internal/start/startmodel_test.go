package start

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/storage"
)

func TestAgentArgs(t *testing.T) {
	dirs := func(root storage.Root) string {
		var out []string
		for _, d := range root.TaskFileDirs() {
			out = append(out, "--add-dir "+d)
		}

		return strings.Join(out, " ")
	}

	main, oot := storage.InRepo("/code/repo"), storage.OutOfTree("/home/me", "repo")

	cases := []struct {
		name     string
		root     storage.Root
		worktree string
		profile  agentctl.Profile
		want     string
	}{
		{name: "worktree beside main", root: main, worktree: "/code/repo.GH-1", want: dirs(main)},
		{
			name: "with a model", profile: agentctl.Profile{Model: "opus"},
			root: main, worktree: "/code/repo.GH-1",
			want: "--model opus " + dirs(main),
		},
		{
			name: "with a profile", profile: agentctl.Profile{Kind: "claude", Model: "opus", Effort: "low"},
			root: main, worktree: "/code/repo.GH-1",
			want: "--model opus --effort low " + dirs(main),
		},
		{name: "out of tree", root: oot, worktree: "/code/repo.GH-1", want: dirs(oot)},
		{name: "main itself", root: main, worktree: "/code/repo"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(AgentArgs(c.profile, c.root, c.worktree), " "); got != c.want {
				t.Errorf("agentArgs = %q, want %q", got, c.want)
			}

			if strings.Contains(strings.Join(AgentArgs(c.profile, c.root, c.worktree), " "), "instructions") {
				t.Error("the instructions dir was added")
			}
		})
	}
}

// A worktree reached through a symlink holds the storage root inside it, so
// nothing is added.
func TestAgentArgsResolvesSymlinks(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if got := AgentArgs(agentctl.Profile{}, storage.InRepo(target), link); len(got) != 0 {
		t.Errorf("agentArgs = %q, want nothing added", got)
	}
}

// demo writes <storage>/demo/<id>/report.html, so a worktree agent needs it
// added or every demo asks for permission.
func TestAgentArgsAddsDemo(t *testing.T) {
	got := strings.Join(AgentArgs(agentctl.Profile{}, storage.InRepo("/code/repo"), "/code/repo.GH-1"), " ")
	if !strings.Contains(got, "--add-dir /code/repo/.f10/demo") {
		t.Fatalf("agentArgs = %q, want the demo dir added", got)
	}
}

// A drive's agent is marked ahead of the storage grants, because --add-dir
// takes every argument after it. Any other launch carries no mark.
func TestLaunchArgsMarksADrive(t *testing.T) {
	root := storage.InRepo("/code/repo")

	got := LaunchArgs(agentctl.Profile{}, root, "/code/repo.GH-1", true)
	if len(got) < 2 || got[0] != "--settings" || !strings.Contains(got[1], `"F10_DRIVEN":"1"`) {
		t.Errorf("driven = %q, want the --settings mark first", got)
	}

	plain := LaunchArgs(agentctl.Profile{}, root, "/code/repo.GH-1", false)
	if strings.Contains(strings.Join(plain, " "), "--settings") {
		t.Errorf("undriven = %q, want no mark", plain)
	}
}
