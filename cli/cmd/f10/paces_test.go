package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// paceRepo is a checkout whose project.md declares frontmatter, with the
// user's ~/.f10/paces.md written when user is not "".
func paceRepo(t *testing.T, frontmatter, user string) string {
	t.Helper()

	home := isolateEnv(t)
	t.Setenv(userDirEnv, filepath.Join(home, ".f10"))

	repo := mkRepo(t)
	path := filepath.Join(repo, ".f10", "instructions", "project.md")

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("---\n"+frontmatter+"---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if user != "" {
		if err := os.MkdirAll(filepath.Join(home, ".f10"), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(
			filepath.Join(home, ".f10", pacesConfigFile),
			[]byte("---\n"+user+"---\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}

	return repo
}

func runErrIn(dir string, args ...string) error {
	app := newApp()
	app.Writer, app.ErrWriter = &bytes.Buffer{}, &bytes.Buffer{}

	return app.Run(context.Background(), append([]string{"f10", "-C", dir}, args...))
}

// f10 flow <name> is the list ship runs: skipped entries marked, each dial
// with its origin, and the pace's finish where the run ends at a PR.
func TestFlowUnderAPace(t *testing.T) {
	repo := paceRepo(t, "flows:\n  default: plan judge implement cleanup pr review:local judge pr\n", "")

	out := runIn(t, repo, "flow", "default", "--pace", "ultra")
	for _, want := range []string{
		"pace: ultra (--pace)",
		"plan          skipped: off in pace ultra (built-in)",
		"review:local  by local (entry argument), depth light (pace ultra (built-in))",
		"pr            marker fast (pace ultra (built-in))",
		"finish        on (pace ultra (built-in))",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}

	// no pace: the flow as declared, an entry per line
	if out := runIn(t, repo, "flow", "default"); out != strings.ReplaceAll(
		"plan judge implement cleanup pr review:local judge pr", " ", "\n")+"\n" {
		t.Errorf("no pace:\n%s", out)
	}

	err := runErrIn(repo, "flow", "--pace", "brisk")
	if err == nil || !strings.Contains(err.Error(), "the paces are fast, medium, slow, ultra") {
		t.Errorf("an unknown pace = %v", err)
	}
}

// The user's paces lie under the project's, key by key, and the project's
// pace: names the default a run takes.
func TestPaceLayers(t *testing.T) {
	repo := paceRepo(t,
		"flows:\n  default: plan judge implement pr\npaces:\n  brisk: { judge: { effort: high } }\npace: brisk\n",
		"paces:\n  brisk: { judge: off, review: { rounds: 1 } }\npace: fast\n")

	out := runIn(t, repo, "flow", "default")
	if !strings.Contains(out, "pace: brisk (declared (main, frontmatter))") ||
		!strings.Contains(out, "judge      effort high (pace brisk (declared (main, frontmatter)))") {
		t.Errorf("the project's brisk over the user's:\n%s", out)
	}

	cfg := runIn(t, repo, "config")
	for _, want := range []string{"paces.brisk.judge", "paces.brisk.review", "paces.medium", ".f10/paces.md"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config misses %q:\n%s", want, cfg)
		}
	}

	if got := runIn(t, repo, "agents", "judge"); !strings.Contains(got, "effort high") ||
		!strings.Contains(got, "pace brisk") {
		t.Errorf("agents judge = %q", got)
	}
}

// A user file the vocabulary refuses fails the run, naming the file.
func TestPaceUserFileRefused(t *testing.T) {
	repo := paceRepo(t, "flows:\n  default: plan implement pr\n", "paces:\n  bad: { implement: off }\n")

	if err := runErrIn(repo, "flow"); err == nil || !strings.Contains(err.Error(), "paces.md") ||
		!strings.Contains(err.Error(), "implement cannot be off") {
		t.Errorf("err = %v", err)
	}
}

// A pace that skips plan drops /f10:plan from drive's chain, and its
// finish word beats drive_finish.
func TestPaceShapesTheDriveChain(t *testing.T) {
	eff := &facts.Effective{Flows: map[string][]string{"default": strings.Fields("plan judge implement pr")}}
	set := facts.NewPaceSet()

	for pace, want := range map[string]string{
		"":      "plan ship",
		"fast":  "plan ship finish",
		"slow":  "plan ship",
		"ultra": "ship finish",
	} {
		fl, err := resolvePacedFlow(eff, set, pace, "")
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(driveChain(fl, false), " "); got != want {
			t.Errorf("%q: chain %q, want %q", pace, got, want)
		}
	}

	slow, _ := resolvePacedFlow(eff, set, "slow", "")
	if got := strings.Join(driveChain(slow, true), " "); got != "plan ship" {
		t.Errorf("slow's finish off under drive_finish on = %q", got)
	}
}

// A rerun of drive keeps the pace a branch records; a run's --pace on a
// branch with none is recorded there.
func TestDriveKeepsTheRecordedPace(t *testing.T) {
	paced := func(fl shipFlow, pace string) (shipFlow, error) {
		fl.pace = pace

		return fl, nil
	}

	for _, tc := range []struct {
		recorded, flag, run, want, note string
		writes                          bool
	}{
		{recorded: "", flag: "fast", run: "fast", want: "fast", writes: true},
		{recorded: "", run: "slow", want: "slow"},
		{recorded: "fast", run: "fast", want: "fast"},
		{recorded: "ultra", flag: "fast", run: "fast", want: "ultra", note: "kept the ultra pace"},
	} {
		f := newFakes(t)
		f.script("git config --get branch.GH-1."+cfgPace, tc.recorded)

		in := &driveInput{main: "/code/repo", args: driveArgs{pace: tc.flag}, paced: paced}

		fl, note, err := in.keepPace(t.Context(), "GH-1", shipFlow{pace: tc.run})
		if err != nil || fl.pace != tc.want || !strings.Contains(note, tc.note) ||
			f.called("git config branch.GH-1."+cfgPace+" "+tc.flag) != tc.writes {
			t.Errorf("recorded %q: pace %q, note %q, err %v, calls %v", tc.recorded, fl.pace, note, err, f.calls)
		}
	}
}

func TestDriveTakesPace(t *testing.T) {
	a, err := parseDriveArgs([]string{"GH-1", "--pace", "Fast"})
	if err != nil || a.pace != "fast" {
		t.Errorf("--pace = %+v, %v", a, err)
	}

	if _, err := parseDriveArgs([]string{"GH-1", "--pace", "fast", "--pace=slow"}); err == nil {
		t.Error("two paces were taken")
	}
}
