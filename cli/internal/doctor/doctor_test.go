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
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
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
	drivers  string                  // what `f10-drivers --version` prints
	herdr    map[string]shell.Result // herdr's answers, by its joined args
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
		herdr: map[string]shell.Result{
			"--version": {Stdout: "herdr 0.9.5\n"},
			"plugin list": {
				Stdout: "1 plugin installed:\n- amberpixels.sidebar-plus (Sidebar Plus) enabled [local:/p]\n",
			},
		},
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

	for _, s := range []string{"f10", "f10-state.sh", "f10-load-project.sh"} {
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
		Run: func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
			if name == f.path["herdr"] {
				res, ok := f.herdr[strings.Join(args, " ")]
				if !ok {
					return shell.Result{}, errors.New("herdr: no answer")
				}

				return res, nil
			}

			if len(args) == 1 && args[0] == "--version" {
				if f.drivers == "" {
					return shell.Result{Code: 2}, nil
				}

				return shell.Result{Stdout: f.drivers + "\n"}, nil
			}

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
	t.Parallel()

	cases := []struct {
		check  string
		name   string
		mutate func(f *fixture)
		want   string
		remedy string // a substring of the remedy, for problems
	}{
		{CheckVersion, "same release", nil, StatusOK, ""},
		{
			CheckVersion,
			"binary behind",
			func(f *fixture) { f.e.Version = "0.33.0" },
			StatusProblem,
			"fetches or builds",
		},
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
		{CheckScripts, "in session, the shim shadowed", func(f *fixture) {
			f.path["f10"] = "/home/me/go/bin/f10"
		}, StatusProblem, "remove /home/me/go/bin/f10"},
		{CheckScripts, "outside a session", func(f *fixture) {
			delete(f.env, "CLAUDECODE")
			delete(f.path, "f10-load-project.sh")
		}, StatusOK, ""},
		{CheckScripts, "not executable", func(f *fixture) {
			must(f.t, os.Chmod(filepath.Join(f.root, "bin", "f10-load-project.sh"), 0o644))
		}, StatusProblem, "claude plugin update"},

		{CheckHooks, "registered", nil, StatusOK, ""},
		{CheckHooks, "not enabled", func(f *fixture) {
			delete(f.env, "CLAUDECODE")
			f.settings["enabledPlugins"] = map[string]bool{"f10@amberpixels": false}
		}, StatusProblem, "claude plugin enable"},
		{CheckHooks, "loaded from the session's PATH, install record disabled", func(f *fixture) {
			f.settings["enabledPlugins"] = map[string]bool{"f10@amberpixels": false}
		}, StatusOK, ""},
		{CheckHooks, "no session started yet", func(f *fixture) {
			must(f.t, os.Remove(filepath.Join(f.home, ".claude", "f10", "statusline")))
		}, StatusProblem, "restart Claude Code"},
		{CheckHooks, "hooks.json missing", func(f *fixture) {
			must(f.t, os.Remove(filepath.Join(f.root, "hooks", "hooks.json")))
		}, StatusProblem, "claude plugin update"},
		{CheckHooks, "this session started before the update", func(f *fixture) {
			// another session started since (the link is new), this one did not
			f.env["CLAUDE_CODE_SESSION_ID"] = "s1"
			marker := filepath.Join(f.home, ".claude", "f10", "state", "s1.started")
			f.write(marker, "")
			old := time.Now().Add(-2 * time.Hour)
			must(f.t, os.Chtimes(marker, old, old))
			f.install["lastUpdated"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		}, StatusProblem, "restart Claude Code"},
		{CheckHooks, "this session started after the update", func(f *fixture) {
			f.env["CLAUDE_CODE_SESSION_ID"] = "s1"
			f.write(filepath.Join(f.home, ".claude", "f10", "state", "s1.started"), "")
			f.install["lastUpdated"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		}, StatusOK, ""},
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

		{CheckRemote, "off", nil, StatusNotNeeded, ""},
		{CheckRemote, "listening, service installed", func(f *fixture) { f.e.Remote = remoteWell() }, StatusOK, ""},
		{CheckRemote, "on without a service", func(f *fixture) {
			f.e.Remote = remoteWell()
			f.e.Remote.ServiceInstalled = false
		}, StatusOK, ""},
		{CheckRemote, "Tailscale down", func(f *fixture) {
			f.e.Remote = remoteWell()
			f.e.Remote.Tailnet, f.e.Remote.Bound = "", ""
		}, StatusProblem, "tailscale up"},
		{CheckRemote, "daemon not listening", func(f *fixture) {
			f.e.Remote = remoteWell()
			f.e.Remote.Bound, f.e.Remote.Reason = "", "listening on 100.64.0.7:7410: address in use"
		}, StatusProblem, "f10 daemon logs"},
		{CheckRemote, "plain HTTP", func(f *fixture) {
			f.e.Remote = remoteWell()
			f.e.Remote.HTTPSReason = "tailscale cert failed"
		}, StatusProblem, "HTTPS certificates"},
		{CheckRemote, "service program gone", func(f *fixture) {
			f.e.Remote = remoteWell()
			f.e.Remote.ProgramMissing = true
		}, StatusProblem, "f10 remote install"},
		{CheckRemote, "service pinned to an older release", func(f *fixture) {
			f.e.Remote = remoteWell()
			f.e.Remote.ServiceProgram = "/home/me/.claude/plugins/cache/amberpixels/f10/0.30.0/bin/f10"
		}, StatusProblem, "f10 remote install"},
		{CheckRemote, "service pinned to this release", func(f *fixture) {
			f.e.Remote = remoteWell()
			f.e.Remote.ServiceProgram = "/home/me/.claude/plugins/cache/amberpixels/f10/" + f.e.Version + "/bin/f10"
		}, StatusOK, ""},
		{CheckRemote, "setting unreadable", func(f *fixture) {
			f.e.Remote = &RemoteState{ConfigErr: "config.json does not read"}
		}, StatusProblem, "f10 remote on"},

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

func TestHerdrDetail(t *testing.T) {
	t.Parallel()

	const f10Linked = "1 plugin installed:\n- amberpixels.f10 (f10) enabled [local:/p]\n  config: /c\n"

	cases := []struct {
		label  string
		mutate func(*fixture)
		want   string
	}{
		{
			"upstream, plugin not linked", nil,
			"/usr/local/bin/herdr (herdr 0.9.5); amberpixels.f10 plugin not linked",
		},
		{"fork, plugin linked", func(f *fixture) {
			f.herdr["--version"] = shell.Result{Stdout: "herdr 0.9.5-fork.2026-11-21\n"}
			f.herdr["plugin list"] = shell.Result{Stdout: f10Linked}
		}, "/usr/local/bin/herdr (herdr 0.9.5-fork.2026-11-21); amberpixels.f10 plugin linked"},
		{"plugin disabled", func(f *fixture) {
			f.herdr["plugin list"] = shell.Result{Stdout: "- amberpixels.f10 (f10) disabled [local:/p]\n"}
		}, "/usr/local/bin/herdr (herdr 0.9.5); amberpixels.f10 plugin linked, disabled"},
		{"version fails", func(f *fixture) {
			f.herdr["--version"] = shell.Result{Code: 2}
		}, "/usr/local/bin/herdr; amberpixels.f10 plugin not linked"},
		{"plugin list fails", func(f *fixture) {
			delete(f.herdr, "plugin list")
		}, "/usr/local/bin/herdr (herdr 0.9.5)"},
		{"both fail", func(f *fixture) {
			f.herdr = nil
		}, "/usr/local/bin/herdr"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			if tc.mutate != nil {
				tc.mutate(f)
			}

			r := f.run(CheckHerdr)
			if r.Status != StatusOK || r.Remedy != "" {
				t.Errorf("status = %v, remedy = %q", r.Status, r.Remedy)
			}

			if r.Detail != tc.want {
				t.Errorf("detail = %q, want %q", r.Detail, tc.want)
			}
		})
	}
}

func TestHerdrMissingIsScopedAndDoesNotFail(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	delete(f.path, "herdr")

	r := f.run(CheckHerdr)
	if strings.Join(r.Scope, ",") != "start,forward,finish" {
		t.Errorf("scope = %v", r.Scope)
	}

	if Failed([]Result{r}) {
		t.Error("a scoped problem failed the run")
	}
}

func TestStatuslineMissingIsScopedAndDoesNotFail(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.settings["statusLine"] = map[string]any{"command": "echo hi"}

	r := f.run(CheckStatusline)
	if r.Status != StatusProblem || len(r.Scope) == 0 {
		t.Fatalf("status %q, scope %v: want a scoped problem", r.Status, r.Scope)
	}

	if Failed([]Result{r}) {
		t.Error("a missing badge failed the run")
	}
}

// A project-scope install is matched to the checkout through symlinks, the
// way macOS reaches /tmp through /private/tmp, and beats the user-scope one.
func TestProjectScopeInstallThroughASymlink(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	delete(f.env, "CLAUDECODE")

	alias := filepath.Join(t.TempDir(), "alias")
	must(t, os.Symlink(f.e.Layout.MainRoot, alias))

	other := t.TempDir()
	f.write(filepath.Join(other, ".claude-plugin", "plugin.json"), `{"name":"f10","version":"0.30.0"}`)

	f.writeJSON(filepath.Join(f.claude, "plugins", "installed_plugins.json"), map[string]any{"plugins": map[string]any{
		"f10@amberpixels": []any{
			f.install,
			map[string]any{"scope": "project", "projectPath": alias, "installPath": other, "version": "0.30.0"},
		},
	}})
	f.install = nil

	r := f.run(CheckVersion)
	if r.Status != StatusProblem || !strings.Contains(r.Detail, "plugin 0.30.0") {
		t.Errorf("version check judged %q, want the project-scope 0.30.0 install", r.Detail)
	}
}

func TestFailed(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	f := newFixture(t)
	f.run(CheckVersion)

	data, err := json.Marshal(Run(context.Background(), f.e))
	must(t, err)

	var got []map[string]any
	must(t, json.Unmarshal(data, &got))

	if len(got) != 8 {
		t.Fatalf("%d checks, want 8", len(got))
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

// shim writes an executable .f10/driver with body and points the fixture at it.
func (f *fixture) shim(body string) {
	f.t.Helper()

	path := filepath.Join(f.e.Layout.CheckoutRoot, ".f10", "driver")
	f.write(path, "#!/bin/sh\n"+body+"\n")
	must(f.t, os.Chmod(path, 0o755))
	f.e.Driver = &trackerdriver.Driver{Path: path, Dir: f.e.Layout.CheckoutRoot}
}

func TestDriverCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(f *fixture)
		want   string
		scoped bool
		detail string
	}{
		{"too old", func(f *fixture) {
			f.shim(`exec f10-drivers notion "$@"`)
			f.drivers = "f10-drivers v0.3.0"
		}, StatusProblem, true, "v0.3.0"},
		{"at the minimum", func(f *fixture) {
			f.shim(`exec f10-drivers notion "$@"`)
			f.drivers = "f10-drivers v0.4.0"
		}, StatusOK, false, "v0.4.0"},
		{"newer", func(f *fixture) {
			f.shim(`exec f10-drivers notion "$@"`)
			f.drivers = "f10-drivers v1.0.0"
		}, StatusOK, false, "v1.0.0"},
		{"absolute path in the shim", func(f *fixture) {
			exe := filepath.Join(f.t.TempDir(), "f10-drivers")
			f.write(exe, "#!/bin/sh\n")
			f.shim("exec " + exe + ` notion "$@"`)
			delete(f.path, "f10-drivers")
			f.drivers = "f10-drivers v0.3.0"
		}, StatusProblem, true, "v0.3.0"},
		{"not on PATH", func(f *fixture) {
			f.shim(`exec f10-drivers notion "$@"`)
			delete(f.path, "f10-drivers")
		}, StatusProblem, false, "not found"},
		{"quoted $HOME path, missing", func(f *fixture) {
			f.env["HOME"] = f.home
			f.shim(`exec "$HOME/go/bin/f10-drivers" notion "$@"`)
		}, StatusProblem, false, "not found"},
		{"quoted $HOME path, present", func(f *fixture) {
			f.env["HOME"] = f.home
			f.write(filepath.Join(f.home, "go", "bin", "f10-drivers"), "#!/bin/sh\n")
			f.shim(`exec "$HOME/go/bin/f10-drivers" notion "$@"`)
			delete(f.path, "f10-drivers")
			f.drivers = "f10-drivers v0.4.0"
		}, StatusOK, false, filepath.Join("go", "bin", "f10-drivers")},
		{"predates --version", func(f *fixture) {
			f.shim(`exec f10-drivers notion "$@"`)
		}, StatusProblem, true, "predates --version"},
		{"commented-out old path", func(f *fixture) {
			f.shim("# was: exec /nope/f10-drivers notion \"$@\"\nexec f10-drivers notion \"$@\" # current")
			f.drivers = "f10-drivers v0.3.0"
		}, StatusProblem, true, "v0.3.0"},
		{"tracker needs a driver, none found", func(f *fixture) {
			f.e.NeedsDriver = true
		}, StatusProblem, true, "no .f10/driver"},
		{"dev build", func(f *fixture) {
			f.shim(`exec f10-drivers notion "$@"`)
			f.drivers = "f10-drivers dev"
		}, StatusOK, false, "dev build"},
		{"custom driver", func(f *fixture) {
			f.shim(`exec my-jira-driver "$@"`)
		}, StatusOK, false, "not compared"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.path["f10-drivers"] = "/usr/local/bin/f10-drivers"
			c.mutate(f)

			r := f.run(CheckDriver)
			if r.Status != c.want || !strings.Contains(r.Detail, c.detail) {
				t.Fatalf("status = %q (%s), want %q with %q", r.Status, r.Detail, c.want, c.detail)
			}

			want := trackerdriver.F10DriversInstall
			if f.e.Driver == nil {
				want = "docs/driver-contract.md"
			}

			if c.want == StatusProblem && !strings.Contains(r.Remedy, want) {
				t.Errorf("remedy = %q, want it to contain %q", r.Remedy, want)
			}

			if c.want != StatusProblem && r.Remedy != "" {
				t.Errorf("remedy on a %s check: %q", r.Status, r.Remedy)
			}

			if scoped := len(r.Scope) > 0; scoped != c.scoped {
				t.Errorf("scope = %v, want scoped %v", r.Scope, c.scoped)
			}

			if c.scoped && Failed([]Result{r}) {
				t.Error("a scoped driver problem failed the run")
			}
		})
	}
}

func TestNoDriverPrintsNoDriverRow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.writeJSON(filepath.Join(f.claude, "settings.json"), f.settings)

	for _, r := range Run(context.Background(), f.e) {
		if r.Name == CheckDriver {
			t.Fatalf("driver row without a driver: %+v", r)
		}
	}
}

// remoteWell is a machine with the listener on, Tailscale up, the daemon
// listening and the service installed.
func remoteWell() *RemoteState {
	return &RemoteState{
		Enabled: true, Port: 7410, Tailnet: "100.64.0.7",
		DaemonRunning: true, Bound: "100.64.0.7:7410",
		ServiceFile: "/home/me/.config/systemd/user/f10-daemon.service", ServiceInstalled: true,
		ServiceProgram: "/home/me/bin/f10",
	}
}

func TestRemoteNamesTheMCPEndpoint(t *testing.T) {
	t.Parallel()

	st := remoteWell()
	st.MCP = true

	if r := checkRemote(
		&Env{Remote: st},
	); r.Status != StatusOK ||
		!strings.Contains(r.Detail, "MCP tools at http://100.64.0.7:7410/mcp") {
		t.Errorf("remote = %s %q, want the MCP endpoint named", r.Status, r.Detail)
	}

	st.URL = "https://box.tail1234.ts.net:7410/"
	if r := checkRemote(
		&Env{Remote: st},
	); !strings.Contains(
		r.Detail,
		"MCP tools at https://box.tail1234.ts.net:7410/mcp",
	) {
		t.Errorf("remote over HTTPS = %q, want the MCP endpoint on the HTTPS address", r.Detail)
	}
}

// A remote problem affects remote calls only, so it never fails the run.
func TestRemoteProblemIsScopedAndDoesNotFail(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.e.Remote = remoteWell()
	f.e.Remote.Tailnet = ""

	r := f.run(CheckRemote)
	if strings.Join(r.Scope, ",") != "remote" || Failed([]Result{r}) {
		t.Errorf("scope = %v, failed = %v; want remote, not failed", r.Scope, Failed([]Result{r}))
	}
}

// a defaulted tracker.title names the backfill on an OK check, and a
// declared one says nothing
func TestProjectNamesTitleDefault(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		origin facts.OriginKind
		want   bool
	}{{facts.OriginDefault, true}, {facts.OriginDeclared, false}} {
		f := newFixture(t)
		f.e.Facts.Facts = []facts.Fact{{Key: "tracker.title", Value: "{id}: {title}", Origin: facts.OriginOf(c.origin)}}

		r := f.run(CheckProject)
		if r.Status != StatusOK || r.Remedy != "" {
			t.Fatalf("%s: status %q, remedy %q; want OK", c.origin, r.Status, r.Remedy)
		}

		if got := strings.Contains(r.Detail, "f10 task retitle --all"); got != c.want {
			t.Errorf("%s: detail %q names the backfill = %v, want %v", c.origin, r.Detail, got, c.want)
		}
	}
}
