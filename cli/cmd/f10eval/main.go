// Command f10eval runs f10's prompt scenarios against the real model and
// checks what each run leaves behind. It costs model spend, so it runs on
// demand and before a release (`just eval`), never in CI; its reports under
// evals/reports/<version>/ are kept in git so releases compare by diff.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A Scenario is one evals/scenarios/<name>/scenario.json.
type Scenario struct {
	Name        string   `json:"-"`
	Description string   `json:"description"`
	Task        string   `json:"task"`
	Prompt      string   `json:"prompt"`
	Verify      []string `json:"verify"`
	Accept      []Accept `json:"accept"`
	Checks      []string `json:"checks"`

	env []string // the run's environment, so a check that runs f10 reads the run's state
	f10 string   // this checkout's bin/f10, the binary the run itself used
}

// An Accept lives in scenario.json, outside the fixture, so the run never
// sees what it is checked against.
type Accept struct {
	Run  string `json:"run"`
	Want string `json:"want"`
}

// A Report is one scenario's run, as written under evals/reports.
type Report struct {
	Scenario   string    `json:"scenario"`
	Version    string    `json:"version"`
	Commit     string    `json:"commit"`
	At         time.Time `json:"at"`
	Pass       bool      `json:"pass"`
	Checks     []Check   `json:"checks"`
	CostUSD    float64   `json:"cost_usd"`
	DurationMS int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
}

type options struct {
	root       string
	only       string
	permission string
	budget     string
	keep       bool
}

func main() {
	var o options

	flag.StringVar(&o.root, "root", ".", "the f10 checkout: its plugin is loaded and its evals/ run")
	flag.StringVar(&o.only, "scenario", "", "run only this scenario")
	flag.StringVar(&o.permission, "permission-mode", "acceptEdits",
		"claude's --permission-mode for the run; a driven ship runs commands, which acceptEdits may refuse")
	flag.StringVar(&o.budget, "max-budget-usd", "5", "claude's --max-budget-usd per scenario")
	flag.BoolVar(&o.keep, "keep", false, "keep each scenario's temp dir and print its path")
	flag.Parse()

	if err := run(context.Background(), o); err != nil {
		fmt.Fprintln(os.Stderr, "f10eval:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options) error {
	root, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}

	scenarios, err := loadScenarios(filepath.Join(root, "evals", "scenarios"), o.only)
	if err != nil {
		return err
	}

	// the plugin's bin/f10 runs its cached build of this checkout, else the
	// release: build it, so the report's commit is the code that ran
	f10 := filepath.Join(root, "bin", "f10")
	if out, err := exec.CommandContext(ctx, f10, "--shim-rebuild").CombinedOutput(); err != nil {
		return fmt.Errorf("building this checkout's f10: %w: %s", err, lastLine(string(out)))
	}

	version, commit := pluginVersion(root), gitOut(ctx, root, "rev-parse", "--short", "HEAD")
	failed := 0

	for _, s := range scenarios {
		s.f10 = f10
		rep := runScenario(ctx, root, s, o)
		rep.Version, rep.Commit = version, commit

		path := filepath.Join(root, "evals", "reports", version, s.Name+".json")
		if err := writeReport(path, rep); err != nil {
			return err
		}

		status := "pass"
		if !rep.Pass {
			status, failed = "FAIL", failed+1
		}

		fmt.Printf("%-24s %s  $%.2f  %s  %s\n", s.Name, status, rep.CostUSD,
			time.Duration(rep.DurationMS)*time.Millisecond, path)
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d scenarios failed", failed, len(scenarios))
	}

	return nil
}

func loadScenarios(dir, only string) ([]Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var list []Scenario

	for _, e := range entries {
		if !e.IsDir() || only != "" && e.Name() != only {
			continue
		}

		body, err := os.ReadFile(filepath.Join(dir, e.Name(), "scenario.json"))
		if err != nil {
			return nil, err
		}

		s := Scenario{Name: e.Name()}
		if err := json.Unmarshal(body, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}

		for _, c := range s.Checks {
			if checks[c] == nil {
				return nil, fmt.Errorf("%s: unknown check %q", e.Name(), c)
			}
		}

		list = append(list, s)
	}

	if len(list) == 0 {
		return nil, fmt.Errorf("no scenario under %s matches %q", dir, only)
	}

	return list, nil
}

// runScenario points f10's ledger, run state, asks and daemon dirs inside
// the temp dir, so the nested run never reaches the user's own; the repo
// has no origin, so nothing can be pushed.
func runScenario(ctx context.Context, root string, s Scenario, o options) Report {
	rep := Report{Scenario: s.Name, At: time.Now().UTC()}

	tmp, err := os.MkdirTemp("", "f10eval-"+s.Name+"-")
	if err != nil {
		rep.Error = err.Error()

		return rep
	}

	if o.keep {
		fmt.Println("kept:", tmp)
	} else {
		defer os.RemoveAll(tmp)
	}

	dir := filepath.Join(tmp, "repo")
	if err := prepare(ctx, filepath.Join(root, "evals", "scenarios", s.Name, "repo"), dir); err != nil {
		rep.Error = err.Error()

		return rep
	}

	s.env = append(os.Environ(),
		"F10_LEDGER="+filepath.Join(tmp, "f10.db"),
		"F10_STATE_DIR="+filepath.Join(tmp, "state"),
		"F10_ASKS_DIR="+filepath.Join(tmp, "asks"),
		"F10_RUN_DIR="+filepath.Join(tmp, "run"),
	)

	cmd := exec.CommandContext(ctx, "claude", "-p", "--plugin-dir", root, "--output-format", "json",
		"--permission-mode", o.permission, "--max-budget-usd", o.budget, s.Prompt)
	cmd.Dir, cmd.Env = dir, s.env

	out, err := cmd.Output()

	var res struct {
		CostUSD    float64 `json:"total_cost_usd"`
		DurationMS int64   `json:"duration_ms"`
		IsError    bool    `json:"is_error"`
		Result     string  `json:"result"`
	}

	if jerr := json.Unmarshal(out, &res); jerr == nil {
		rep.CostUSD, rep.DurationMS = res.CostUSD, res.DurationMS
		if res.IsError {
			rep.Error = lastLine(res.Result)
		}
	}

	if err != nil && rep.Error == "" {
		rep.Error = fmt.Sprintf("claude: %v", err)
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			rep.Error += ": " + lastLine(string(exit.Stderr))
		}
	}

	rep.Checks, rep.Pass = runChecks(ctx, dir, s)
	rep.Pass = rep.Pass && rep.Error == ""

	return rep
}

func runChecks(ctx context.Context, dir string, s Scenario) ([]Check, bool) {
	pass := true
	list := make([]Check, 0, len(s.Checks))

	for _, name := range s.Checks {
		c := checks[name](ctx, dir, s)
		list = append(list, c)
		pass = pass && c.Pass
	}

	return list, pass
}

// prepare renames the fixture's f10/ to .f10/, since a .f10/ inside this
// repo would be excluded with f10's own.
func prepare(ctx context.Context, src, dir string) error {
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		return err
	}

	if err := os.Rename(filepath.Join(dir, "f10"), filepath.Join(dir, ".f10")); err != nil &&
		!errors.Is(err, fs.ErrNotExist) {
		return err
	}

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "eval@f10.invalid"},
		{"config", "user.name", "f10 eval"},
		{"add", "-A", "--", ".", ":!.f10"},
		{"commit", "-qm", "initial"},
	} {
		if out, err := git(ctx, dir, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", args[0], err, out)
		}
	}

	return os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte(".f10/\n"), 0o600)
}

func git(ctx context.Context, dir string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed git args
}

func writeReport(path string, rep Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}

	body, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(body, '\n'), 0o644) //nolint:gosec // a report committed to the repo
}

func pluginVersion(root string) string {
	body, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return "unknown"
	}

	var p struct {
		Version string `json:"version"`
	}

	if json.Unmarshal(body, &p) != nil || p.Version == "" {
		return "unknown"
	}

	return p.Version
}

func gitOut(ctx context.Context, dir string, args ...string) string {
	out, err := git(ctx, dir, args...).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
