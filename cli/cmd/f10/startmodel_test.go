package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

func TestClaudeDefaultModel(t *testing.T) {
	dir := t.TempDir()

	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}

		return p
	}

	cases := []struct {
		name, path, want string
	}{
		{"key", write("key.json", `{"model": "opus", "theme": "dark"}`), "opus"},
		{"no key", write("nokey.json", `{"theme": "dark"}`), ""},
		{"not json", write("bad.json", `model: opus`), ""},
		{"no file", filepath.Join(dir, "missing.json"), ""},
		{"no path", "", ""},
	}

	for _, c := range cases {
		if got := claudeDefaultModel(c.path); got != c.want {
			t.Errorf("%s: claudeDefaultModel = %q, want %q", c.name, got, c.want)
		}
	}
}

// The flag's profile resolves over nothing: a family passes, a near miss is
// corrected with a note, and a name no family matches falls back to the
// kind's default with the warning naming claude's own.
func TestFlagProfileLines(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"model": "opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		raw, settings, profile, line string
	}{
		{raw: "", settings: settings, profile: "claude::"},
		{raw: "fable", settings: settings, profile: "claude:fable:"},
		{raw: "fabel:low", settings: settings, profile: "claude:fable:low", line: "--model fabel read as fable"},
		{
			raw:      "xyz:high",
			settings: settings,
			profile:  "claude::high",
			line:     "!!! --model xyz is not a model name f10 recognizes: the agent starts on opus (your claude default) instead",
		},
		{
			raw:     "xyz",
			profile: "claude::",
			line:    "!!! --model xyz is not a model name f10 recognizes: the agent starts on claude's default model instead",
		},
	}

	for _, c := range cases {
		r := agentMap{}.with(c.raw, agentFlagOrigin).profileFor("default", "")
		line := strings.Join(r.lines(c.settings), "\n")

		if r.Profile.String() != c.profile || !strings.Contains(line, c.line) || (c.line == "" && line != "") {
			t.Errorf("%q resolves to %q, %q; want %q, %q", c.raw, r.Profile, line, c.profile, c.line)
		}
	}
}

// A step's profile is default, then the step's key, then the step scoped
// by kind, each segment from the last layer that named it.
func TestProfileForLayersKeys(t *testing.T) {
	m := agentMap{
		"default":  {Key: "default", Value: "opus:high", Origin: "user"},
		"review":   {Key: "review", Value: "sonnet", Origin: "project"},
		"plan.bug": {Key: "plan.bug", Value: "medium", Origin: "project"},
		"cleanup":  {Key: "cleanup", Value: "xyz:low", Origin: "project"},
	}

	cases := []struct {
		step, kind, profile, model, effort string
	}{
		{step: "default", profile: "claude:opus:high", model: "default (user)", effort: "default (user)"},
		{step: "implement", profile: "claude:opus:high", model: "default (user)", effort: "default (user)"},
		{step: "review", profile: "claude:sonnet:high", model: "review (project)", effort: "default (user)"},
		{
			step: "plan", kind: "bug", profile: "claude:opus:medium",
			model: "default (user)", effort: "plan.bug (project)",
		},
		{
			step: "plan", kind: "feature", profile: "claude:opus:high",
			model: "default (user)", effort: "default (user)",
		},
		{step: "cleanup", profile: "claude::low", effort: "cleanup (project)"},
	}

	for _, c := range cases {
		r := m.profileFor(c.step, c.kind)
		if r.Profile.String() != c.profile || r.Sources["model"] != c.model || r.Sources["effort"] != c.effort {
			t.Errorf("%s.%s = %q from %v; want %q, model %q, effort %q",
				c.step, c.kind, r.Profile, r.Sources, c.profile, c.model, c.effort)
		}
	}

	if r := m.profileFor("cleanup", ""); r.Fell != "xyz" {
		t.Errorf("a model no family matches is reported as fallen: %+v", r)
	}

	if r := (agentMap{}).profileFor("review", ""); !r.Profile.IsDefault() || r.Profile.String() != "claude::" {
		t.Errorf("no map resolves to the kind's default: %+v", r)
	}
}

func TestProfileFlag(t *testing.T) {
	cases := []struct {
		agent, model, want, err string
	}{
		{},
		{agent: "opus:high", want: "opus:high"},
		{model: "opus", want: "opus"},
		{model: "opus-5.5", want: "opus-5.5"},
		{agent: "opus", model: "fable", err: "give one"},
		{model: "high", err: "names an effort"},
		{model: "opus:high", err: "a model alone"},
	}

	for _, c := range cases {
		got, err := profileFlag(c.agent, c.model)

		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("profileFlag(%q, %q) err = %v, want %q", c.agent, c.model, err, c.err)
			}
		case err != nil:
			t.Errorf("profileFlag(%q, %q): %v", c.agent, c.model, err)
		case got != c.want:
			t.Errorf("profileFlag(%q, %q) = %q, want %q", c.agent, c.model, got, c.want)
		}
	}
}

func TestAgentArgs(t *testing.T) {
	dirs := func(root string) string {
		var out []string
		for _, d := range taskFileDirs {
			out = append(out, "--add-dir "+root+"/"+d)
		}

		return strings.Join(out, " ")
	}

	cases := []struct {
		name, plansDir, worktree string
		profile                  agentctl.Profile
		want                     string
	}{
		{
			name: "worktree beside main", plansDir: "/code/repo/.f10/plans", worktree: "/code/repo.GH-1",
			want: dirs("/code/repo/.f10"),
		},
		{
			name: "with a model", profile: agentctl.Profile{Model: "opus"},
			plansDir: "/code/repo/.f10/plans", worktree: "/code/repo.GH-1",
			want: "--model opus " + dirs("/code/repo/.f10"),
		},
		{
			name: "with a profile", profile: agentctl.Profile{Kind: "claude", Model: "opus", Effort: "low"},
			plansDir: "/code/repo/.f10/plans", worktree: "/code/repo.GH-1",
			want: "--model opus --effort low " + dirs("/code/repo/.f10"),
		},
		{
			name: "out of tree", plansDir: "/home/me/.f10/repo/plans", worktree: "/code/repo.GH-1",
			want: dirs("/home/me/.f10/repo"),
		},
		{name: "main itself", plansDir: "/code/repo/.f10/plans", worktree: "/code/repo"},
		{name: "no shared plans dir", worktree: "/code/repo.GH-1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(agentArgs(c.profile, c.plansDir, c.worktree), " "); got != c.want {
				t.Errorf("agentArgs = %q, want %q", got, c.want)
			}

			if strings.Contains(strings.Join(agentArgs(c.profile, c.plansDir, c.worktree), " "), "instructions") {
				t.Error("the instructions dir was added")
			}
		})
	}
}

// A run's flag lays over the map's default segment by segment, so a lone
// effort keeps the map's model, and a step's own key still wins over it.
func TestFlagOverlaysTheMapsDefault(t *testing.T) {
	m := agentMap{
		"default": {Key: "default", Value: "opus:high", Origin: "user"},
		"review":  {Key: "review", Value: "sonnet", Origin: "project"},
	}

	cases := []struct {
		flag, step, want, model, effort string
	}{
		{
			flag: "low", step: "default", want: "claude:opus:low",
			model: "default (user)", effort: agentKeyFlag + " (--agent)",
		},
		{
			flag: "sonnet", step: "implement", want: "claude:sonnet:high",
			model: agentKeyFlag + " (--agent)", effort: "default (user)",
		},
		{
			flag: "haiku:max", step: "review", want: "claude:sonnet:max",
			model: "review (project)", effort: agentKeyFlag + " (--agent)",
		},
	}

	for _, c := range cases {
		r := m.with(c.flag, agentFlagOrigin).profileFor(c.step, "")
		if r.Profile.String() != c.want || r.Sources["model"] != c.model || r.Sources["effort"] != c.effort {
			t.Errorf("--agent %s for %s = %q from %v; want %q, model %q, effort %q",
				c.flag, c.step, r.Profile, r.Sources, c.want, c.model, c.effort)
		}
	}

	// the last profile laid on wins whole: a task's own suffix over the run's flag
	own := m.with("low", agentFlagOrigin).with("haiku", "<id>:haiku")
	if r := own.profileFor("default", ""); r.Profile.String() != "claude:haiku:high" {
		t.Errorf("a task's own profile over the flag: %q", r.Profile)
	}
}

// The branch's record lays under a spawning step's line when no flag is
// given, read from f10-agent, else the model recorded before profiles.
func TestWithBranchReadsTheRecord(t *testing.T) {
	f := newFakes(t)
	f.script("git config --get branch.GH-1/slug."+cfgAgent, "claude:sonnet:low")
	f.script("git config --get branch.GH-2/slug."+cfgModel, "opus")

	m := agentMap{"default": {Key: "default", Value: "opus:high", Origin: "user"}}

	got, err := m.withBranch(t.Context(), "/code/repo", "GH-1/slug")
	if err != nil {
		t.Fatal(err)
	}

	if r := got.profileFor("review", ""); r.Profile.String() != "claude:sonnet:low" ||
		r.Sources["model"] != agentKeyFlag+" (recorded on branch GH-1/slug)" {
		t.Errorf("review on a recorded profile = %q from %v", r.Profile, r.Sources)
	}

	if got, err = m.withBranch(t.Context(), "/code/repo", "GH-2/slug"); err != nil {
		t.Fatal(err)
	}

	if r := got.profileFor("review", ""); r.Profile.String() != "claude:opus:high" {
		t.Errorf("an old model record keeps the map's effort: %q", r.Profile)
	}

	if got, err = m.withBranch(t.Context(), "/code/repo", ""); err != nil || len(got) != 1 {
		t.Errorf("no branch leaves the map: %v %v", got, err)
	}
}
