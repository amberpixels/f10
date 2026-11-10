package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
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
			filepath.Join(home, ".f10", facts.UserPacesFile),
			[]byte("---\n"+user+"---\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}

	return repo
}

// With no pace, f10 flow <name> still settles an optional CI review, and
// says why it was dropped.
func TestFlowSettlesOptionalCI(t *testing.T) {
	repo := paceRepo(t, "flows:\n  default: plan implement pr review:local+ci?\n", "")

	out := runApp(t, "-C", repo, "flow", "default").must(t)
	if want := "review:local  by local (local+ci? (no remote reviewer declared))"; !strings.Contains(out, want) {
		t.Errorf("missing %q:\n%s", want, out)
	}
}

// f10 flow <name> is the list ship runs: skipped entries marked, each dial
// with its origin, and the pace's finish where the run ends at a PR.
func TestFlowUnderAPace(t *testing.T) {
	repo := paceRepo(t, "flows:\n  default: plan judge implement cleanup pr review:local judge pr\n", "")

	out := runApp(t, "-C", repo, "flow", "default", "--pace", "ultra").must(t)
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

	// no pace named: medium, which only finishes, so the flow as declared,
	// an entry per line, then its finish
	want := "pace: medium (default)\n" +
		strings.ReplaceAll("plan judge implement cleanup pr review:local judge pr", " ", "\n") +
		"\nfinish        on (pace medium (built-in))\n"
	if out := runApp(t, "-C", repo, "flow", "default").must(t); out != want {
		t.Errorf("no pace:\n%s", out)
	}

	err := runApp(t, "-C", repo, "flow", "--pace", "brisk").err
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

	// the project's judge key replaces the user's off, so judge runs
	out := runApp(t, "-C", repo, "flow", "default").must(t)
	if !strings.Contains(out, "pace: brisk (declared (main, frontmatter))") ||
		!strings.Contains(out, "\njudge\n") || strings.Contains(out, "skipped") {
		t.Errorf("the project's brisk over the user's:\n%s", out)
	}

	cfg := runApp(t, "-C", repo, "config").must(t)
	for _, want := range []string{"paces.brisk.judge", "paces.brisk.review", "paces.medium", ".f10/paces.md"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config misses %q:\n%s", want, cfg)
		}
	}

	if got := runApp(t, "-C", repo, "agents", "judge").must(t); !strings.Contains(got, "effort high") ||
		!strings.Contains(got, "pace brisk") {
		t.Errorf("agents judge = %q", got)
	}
}

// A user file the vocabulary refuses fails the run, naming the file.
func TestPaceUserFileRefused(t *testing.T) {
	repo := paceRepo(t, "flows:\n  default: plan implement pr\n", "paces:\n  bad: { implement: off }\n")

	if err := runApp(t, "-C", repo, "flow").err; err == nil || !strings.Contains(err.Error(), "paces.md") ||
		!strings.Contains(err.Error(), "implement cannot be off") {
		t.Errorf("err = %v", err)
	}
}

// A pace that skips plan drops /f10:plan from drive's skills, and its
// finish word decides whether they end in finish.
func TestPaceShapesTheDriveSkills(t *testing.T) {
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": flow.Of(strings.Fields("plan judge implement pr"))}}
	set := facts.NewPaceSet()

	for pace, want := range map[string]string{
		"":       "plan ship finish",
		"medium": "plan ship finish",
		"fast":   "plan ship finish",
		"slow":   "plan ship",
		"ultra":  "ship finish",
	} {
		fl, err := pacedFlowIn(eff, set, pace, "")
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(driveSkills(fl), " "); got != want {
			t.Errorf("%q: skills %q, want %q", pace, got, want)
		}
	}
}

// A run's --pace wins and is recorded; a rerun naming none keeps the
// pace its branch records.
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
		{recorded: "fast", flag: "fast", run: "fast", want: "fast"},
		{recorded: "ultra", flag: "slow", run: "slow", want: "slow", writes: true},
		{recorded: "ultra", run: "fast", want: "ultra", note: "kept the ultra pace"},
	} {
		f := newFakes(t)
		f.meta.Seed("GH-1", branchmeta.Pace, tc.recorded)

		in := &driveInput{
			main:  "/code/repo",
			args:  driveArgs{pace: tc.flag},
			paced: paced,
		}

		fl, note, err := in.keepPace(t.Context(), "GH-1", shipFlow{pace: tc.run})
		if err != nil || fl.pace != tc.want || !strings.Contains(note, tc.note) ||
			f.called("set branch.GH-1."+string(branchmeta.Pace)+" "+tc.flag) != tc.writes {
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

// f10 start --pace records the pace on the new branch, and a pace that
// skips plan sends ship alone.
func TestStartRecordsThePace(t *testing.T) {
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": flow.Of(strings.Fields("plan judge implement pr"))}}

	ultra, err := pacedFlowIn(eff, facts.NewPaceSet(), "ultra", "")
	if err != nil {
		t.Fatal(err)
	}

	if got := prompt(modeDefault, ultra, "GH-1", nil, "", nil, ""); strings.Contains(got, "/f10:plan") {
		t.Errorf("ultra's prompt = %q, want ship alone", got)
	}

	f := newFakes(t)
	f.script(refsLocal, "")
	f.script(refsAfter, heads("GH-7/base"))
	f.script(shaAfter, "ccc")
	f.script(headRef, "origin/main")
	f.script(shaMain, "bbb")
	f.script("git merge-base refs/heads/GH-1 refs/heads/GH-7/base", "ccc")

	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("git worktree add -b GH-1 /code/repo.GH-1 GH-7/base", "")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")

	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: testStorage.Plan("GH-7")}
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, ultra, "GH-1", dep, "", nil, ""), "{}")

	var out bytes.Buffer

	in := startInput{main: "/code/repo", storage: testStorage, task: gh1, flow: ultra, pace: "ultra", after: "GH-7"}
	if err := start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	if !f.called("set branch.GH-1." + string(branchmeta.Pace) + " ultra") {
		t.Errorf("the pace was not recorded on the branch: %v", f.calls)
	}
}

// f10 flow shows the ceiling and each capped dial with what the pace had;
// an excepted pace runs uncapped and says why.
func TestFlowUnderACeiling(t *testing.T) {
	repo := paceRepo(t, "flows:\n  default: plan judge implement cleanup pr review:local judge pr\n"+
		"ceiling: { finish: off, judge: { effort: high }, except: [ultra] }\n", "")

	out := runApp(t, "-C", repo, "flow", "default", "--pace", "fast").must(t)
	for _, want := range []string{
		"ceiling: finish off (declared (main, frontmatter)), judge effort high (declared (main, frontmatter))",
		"finish        off (ceiling (declared (main, frontmatter)); pace fast said on)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}

	if out := runApp(t, "-C", repo, "flow", "default", "--pace", "ultra").must(t); !strings.Contains(
		out,
		"ceiling: not applied: pace ultra is in except",
	) ||
		!strings.Contains(out, "finish        on (pace ultra") {
		t.Errorf("ultra:\n%s", out)
	}

	if out := runApp(t, "-C", repo, "flow", "default").must(t); !strings.Contains(out, "finish        off (ceiling") {
		t.Errorf("no pace named:\n%s", out)
	}

	cfg := runApp(t, "-C", repo, "config").must(t)
	for _, want := range []string{"ceiling.finish", "ceiling.judge", "ceiling.except"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config misses %q:\n%s", want, cfg)
		}
	}

	got := runApp(t, "-C", repo, "agents", "judge", "--pace", "fast").must(t)
	if !strings.Contains(got, "effort high") || !strings.Contains(got, "ceiling") ||
		!strings.Contains(got, "pace fast said low") {
		t.Errorf("agents judge = %q", got)
	}

	// an effort reaches the judge through its profile, so the flow never
	// lists one
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, "judge") && strings.Contains(line, "effort") {
			t.Errorf("the flow lists an effort dial: %q", line)
		}
	}
}

// An except naming no pace fails with the list; a ceiling in the user's
// file is refused, naming project.md.
func TestCeilingRefusedWhereItDoesNotBelong(t *testing.T) {
	repo := paceRepo(t, "flows:\n  default: plan implement pr\nceiling: { finish: off, except: [warp] }\n", "")
	if err := runApp(t, "-C", repo, "flow").err; err == nil || !strings.Contains(err.Error(), `no pace "warp"`) {
		t.Errorf("unknown except = %v", err)
	}

	user := paceRepo(t, "flows:\n  default: plan implement pr\n", "ceiling: { finish: off }\n")
	if err := runApp(t, "-C", user, "flow").err; err == nil || !strings.Contains(err.Error(), "project.md") {
		t.Errorf("a user ceiling = %v", err)
	}
}

// A ceiling's finish off keeps finish out of drive's skills under a pace
// that finishes, unless that pace is excepted.
func TestCeilingShapesTheDriveChain(t *testing.T) {
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": flow.Of(strings.Fields("plan judge implement pr"))}}
	set := facts.NewPaceSet()
	set.Ceiling = facts.Pace{Finish: "off"}

	for _, pace := range []string{"medium", "fast"} {
		fl, err := pacedFlowIn(eff, set, pace, "")
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(driveSkills(fl), " "); got != "plan ship" {
			t.Errorf("%s under finish off: %q", pace, got)
		}
	}

	set.Except = []string{"fast"}

	fl, err := pacedFlowIn(eff, set, "fast", "")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(driveSkills(fl), " "); got != "plan ship finish" {
		t.Errorf("excepted fast: %q", got)
	}
}

// pacedFlowIn resolves on a literal Effective, which Build would have given
// a pace set; nil is the built-ins.
func pacedFlowIn(eff *facts.Effective, set *facts.PaceSet, pace, name string) (shipFlow, error) {
	if set == nil {
		set = facts.NewPaceSet()
		set.Remote = eff.Review.Remote()
	}

	eff.PaceSet = set

	return pacedFlow(eff, pace, name)
}
