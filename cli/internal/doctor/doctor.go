// Package doctor checks that the Claude Code plugin and the f10 binary are
// present, agree with each other and are wired into their environment. Every
// check reads and none writes, so `f10 doctor` is always safe to run.
package doctor

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// The three outcomes of a check.
const (
	StatusOK        = "ok"
	StatusProblem   = "problem"
	StatusNotNeeded = "not_needed" // the check does not apply here; it never fails the run
)

// A Result is one check's answer.
type Result struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Scope  []string `json:"scope"`  // what a problem affects (commands, or the badge); empty means every command
	Detail string   `json:"detail"` // the evidence, one line
	Remedy string   `json:"remedy"` // empty unless the status is problem
}

// Env is everything the checks read from outside, so a test can hand them
// a temp directory and fakes instead of the real home and PATH.
type Env struct {
	ClaudeDir string // CLAUDE_CONFIG_DIR or ~/.claude
	Home      string // the statusline symlink lives under ~/.claude/f10 whatever ClaudeDir is
	Layout    *layout.Layout
	Probes    *probe.Probes
	Facts     *facts.Effective
	Version   string // the binary's, as `f10 --version` prints it

	Getenv   func(string) string
	LookPath func(string) (string, error)
	Run      func(ctx context.Context, dir, name string, args ...string) (shell.Result, error)
}

// Run runs every check in a fixed order; a failing one never stops the rest.
func Run(ctx context.Context, env *Env) []Result {
	in, reason := readInstall(env)
	p := resolvePlugin(env, in)
	set := readSettings(env)

	results := []Result{
		checkVersion(env, p, reason),
		checkScripts(env, p, reason),
		checkHooks(env, p, in, reason, set),
		checkStatusline(env, set),
		checkHerdr(env),
		checkHost(ctx, env),
		checkProject(env),
	}

	for i := range results {
		if results[i].Scope == nil {
			results[i].Scope = []string{}
		}
	}

	return results
}

// Failed reports a problem in a check every command needs: a scoped problem
// affects only the commands it names, and a not-needed check never fails.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Status == StatusProblem && len(r.Scope) == 0 {
			return true
		}
	}

	return false
}

// pluginName is the plugin's name in every `<name>@<marketplace>` key.
const pluginName = "f10"

// An install is the plugin's entry in installed_plugins.json.
type install struct {
	Key         string `json:"-"` // f10@<marketplace>
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
	Version     string `json:"version"`
	LastUpdated string `json:"lastUpdated"`
	ProjectPath string `json:"projectPath"`
}

// id is the `<name>@<marketplace>` the claude CLI takes; without an
// install it names the marketplace f10 publishes from.
func (in *install) id() string {
	if in == nil || in.Key == "" {
		return pluginName + "@amberpixels"
	}

	return in.Key
}

// readInstall finds the plugin in installed_plugins.json, this checkout's
// project-scope entry before the user-scope one; a nil install carries the
// reason.
func readInstall(env *Env) (*install, string) {
	path := filepath.Join(env.ClaudeDir, "plugins", "installed_plugins.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "no plugins installed: " + path + " is missing"
	}

	var file struct {
		Plugins map[string][]install `json:"plugins"`
	}

	if err := json.Unmarshal(data, &file); err != nil {
		return nil, path + " does not parse: " + err.Error()
	}

	var found *install

	// sorted, so two marketplaces carrying f10 always resolve the same way
	for _, key := range slices.Sorted(maps.Keys(file.Plugins)) {
		entries := file.Plugins[key]
		if name, _, _ := strings.Cut(key, "@"); name != pluginName {
			continue
		}

		for i := range entries {
			e := entries[i]
			e.Key = key

			switch {
			case e.Scope == "project" && env.Layout != nil && samePath(e.ProjectPath, env.Layout.MainRoot):
				return &e, ""
			case e.Scope == "user" && found == nil:
				found = &e
			}
		}
	}

	if found == nil {
		return nil, "f10 is not in " + path
	}

	return found, ""
}

// settings is the slice of Claude Code's settings the checks read, merged
// user, then project, then project-local: a later file's value wins.
type settings struct {
	Enabled    map[string]bool
	StatusLine string // the statusLine command
	Refresh    bool   // statusLine.refreshInterval is set
	From       string // the file the statusLine came from
	User       string // the user settings file, where the remedies point
}

func readSettings(env *Env) settings {
	set := settings{Enabled: map[string]bool{}, User: filepath.Join(env.ClaudeDir, "settings.json")}

	// the main checkout's project settings, then a linked worktree's on top:
	// settings.local.json is untracked, so a worktree usually has none
	files := []string{set.User}
	if env.Layout != nil {
		for _, root := range []string{env.Layout.MainRoot, env.Layout.CheckoutRoot} {
			if root == "" || (root == env.Layout.CheckoutRoot && samePath(root, env.Layout.MainRoot)) {
				continue
			}

			files = append(files,
				filepath.Join(root, ".claude", "settings.json"),
				filepath.Join(root, ".claude", "settings.local.json"))
		}
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var file struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
			StatusLine     *struct {
				Command         string   `json:"command"`
				RefreshInterval *float64 `json:"refreshInterval"`
			} `json:"statusLine"`
		}

		if json.Unmarshal(data, &file) != nil {
			continue
		}

		maps.Copy(set.Enabled, file.EnabledPlugins)

		if file.StatusLine != nil {
			set.StatusLine = file.StatusLine.Command
			set.Refresh = file.StatusLine.RefreshInterval != nil
			set.From = path
		}
	}

	return set
}

// samePath compares two paths after resolving symlinks, so /tmp and
// /private/tmp are one directory.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}

	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}

	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}

	return filepath.Clean(a) == filepath.Clean(b)
}
