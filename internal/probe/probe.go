// Package probe runs the deterministic detection probes bin/resolve.sh
// runs: remote host and CLI routing, stack and verify marker files, and the
// existing plans. Each answer carries the evidence that produced it - the
// provenance column is the point of the binary.
package probe

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/amberpixels/f10/internal/gitx"
	"github.com/amberpixels/f10/internal/resolve"
)

// A Signal is one detected value plus the evidence that produced it.
type Signal struct {
	Value    string `json:"value"`
	Evidence string `json:"evidence"`
}

// Probes holds everything detection can say about a checkout.
type Probes struct {
	RemoteURL string   `json:"remote,omitempty"` // origin url; empty without an origin
	Host      string   `json:"host,omitempty"`   // hostname of the remote; empty for a local-path remote
	CLI       Signal   `json:"cli"`              // "gh", "glab", "unknown" or "none"
	Stack     []Signal `json:"stack,omitempty"`  // marker files at the checkout root
	Verify    []Signal `json:"verify,omitempty"`
	Plans     []string `json:"plans,omitempty"` // plan filenames under the storage root
	PlansDir  string   `json:"plansDir"`
}

// Run probes the resolved checkout. Read-only, like everything here.
func Run(ctx context.Context, res *resolve.Resolution) *Probes {
	p := &Probes{PlansDir: filepath.Join(res.StorageRoot, "plans")}

	p.RemoteURL = gitx.Out(ctx, res.CheckoutRoot, "remote", "get-url", "origin")
	if p.RemoteURL != "" {
		p.Host = hostOf(p.RemoteURL)
	}

	p.CLI = routeCLI(p.RemoteURL, p.Host)

	root := res.CheckoutRoot
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

	if fileExists(filepath.Join(root, "justfile")) || fileExists(filepath.Join(root, "Justfile")) {
		p.Verify = append(p.Verify, Signal{Value: "just lint / just test", Evidence: "justfile"})
	}

	if fileExists(filepath.Join(root, "Makefile")) {
		p.Verify = append(p.Verify, Signal{Value: "make", Evidence: "Makefile"})
	}

	p.Plans = planNames(p.PlansDir)

	return p
}

// hostOf extracts the hostname from a git remote url the way the script's
// host_of does - scp-like, ssh://, https://, userinfo and ports all
// handled. A local path remote yields "".
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

// planNames lists the .md files in the plans dir. Names, not a count: the
// binary is user-facing, and names are the point of a lookaround.
func planNames(dir string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(matches) == 0 {
		return nil
	}

	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, filepath.Base(m))
	}

	sort.Strings(names)

	return names
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)

	return err == nil && !fi.IsDir()
}
