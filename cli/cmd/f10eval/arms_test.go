package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

func TestLoadScenariosChecksArms(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		body string
		err  string
	}{
		"raw alone": {`{"prompt":"p","arms":{"raw":{"prompt":"r"}}}`, ""},
		"native with modes": {
			`{"prompt":"p","arms":{"raw":{"prompt":"r"},"native":{"prompts":["a","b"],"permission_modes":["plan",""]}}}`, "",
		},
		"no raw":         {`{"prompt":"p","arms":{"native":{"prompt":"n"}}}`, "no raw arm"},
		"both forms":     {`{"prompt":"p","arms":{"raw":{"prompt":"r","prompts":["a"]}}}`, "raw: both prompt and prompts"},
		"no prompt":      {`{"prompt":"p","arms":{"raw":{}}}`, "raw: no prompt"},
		"modes too few":  {`{"prompt":"p","arms":{"raw":{"prompts":["a","b"],"permission_modes":["plan"]}}}`, "1 permission modes for 2 turns"},
		"no such mode":   {`{"prompt":"p","arms":{"raw":{"prompt":"r","permission_modes":["default"]}}}`, `"default" is none of`},
		"modes on raw":   {`{"prompt":"p","arms":{"raw":{"prompt":"r","permission_modes":["acceptEdits"]}}}`, ""},
		"native no form": {`{"prompt":"p","arms":{"raw":{"prompt":"r"},"native":{}}}`, "native: no prompt"},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "s"), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "s", "scenario.json"), []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := loadScenarios(dir, "")
		if (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %v, want %q", name, err, c.err)
		}
	}
}

// A scenario with no arms field is refused before f10 is built or claude
// is called, so the refusal costs nothing.
//
//nolint:paralleltest // fakeClaude sets PATH
func TestArmsRefusesAScenarioWithoutArms(t *testing.T) {
	log := fakeClaude(t, "")
	root := turnsRoot(t)

	if err := os.WriteFile(filepath.Join(root, "evals", "scenarios", "s", "scenario.json"),
		[]byte(`{"prompt":"p"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := armsMain(t.Context(), &bytes.Buffer{}, root, []string{"-scenario", "s"})
	if err == nil || !strings.Contains(err.Error(), "s has no arms field") {
		t.Errorf("err = %v, want the scenario refused by name", err)
	}

	if _, err := os.Stat(log); err == nil {
		t.Error("claude was called for a refused scenario")
	}

	if err := armsMain(t.Context(), &bytes.Buffer{}, root, nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("no -scenario: %v, want the usage", err)
	}
}

func TestPlanRunsOrdersArms(t *testing.T) {
	t.Parallel()

	both := Scenario{Name: "s", Agents: []string{"opus:high", "opus:medium"}, Arms: &Arms{Raw: &Arm{}, Native: &Arm{}}}
	rawOnly := Scenario{Name: "s", Arms: &Arms{Raw: &Arm{}}}

	names := func(plan []planned) []string {
		var out []string
		for _, p := range plan {
			out = append(out, filepath.Base(p.path)+"="+p.s.arm)
		}

		return out
	}

	plan, err := planRuns([]Scenario{both}, options{repeat: 3}, "/out", "f10", armOrder, armProfile)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"s.raw.opus-high.1.json=raw", "s.raw.opus-high.2.json=raw", "s.raw.opus-high.3.json=raw",
		"s.native.opus-high.1.json=native", "s.native.opus-high.2.json=native", "s.native.opus-high.3.json=native",
		"s.f10.opus-high.1.json=f10", "s.f10.opus-high.2.json=f10", "s.f10.opus-high.3.json=f10",
	}
	if got := names(plan); !slices.Equal(got, want) {
		t.Errorf("plan %q, want %q", got, want)
	}

	if plan[0].display.name() != "raw · s #1" {
		t.Errorf("display %q, want the arm before the scenario", plan[0].display.name())
	}

	plan, err = planRuns([]Scenario{rawOnly}, options{repeat: 1}, "/out", "f10", armOrder, armProfile)
	if err != nil {
		t.Fatal(err)
	}

	if got := names(plan); !slices.Equal(got, []string{"s.raw.json=raw", "s.f10.json=f10"}) {
		t.Errorf("no native arm, -repeat 1: %q", got)
	}

	plan, err = planRuns([]Scenario{both}, options{repeat: 1}, "/out", "f10", []string{""}, profilesFor)
	if err != nil {
		t.Fatal(err)
	}

	if got := names(plan); !slices.Equal(got, []string{"s.opus-high.json=", "s.opus-medium.json="}) {
		t.Errorf("a plain eval runs every profile with no arm: %q", got)
	}
}

// A native arm plans under plan mode and implements under the run's mode
// on the resumed session, with no plugin and the isolation flags.
//
//nolint:paralleltest // fakeClaude sets PATH
func TestNativeArmTurns(t *testing.T) {
	log := fakeClaude(t, "")
	root := turnsRoot(t)

	s := Scenario{
		Name:   "s",
		Task:   "EV-1",
		Prompt: "/f10:ship EV-1",
		arm:    armNative,
		Arms: &Arms{
			Raw:    &Arm{Prompt: "r"},
			Native: &Arm{Prompts: []string{"plan it", "build it"}, PermissionModes: []string{"plan", ""}},
		},
	}

	def, _, _ := agentctl.Profile{}.Resolve()
	rep := runScenario(t.Context(), root, s, options{permission: "acceptEdits", budget: "1"}, def, runOut{})

	if rep.Error != "" || rep.Arm != armNative {
		t.Fatalf("report %+v", rep)
	}

	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	calls := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(calls) != 2 {
		t.Fatalf("calls %q, want two turns", calls)
	}

	for i, want := range []string{"--permission-mode plan ", "--permission-mode acceptEdits "} {
		c := calls[i]
		if !strings.Contains(c, want) || strings.Contains(c, "--plugin-dir") ||
			!strings.Contains(c, "--setting-sources project,local --strict-mcp-config") ||
			!strings.Contains(c, `"plansDirectory"`) {
			t.Errorf("turn %d: %q, want %q, no plugin and the isolation flags", i+1, c, want)
		}
	}

	if !strings.HasSuffix(calls[0], "plan it") || !strings.Contains(calls[1], "--resume sess-1 build it") {
		t.Errorf("calls %q, want the arm's prompts, the second resuming the first", calls)
	}

	s.arm = armF10
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}

	runScenario(t.Context(), root, s, options{permission: "acceptEdits", budget: "1"}, def, runOut{})

	body, _ = os.ReadFile(log)
	if !strings.Contains(string(body), "--plugin-dir "+root) ||
		!strings.HasSuffix(strings.TrimSpace(string(body)), "/f10:ship EV-1") {
		t.Errorf("the f10 arm runs the scenario's prompt with the plugin: %q", body)
	}
}

func TestPlainFixture(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "repo")

	if err := prepare(
		ctx,
		filepath.Join("..", "..", "..", "evals", "scenarios", "feature-small", "repo"),
		dir,
		true,
	); err != nil {
		t.Fatal(err)
	}

	for _, gone := range []string{"f10", ".f10"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("a plain fixture holds %s/", gone)
		}
	}

	if files := gitOut(ctx, dir, "ls-files"); !strings.Contains(files, "CLAUDE.md") {
		t.Errorf("CLAUDE.md is not committed: %q", files)
	}

	body, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}

	md := string(body)
	if !strings.HasPrefix(md, "# greet") || !strings.Contains(md, "## Verify") || !strings.Contains(md, "just test") ||
		strings.Contains(md, "## Tracker") || strings.Contains(md, "driver") || strings.Contains(md, "visibility:") {
		t.Errorf("CLAUDE.md keeps the project and verify sections only:\n%s", md)
	}
}

func TestClaudeMD(t *testing.T) {
	t.Parallel()

	in := "---\nvisibility: stealth\n---\n# app\n\n## Project\n\nGo.\n\n## Tracker\n\nthe driver\n\n" +
		"## Hosting & PR\n\ngh\n\n## Verify\n\n`just test`\n"
	want := "# app\n\n## Project\n\nGo.\n\n## Verify\n\n`just test`\n"

	if got := claudeMD(in); got != want {
		t.Errorf("claudeMD = %q, want %q", got, want)
	}

	if got := claudeMD("# bare\n"); got != "# bare\n" {
		t.Errorf("no frontmatter: %q", got)
	}
}

// A plain arm runs only the outcome checks, and its findings come from the
// file its prompt names.
func TestPlainArmChecks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(strings.Repeat("x\n", 20)), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(dir, plainFindings),
		[]byte("### leak\nwhere: main.go:3\nbad\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	s := Scenario{
		Task: "T-1", arm: armRaw, Checks: []string{"plan", "planted", "lore", "findings"},
		Planted: []Planted{{File: "main.go", From: 1, To: 5}},
	}

	list, pass := runChecks(t.Context(), dir, s)

	var names []string
	for _, c := range list {
		names = append(names, c.Name)
	}

	if !slices.Equal(names, []string{"planted", "findings"}) || !pass {
		t.Errorf("checks %+v, want planted and findings passing from %s", list, plainFindings)
	}
}

func TestIsolationLeaks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	init := event{Type: "system", Subtype: "init"}
	init.Plugins = append(init.Plugins,
		struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}{"f10", root},
		struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}{"cc-plugin-telemetry", "builtin"},
	)

	f10 := &isolationLeaks{plugin: root}
	f10.note(init)

	if s := f10.String(); s != "" {
		t.Errorf("the f10 arm's own plugin leaked: %q", s)
	}

	plain := &isolationLeaks{plugin: root, plain: true}
	plain.note(init)
	plain.note(event{Type: "assistant"})

	init.MCPServers = append(init.MCPServers, struct {
		Name string `json:"name"`
	}{"gmail"})
	plain.note(init)

	if s := plain.String(); s != "user plugins loaded: f10; MCP servers loaded: gmail" {
		t.Errorf("plain arm leaks %q", s)
	}
}

func armRun(arm string, pass bool, cost float64, turns int) Report {
	return Report{
		Scenario: "s", Arm: arm, Pass: pass, CostUSD: cost, DurationMS: 100000, Turns: turns,
		Plumbing: &Plumbing{Calls: 1, TotalCalls: 10, Seconds: 20, RunSeconds: 100},
		Checks:   []Check{{Name: "verify", Pass: pass}},
	}
}

func TestArmsTable(t *testing.T) {
	t.Parallel()

	var runs []Report

	for range 3 {
		raw := armRun(armRaw, true, 1, 10)
		f10 := armRun(armF10, true, 3, 30)
		f10.Checks = append(f10.Checks, Check{Name: "plan", Pass: true})
		f10.Isolation = "user plugins loaded: x"
		runs = append(runs, raw, f10)
	}

	var b bytes.Buffer
	writeArmsTable(&b, &styles{}, runs)
	out := b.String()

	for _, want := range []struct{ row, text string }{
		{"check verify", "3/3"},
		{"check plan", "f10 only"},
		{"cost", "$1.00 [$1.00-$1.00] n=3 better"},
		{"turns", "10 [10-10] n=3 better"},
		{"tool calls", "10 [10-10] n=3"},
		{"plumbing share", "f10 only"},
	} {
		if r := row(out, want.row); !strings.Contains(r, want.text) {
			t.Errorf("row %q = %q, want %q in it\n%s", want.row, r, want.text, out)
		}
	}

	if r := row(out, "plumbing share"); !strings.HasSuffix(r, "20% [20%-20%] n=3") {
		t.Errorf("plumbing share on f10 = %q", r)
	}

	if strings.Contains(out, "native") || strings.Contains(out, "plumbing calls") {
		t.Errorf("a column with no runs or a folded plumbing row printed:\n%s", out)
	}

	if strings.Count(out, "warning: not isolated: user plugins loaded: x") != 1 {
		t.Errorf("the isolation line is not printed once:\n%s", out)
	}
}

// An f10 run that fails only an f10-only check still passes on outcomes,
// so the pass row compares like with like.
func TestArmsTablePassCountsOutcomes(t *testing.T) {
	t.Parallel()

	var runs []Report

	for range 3 {
		f10 := armRun(armF10, false, 3, 30)
		f10.Checks = []Check{{Name: "verify", Pass: true}, {Name: "lore", Pass: false}}
		runs = append(runs, armRun(armRaw, true, 1, 10), f10)
	}

	var b bytes.Buffer
	writeArmsTable(&b, &styles{}, runs)

	if r := strings.Fields(row(b.String(), "pass")); !slices.Equal(r, []string{"pass", "3/3", "3/3"}) {
		t.Errorf("pass row %q, want 3/3 on both arms and no mark", r)
	}
}

func TestCompareRefusesAnArmsSet(t *testing.T) {
	t.Parallel()

	arms := writeSet(t, armRun(armRaw, true, 1, 1))
	plain := writeSet(t, planted(4, 0, 1))

	err := compareMain(&bytes.Buffer{}, "/", []string{plain, arms})
	if err == nil || !strings.Contains(err.Error(), "just eval arms") {
		t.Errorf("err = %v, want the arms set refused", err)
	}
}

// The turns a run's result events count are summed into its report.
//
//nolint:paralleltest // fakeClaude sets PATH
func TestRunCountsTurns(t *testing.T) {
	fakeClaude(t, "")
	root := turnsRoot(t)

	// the fake claude prints no num_turns, so wrap it with one that does
	bin := t.TempDir()
	fake, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}

	script := "#!/bin/sh\n\"" + fake + "\" \"$@\" | sed 's/\"type\":\"result\"/\"type\":\"result\",\"num_turns\":4/'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := Scenario{Name: "s", Task: "EV-1", Prompts: []string{"one", "two"}}
	def, _, _ := agentctl.Profile{}.Resolve()

	rep := runScenario(context.Background(), root, s, options{permission: "acceptEdits", budget: "1"}, def, runOut{})
	if rep.Turns != 8 {
		t.Errorf("turns = %d, want 4 per prompt summed", rep.Turns)
	}
}
