// Package probe runs the deterministic detection probes bin/f10-load-project.sh
// runs: host and CLI routing, stack and verify markers, existing plans. Each
// answer carries the evidence that produced it.
package probe

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/layout"
)

// A Signal is one detected value plus the evidence that produced it.
type Signal struct {
	Value    string `json:"value"`
	Evidence string `json:"evidence"`
	// Judgment marks guidance rather than commands: a build file with no
	// lint or test entry, or a bare stack. Never written into project.md.
	Judgment bool `json:"judgment,omitempty"`
}

// Probes holds everything detection can say about a checkout.
type Probes struct {
	RemoteURL string   `json:"remote,omitempty"` // origin url; empty without an origin
	Host      string   `json:"host,omitempty"`   // hostname of the remote; empty for a local-path remote
	CLI       Signal   `json:"cli"`              // "gh", "glab", "unknown" or "none"
	Stack     []Signal `json:"stack,omitempty"`  // marker files at the checkout root
	Verify    []Signal `json:"verify,omitempty"`
	Review    *Review  `json:"review,omitempty"` // a CI review workflow, when one uses the Claude Code action
	Plans     []string `json:"plans,omitempty"`  // plan filenames under the storage root
	PlansDir  string   `json:"plansDir"`
}

// Run probes the located checkout. Read-only, like everything here.
func Run(ctx context.Context, lay *layout.Layout) *Probes {
	p := &Probes{PlansDir: filepath.Join(lay.StorageRoot, "plans")}

	p.RemoteURL = gitx.Out(ctx, lay.CheckoutRoot, "remote", "get-url", "origin")
	if p.RemoteURL != "" {
		p.Host = hostOf(p.RemoteURL)
	}

	p.CLI = routeCLI(p.RemoteURL, p.Host)

	root := lay.CheckoutRoot
	for _, m := range []struct{ file, stack string }{
		{file: "go.mod", stack: "Go"},
		{file: "Gemfile", stack: "Ruby"},
		{file: "package.json", stack: "JS/TS"},
	} {
		if fileExists(filepath.Join(root, m.file)) {
			p.Stack = append(p.Stack, Signal{Value: m.stack, Evidence: m.file})
		}
	}

	if specs, _ := filepath.Glob(filepath.Join(root, "*.gemspec")); len(specs) > 0 {
		p.Stack = append(p.Stack, Signal{Value: "Ruby gem", Evidence: filepath.Base(specs[0])})
	}

	p.Verify = verifySignals(root, p.Stack)

	p.Review = reviewWorkflow(root)
	p.Plans = planNames(p.PlansDir)

	return p
}

// hostOf is a git remote url's hostname, read as the script's host_of reads
// it; a local path remote yields "".
func hostOf(url string) string {
	h := url
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}

	if i := strings.Index(h, "@"); i >= 0 {
		h = h[i+1:]
	}

	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}

	if i := strings.Index(h, ":"); i >= 0 {
		h = h[:i]
	}

	return h
}

// routeCLI picks the PR/issue CLI for a host: whichever of gh/glab is
// already logged in to it wins, then the well-known hosts, then unknown.
func routeCLI(remote, host string) Signal {
	if remote == "" {
		return Signal{Value: "none", Evidence: "no origin remote"}
	}

	if host == "" {
		return Signal{Value: "none", Evidence: "local path remote"}
	}

	if path := ghHostsPath(); knowsHost(path, host) {
		return Signal{Value: "gh", Evidence: "logged in to " + host + " per " + path}
	}

	if path := glabConfigPath(); knowsHost(path, host) {
		return Signal{Value: "glab", Evidence: "logged in to " + host + " per " + path}
	}

	switch host {
	case "github.com":
		return Signal{Value: "gh", Evidence: "well-known host " + host}
	case "gitlab.com":
		return Signal{Value: "glab", Evidence: "well-known host " + host}
	}

	return Signal{Value: "unknown", Evidence: "neither gh nor glab is logged in to " + host}
}

// knowsHost reports whether the CLI config file lists host as a key. It
// matches key presence only - both files hold OAuth tokens, so their
// content never travels past this boolean.
func knowsHost(path, host string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(host) + `:`)

	return re.Match(data)
}

func ghHostsPath() string {
	if d := os.Getenv("GH_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "hosts.yml")
	}

	return filepath.Join(xdgConfigHome(), "gh", "hosts.yml")
}

func glabConfigPath() string {
	if d := os.Getenv("GLAB_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "config.yml")
	}

	return filepath.Join(xdgConfigHome(), "glab-cli", "config.yml")
}

func xdgConfigHome() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".config")
}

// planNames lists the plans dir's .md files, most recent first: the dir
// grows without bound, so recency lets renderers trim to the freshest few.
func planNames(dir string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(matches) == 0 {
		return nil
	}

	mtime := make(map[string]int64, len(matches))
	names := make([]string, 0, len(matches))

	for _, m := range matches {
		name := filepath.Base(m)
		names = append(names, name)

		if fi, err := os.Stat(m); err == nil { //nolint:gosec // a glob match under this checkout's own storage root
			mtime[name] = fi.ModTime().UnixNano()
		}
	}

	sort.Slice(names, func(i, j int) bool {
		if mtime[names[i]] != mtime[names[j]] {
			return mtime[names[i]] > mtime[names[j]]
		}

		return names[i] < names[j] // stable answer for equal mtimes
	})

	return names
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)

	return err == nil && !fi.IsDir()
}

// verifySignals names the verify commands the build files hold, else a
// judgment from the stack, so no step re-derives the fallback by hand.
func verifySignals(root string, stack []Signal) []Signal {
	var out []Signal

	for _, b := range []struct {
		files   []string
		tool    string
		noun    string
		targets []string
		names   func(string) []string
	}{
		{files: []string{"justfile", "Justfile"}, tool: "just", noun: "recipe", targets: []string{"lint", "test", "check"}, names: justRecipes},
		{files: []string{"Makefile"}, tool: "make", noun: "target", targets: []string{"lint", "test", "check"}, names: makeTargets},
	} {
		for _, f := range b.files {
			data, err := os.ReadFile(filepath.Join(root, f))
			if err != nil {
				continue
			}

			have := b.names(string(data))

			var cmds []string

			for _, t := range b.targets {
				if slices.Contains(have, t) {
					cmds = append(cmds, b.tool+" "+t)
				}
			}

			if len(cmds) == 0 {
				last := len(b.targets) - 1
				entries := strings.Join(b.targets[:last], ", ") + " or " + b.targets[last]
				out = append(out, Signal{
					Value:    "the " + f + " has no " + entries + " " + b.noun + ": pick the ones that lint and test",
					Evidence: f,
					Judgment: true,
				})
			} else {
				out = append(out, Signal{Value: strings.Join(cmds, " / "), Evidence: f})
			}

			break
		}
	}

	if len(out) > 0 {
		return out
	}

	idiomatic := map[string]string{
		"Go":       "go test ./... and the linter the repo configures",
		"Ruby":     "bundle exec rake or rspec, and rubocop where the repo configures it",
		"Ruby gem": "bundle exec rake or rspec, and rubocop where the repo configures it",
		"JS/TS":    "the package.json test and lint scripts",
	}

	seen := map[string]bool{}

	for _, s := range stack {
		cmds, ok := idiomatic[s.Value]
		if !ok || seen[cmds] {
			continue
		}

		seen[cmds] = true
		out = append(out, Signal{
			Value:    "no build file names them: the stack's idiomatic commands, e.g. " + cmds,
			Evidence: s.Evidence,
			Judgment: true,
		})
	}

	return out
}

// VerifyPicked is the signals verify runs from: the commands found, else the
// judgments, never both, so a found command is never mixed with a guess.
func VerifyPicked(signals []Signal) ([]Signal, bool) {
	var picked []Signal

	for _, s := range signals {
		if !s.Judgment {
			picked = append(picked, s)
		}
	}

	if len(picked) > 0 {
		return picked, false
	}

	return signals, len(signals) > 0
}

// justRecipes reads recipe names from top-level lines: `name args...:`,
// `@name:`, never an assignment (`x := y`) or a setting.
func justRecipes(src string) []string {
	var names []string

	for _, head := range ruleHeads(src) {
		if f := strings.Fields(head); len(f) > 0 {
			names = append(names, strings.TrimPrefix(f[0], "@"))
		}
	}

	return names
}

// makeTargets reads every target a rule line names (`lint test: deps`).
func makeTargets(src string) []string {
	var names []string

	for _, head := range ruleHeads(src) {
		names = append(names, strings.Fields(head)...)
	}

	return names
}

// ruleHeads is the text before the colon of each unindented rule line,
// skipping comments, attributes and `:=` assignments.
func ruleHeads(src string) []string {
	var heads []string

	for line := range strings.SplitSeq(src, "\n") {
		if line == "" || strings.ContainsAny(line[:1], " \t#[") {
			continue
		}

		head, rest, ok := strings.Cut(line, ":")
		f := strings.Fields(head)

		// `x := y`, `X = a:b`, `X ?= a:b` (`+=`, `!=`); a just parameter
		// default (`arg="x"`) does not end in `=`, so it is kept
		if !ok || len(f) == 0 || strings.HasPrefix(rest, "=") || strings.Contains(f[0], "=") ||
			(len(f) > 1 && (strings.HasPrefix(f[1], "=") || strings.HasSuffix(f[1], "="))) {
			continue
		}

		heads = append(heads, head)
	}

	return heads
}
