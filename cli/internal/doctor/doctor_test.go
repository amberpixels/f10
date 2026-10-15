package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// A fixture is a healthy install in temp directories: a plugin root, its
// record in installed_plugins.json, settings enabling it and wiring the
// status line, and the statusline symlink a session start leaves.
type fixture struct {
	t        *testing.T
	home     string
	claude   string
	root     string
	install  map[string]any
	settings map[string]any
	env      map[string]string
	path     map[string]string // LookPath answers
	authCode int
	e        *Env
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{
		t:    t,
		home: t.TempDir(),
		root: t.TempDir(),
		env:  map[string]string{"CLAUDECODE": "1", "HERDR_ENV": "1"},
		path: map[string]string{"herdr": "/usr/local/bin/herdr", "gh": "/usr/local/bin/gh"},
		install: map[string]any{
			"scope": "user", "version": "0.34.0", "lastUpdated": "2020-01-01T00:00:00Z",
		},
		settings: map[string]any{
			"enabledPlugins": map[string]bool{"f10@amberpixels": true},
			"statusLine":     map[string]any{"command": `f10=$("$HOME/.claude/f10/statusline")`, "refreshInterval": 2},
		},
	}
	f.claude = filepath.Join(f.home, ".claude")
	f.install["installPath"] = f.root

	f.write(filepath.Join(f.root, ".claude-plugin", "plugin.json"), `{"name":"f10","version":"0.34.0"}`)
	f.write(filepath.Join(f.root, "hooks", "hooks.json"), `{"hooks":{"SessionStart":[]}}`)

	for _, s := range []string{"f10-state.sh", "f10-load-project.sh"} {
		path := filepath.Join(f.root, "bin", s)
		f.write(path, "#!/bin/sh\n")
		must(t, os.Chmod(path, 0o755))
		f.path[s] = path
	}

	link := filepath.Join(f.home, ".claude", "f10", "statusline")
	must(t, os.MkdirAll(filepath.Dir(link), 0o755))
	must(t, os.Symlink(filepath.Join(f.root, "bin", "f10-state.sh"), link))

	checkout := t.TempDir()
	instr := filepath.Join(checkout, ".f10", "instructions")
	must(t, os.MkdirAll(instr, 0o755))

	f.e = &Env{
		ClaudeDir: f.claude,
		Home:      f.home,
		Layout: &layout.Layout{
			CheckoutRoot: checkout,
			MainRoot:     checkout,
			Layers:       []layout.Layer{{Label: "main", Dir: instr}},
		},
		Probes:  &probe.Probes{CLI: probe.Signal{Value: "gh", Evidence: "well-known host"}, Host: "github.com"},
		Facts:   &facts.Effective{},
		Version: "0.34.0",
		Getenv:  func(k string) string { return f.env[k] },
		LookPath: func(name string) (string, error) {
			if p, ok := f.path[name]; ok {
				return p, nil
			}

			return "", errors.New("not found")
		},
		Run: func(context.Context, string, string, ...string) (shell.Result, error) {
			return shell.Result{Code: f.authCode}, nil
		},
	}

	return f
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) write(path, content string) {
	f.t.Helper()
	must(f.t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(f.t, os.WriteFile(path, []byte(content), 0o644))
}

func (f *fixture) writeJSON(path string, v any) {
	f.t.Helper()

	data, err := json.Marshal(v)
	must(f.t, err)
	f.write(path, string(data))
}

// run writes the fixture's records and returns the named check's result.
func (f *fixture) run(name string) Result {
	f.t.Helper()

	if f.install != nil {
		f.writeJSON(filepath.Join(f.claude, "plugins", "installed_plugins.json"),
			map[string]any{"plugins": map[string]any{"f10@amberpixels": []any{f.install}}})
	}

	f.writeJSON(filepath.Join(f.claude, "settings.json"), f.settings)

	for _, r := range Run(context.Background(), f.e) {
		if r.Name == name {
			return r
		}
	}

	f.t.Fatalf("no %s check in the results", name)

	return Result{}
}

func TestChecks(t *testing.T) {
	cases := []struct {
		check  string
		name   string
		mutate func(f *fixture)
		want   string
		remedy string // a substring of the remedy, for problems
	}{
		{CheckVersion, "same release", nil, StatusOK, ""},
		{CheckVersion, "binary behind", func(f *fixture) { f.e.Version = "0.33.0" }, StatusProblem, "go install"},
		{
			CheckVersion,
			"plugin behind",
			func(f *fixture) { f.e.Version = "0.35.0" },
			StatusProblem,
			"claude plugin update",
		},
		{CheckVersion, "dev build", func(f *fixture) { f.e.Version = "devel abc" }, StatusOK, ""},
		{CheckVersion, "not installed", func(f *fixture) {
			f.install = nil
			delete(f.path, "f10-state.sh")
		}, StatusProblem, "claude plugin install f10@amberpixels"},

		{CheckScripts, "in session, on PATH", nil, StatusOK, ""},
		{CheckScripts, "in session, shadowed", func(f *fixture) {
			f.path["f10-load-project.sh"] = "/elsewhere/f10-load-project.sh"
		}, StatusProblem, "restart"},
		{CheckScripts, "outside a session", func(f *fixture) {
			delete(f.env, "CLAUDECODE")
			delete(f.path, "f10-load-project.sh")
		}, StatusOK, ""},
		{CheckScripts, "not executable", func(f *fixture) {
			must(f.t, os.Chmod(filepath.Join(f.root, "bin", "f10-load-project.sh"), 0o644))
		}, StatusProblem, "claude plugin update"},

		{CheckHooks, "registered", nil, StatusOK, ""},
		{CheckHooks, "not enabled", func(f *fixture) {
			f.settings["enabledPlugins"] = map[string]bool{"f10@amberpixels": false}
		}, StatusProblem, "claude plugin enable"},
		{CheckHooks, "updated after the last session start", func(f *fixture) {
			f.install["lastUpdated"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		}, StatusProblem, "restart Claude Code"},

		{CheckStatusline, "wired", nil, StatusOK, ""},
		{CheckStatusline, "not wired", func(f *fixture) {
			f.settings["statusLine"] = map[string]any{"command": "echo hi"}
		}, StatusProblem, "docs/statusline.md"},
		{CheckStatusline, "dangling link", func(f *fixture) {
			must(f.t, os.Remove(filepath.Join(f.root, "bin", "f10-state.sh")))
		}, StatusProblem, "new Claude Code session"},

		{CheckHerdr, "present", nil, StatusOK, ""},
		{CheckHerdr, "missing", func(f *fixture) { delete(f.path, "herdr") }, StatusProblem, "herdr.dev"},

		{CheckHost, "signed in", nil, StatusOK, ""},
		{
			CheckHost,
			"not signed in",
			func(f *fixture) { f.authCode = 1 },
			StatusProblem,
			"gh auth login --hostname github.com",
		},
		{CheckHost, "cli missing", func(f *fixture) { delete(f.path, "gh") }, StatusProblem, "install gh"},
		{CheckHost, "no remote", func(f *fixture) {
			f.e.Probes.CLI = probe.Signal{Value: "none", Evidence: "no origin remote"}
		}, StatusNotNeeded, ""},

		{CheckProject, "clean", nil, StatusOK, ""},
		{CheckProject, "frontmatter error", func(f *fixture) {
			f.e.Facts.Problems = []facts.Problem{{Error: true, Message: "project.md frontmatter: unknown key"}}
		}, StatusProblem, "project.md"},
		{CheckProject, "outside a project", func(f *fixture) { f.e.Layout.Layers = nil }, StatusNotNeeded, ""},
	}

	for _, c := range cases {
		t.Run(c.check+"/"+c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.mutate != nil {
				c.mutate(f)
			}

			r := f.run(c.check)
			if r.Status != c.want {
				t.Fatalf("status = %q (%s), want %q", r.Status, r.Detail, c.want)
			}

			if c.want == StatusProblem && !strings.Contains(r.Remedy, c.remedy) {
				t.Errorf("remedy = %q, want it to contain %q", r.Remedy, c.remedy)
			}

			if c.want != StatusProblem && r.Remedy != "" {
				t.Errorf("remedy on a %s check: %q", r.Status, r.Remedy)
			}
		})
	}
}

func TestHerdrMissingIsScopedAndDoesNotFail(t *testing.T) {
	f := newFixture(t)
	delete(f.path, "herdr")

	r := f.run(CheckHerdr)
	if strings.Join(r.Scope, ",") != "start,forward,drive,finish" {
		t.Errorf("scope = %v", r.Scope)
	}

	if Failed([]Result{r}) {
		t.Error("a scoped problem failed the run")
	}
}

func TestFailed(t *testing.T) {
	cases := []struct {
		name    string
		results []Result
		want    bool
	}{
		{"all ok", []Result{{Status: StatusOK}}, false},
		{"not needed", []Result{{Status: StatusNotNeeded}}, false},
		{"scoped problem", []Result{{Status: StatusProblem, Scope: []string{"start"}}}, false},
		{"unscoped problem", []Result{{Status: StatusOK}, {Status: StatusProblem, Scope: []string{}}}, true},
	}

	for _, c := range cases {
		if got := Failed(c.results); got != c.want {
			t.Errorf("%s: Failed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResultsMarshalEveryKey(t *testing.T) {
	f := newFixture(t)
	f.run(CheckVersion)

	data, err := json.Marshal(Run(context.Background(), f.e))
	must(t, err)

	var got []map[string]any
	must(t, json.Unmarshal(data, &got))

	if len(got) != 7 {
		t.Fatalf("%d checks, want 7", len(got))
	}

	for _, r := range got {
		for _, key := range []string{"name", "status", "scope", "detail", "remedy"} {
			if _, ok := r[key]; !ok {
				t.Errorf("%v: no %q", r["name"], key)
			}
		}

		if _, ok := r["scope"].([]any); !ok {
			t.Errorf("%v: scope = %v, want an array", r["name"], r["scope"])
		}
	}
}
