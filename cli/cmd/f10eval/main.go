// Command f10eval runs f10's prompt scenarios against the real model and
// checks what each run leaves behind. It costs model spend, so it runs on
// demand and before a release (`just eval`), never in CI; its reports under
// evals/reports/<version>/ are kept in git, and `f10eval compare` reads two
// report sets side by side.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// A Scenario is one evals/scenarios/<name>/scenario.json.
type Scenario struct {
	Name        string    `json:"-"`
	Description string    `json:"description"`
	Task        string    `json:"task"`
	Prompt      string    `json:"prompt"`
	Prompts     []string  `json:"prompts"` // in place of prompt: turns of one conversation, each resumed from the last
	Verify      []string  `json:"verify"`
	Accept      []Accept  `json:"accept"`
	Checks      []string  `json:"checks"`
	Agents      []string  `json:"agents"`  // the profiles the scenario runs on, one run each; none is one run on the kind's default
	Fixture     string    `json:"fixture"` // another scenario whose repo/ this one runs in; none is its own
	Change      string    `json:"change"`  // a patch, relative to the scenario's dir, committed on <task>/change for a review to read
	Branch      string    `json:"branch"`  // a branch checked out before the run, so its commits have main to be reviewed against
	Planted     []Planted `json:"planted"`
	MaxExtra    *int      `json:"max_extra"` // findings allowed outside every planted defect; none is defaultMaxExtra
	// History is commits made on main after the initial one, Reviews the
	// review rounds the run's ledger holds for their tasks, and HistoryCites
	// the ids the plan's ## History section must name.
	History      []HistoryCommit `json:"history"`
	Reviews      []SeedReview    `json:"reviews"`
	HistoryCites []string        `json:"history_cites"`
	Rubric       *Rubric         `json:"rubric"`       // what the rubric check grades, and against which lines
	Captured     *Captured       `json:"captured"`     // the shape of the tasks a capture run creates
	WantVerdict  string          `json:"want_verdict"` // the verdict a judge run must reach, else any of the four

	env   []string // the run's environment, so a check that runs f10 reads the run's state
	f10   string   // this checkout's bin/f10, the binary the run itself used
	reply string   // the run's final reply, for the rubric check
	grade grader   // per scenario, not global, so parallel tests each grade with their own
}

// An Accept lives in scenario.json, outside the fixture, so the run never
// sees what it is checked against.
type Accept struct {
	Run  string `json:"run"`
	Want string `json:"want"`
}

// A Planted defect is one a review scenario's change carries on purpose:
// a finding must land in File between lines From and To.
type Planted struct {
	File string `json:"file"`
	From int    `json:"from"`
	To   int    `json:"to"`
	What string `json:"what"`
}

// A HistoryCommit appends to File and commits it with Message, whose
// trailers name the task the commit belongs to.
type HistoryCommit struct {
	Message string `json:"message"`
	File    string `json:"file"`
	Append  string `json:"append"`
}

// A SeedReview is a task's review rounds, each raising Categories.
type SeedReview struct {
	Task       string           `json:"task"`
	Rounds     int              `json:"rounds"`
	Categories map[string]int32 `json:"categories"`
}

// A Report is one scenario's run, as written under evals/reports.
type Report struct {
	Scenario string    `json:"scenario"`
	Agent    string    `json:"agent,omitempty"` // the profile the run started on, resolved; "" for the kind's default
	Version  string    `json:"version"`
	Commit   string    `json:"commit"`
	Repeat   int       `json:"repeat,omitempty"` // 1-based under -repeat above 1; 0 for a single run
	At       time.Time `json:"at"`
	Pass     bool      `json:"pass"`
	Checks   []Check   `json:"checks"`
	CostUSD  float64   `json:"cost_usd"`
	// GraderCostUSD is the rubric check's spend, apart from the run's, so
	// cost_usd compares across releases with and without a rubric.
	GraderCostUSD float64 `json:"grader_cost_usd,omitempty"`
	DurationMS    int64   `json:"duration_ms"`
	Error         string  `json:"error,omitempty"`
}

type options struct {
	root       string
	only       string
	permission string
	budget     string
	agent      string
	out        string
	repeat     int
	keep       bool
}

func main() {
	var o options

	flag.StringVar(&o.root, "root", ".", "the f10 checkout: its plugin is loaded and its evals/ run")
	flag.StringVar(&o.only, "scenario", "", "run only this scenario")
	flag.StringVar(&o.permission, "permission-mode", "acceptEdits",
		"claude's --permission-mode for the run; a driven ship runs commands, which acceptEdits may refuse")
	flag.StringVar(&o.budget, "max-budget-usd", "5",
		"claude's --max-budget-usd per turn; a scenario with several prompts may spend it on each")
	flag.StringVar(&o.agent, "agent", "",
		"run every scenario on this agent `profile` (kind:model:effort, or a part), not the ones it lists")
	flag.StringVar(&o.out, "out", "",
		"write reports to this `dir`, relative to -root (default evals/reports/<version>)")
	flag.IntVar(&o.repeat, "repeat", 1, "run every scenario `N` times on each of its profiles")
	flag.BoolVar(&o.keep, "keep", false, "keep each scenario's temp dir and print its path")
	flag.Parse()

	// `just eval compare <a> <b>` arrives as `-root .. compare <a> <b>`, so
	// the subcommand follows the flags and shares -root
	if flag.Arg(0) == "compare" {
		if err := compareMain(os.Stdout, o.root, flag.Args()[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "f10eval compare:", err)
			os.Exit(1)
		}

		return
	}

	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "f10eval: unexpected argument %q\n", flag.Arg(0))
		os.Exit(2)
	}

	if o.repeat < 1 {
		fmt.Fprintln(os.Stderr, "f10eval: -repeat must be 1 or more")
		os.Exit(2)
	}

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

	// a run on an uncommitted tree says so, as git describe --dirty does,
	// since the commit alone would name code that did not run
	if gitOut(ctx, root, "status", "--porcelain") != "" {
		commit += "-dirty"
	}

	out := resolveDir(root, cmp.Or(o.out, filepath.Join("evals", "reports", version)))
	failed, runs := 0, 0

	for _, s := range scenarios {
		s.f10 = f10

		profiles, err := profilesFor(s, o.agent)
		if err != nil {
			return err
		}

		for _, p := range profiles {
			for n := range o.repeat {
				repeat := 0
				if o.repeat > 1 {
					repeat = n + 1
				}

				runs++
				rep := runScenario(ctx, root, s, o, p)
				rep.Version, rep.Commit, rep.Repeat = version, commit, repeat

				path := filepath.Join(out, reportName(s.Name, p, repeat))
				if err := writeReport(path, rep); err != nil {
					return err
				}

				status := "pass"
				if !rep.Pass {
					status, failed = "FAIL", failed+1
				}

				cost := fmt.Sprintf("$%.2f", rep.CostUSD)
				if rep.GraderCostUSD > 0 {
					cost += fmt.Sprintf(" + $%.2f grader", rep.GraderCostUSD)
				}

				name := s.Name
				if repeat > 0 {
					name += fmt.Sprintf(" #%d", repeat)
				}

				fmt.Printf("%-28s %-16s %s  %s  %s  %s\n", name, p.Short(), status, cost,
					time.Duration(rep.DurationMS)*time.Millisecond, path)
			}
		}
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d runs failed", failed, runs)
	}

	return nil
}

// checkRubricField refuses a rubric that cannot grade before any spend, and
// one the checks never run, which its author would not otherwise notice.
func (s Scenario) checkRubricField() error {
	listed := slices.Contains(s.Checks, "rubric")

	switch {
	case !listed && s.Rubric != nil:
		return errors.New("a rubric, but no rubric check")
	case !listed:
		return nil
	case s.Rubric == nil || len(s.Rubric.Lines) == 0:
		return errors.New("a rubric check, but no rubric lines")
	case !s.Rubric.Reply && len(s.Rubric.Files) == 0:
		return errors.New("a rubric grading neither the reply nor any file")
	}

	return nil
}

// checkFields refuses a scenario whose fields contradict each other, before
// any spend.
func (s Scenario) checkFields() error {
	if err := s.checkRubricField(); err != nil {
		return err
	}

	switch {
	case s.Prompt != "" && len(s.Prompts) > 0:
		return errors.New("both prompt and prompts")
	case s.Branch != "" && s.Change != "":
		return errors.New("both branch and change, which checks out a branch of its own")
	case slices.Contains(s.Checks, "captured") != (s.Captured != nil):
		return errors.New("the captured check and the captured field come together")
	case s.WantVerdict != "" && !slices.Contains(verdicts, s.WantVerdict):
		return fmt.Errorf("want_verdict %q is none of the four verdicts", s.WantVerdict)
	}

	return nil
}

func (s Scenario) turns() []string {
	if len(s.Prompts) > 0 {
		return s.Prompts
	}

	return []string{s.Prompt}
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

		if err := s.checkFields(); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
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
func runScenario(ctx context.Context, root string, s Scenario, o options, p agentctl.Profile) Report {
	rep := Report{Scenario: s.Name, Agent: p.Short(), At: time.Now().UTC()}

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

	scenarios := filepath.Join(root, "evals", "scenarios")
	dir := filepath.Join(tmp, "repo")

	if err := prepare(ctx, filepath.Join(scenarios, cmp.Or(s.Fixture, s.Name), "repo"), dir); err != nil {
		rep.Error = err.Error()

		return rep
	}

	if err := applyHistory(ctx, dir, s.History); err != nil {
		rep.Error = err.Error()

		return rep
	}

	if err := seedReviews(ctx, dir, filepath.Join(tmp, "f10.db"), s.Reviews); err != nil {
		rep.Error = err.Error()

		return rep
	}

	if s.Branch != "" {
		if out, err := git(ctx, dir, "checkout", "-q", "-b", s.Branch).CombinedOutput(); err != nil {
			rep.Error = fmt.Sprintf("git checkout: %v: %s", err, out)

			return rep
		}
	}

	if s.Change != "" {
		if err := applyChange(ctx, dir, s.Task, filepath.Join(scenarios, s.Name, s.Change)); err != nil {
			rep.Error = err.Error()

			return rep
		}
	}

	s.env = runEnv(os.Environ(), tmp)

	turns, session := s.turns(), ""

	for i, prompt := range turns {
		res, err := runTurn(ctx, dir, s.env, claudeArgs(root, o, prompt, p, session))
		rep.CostUSD += res.CostUSD
		rep.DurationMS += res.DurationMS
		session = res.SessionID

		if err != nil {
			s.reply, rep.Error = "", err.Error()
			if len(turns) > 1 {
				rep.Error = fmt.Sprintf("turn %d: %s", i+1, rep.Error)
			}

			break
		}

		s.reply = res.Result

		// a later turn without a session would start a new conversation
		// and fail as a later stage's check, not as the runner's error
		if session == "" && i < len(turns)-1 {
			rep.Error = fmt.Sprintf("turn %d: claude printed no session id to resume", i+1)

			break
		}
	}

	s.grade = gradeWithClaude

	rep.Checks, rep.Pass = runChecks(ctx, dir, s)
	rep.Pass = rep.Pass && rep.Error == ""
	rep.GraderCostUSD = graderCost(rep.Checks)

	return rep
}

// A turnResult is the part of claude's JSON output the runner reads.
type turnResult struct {
	CostUSD    float64 `json:"total_cost_usd"`
	DurationMS int64   `json:"duration_ms"`
	IsError    bool    `json:"is_error"`
	Result     string  `json:"result"`
	SessionID  string  `json:"session_id"`
}

// runTurn runs one claude call. An errored turn's result is claude's error,
// not a reply worth grading, so it comes back as the error.
func runTurn(ctx context.Context, dir string, env, args []string) (turnResult, error) {
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir, cmd.Env = dir, env

	out, err := cmd.Output()

	var res turnResult
	if jerr := json.Unmarshal(out, &res); jerr == nil && res.IsError {
		return res, errors.New(lastLine(res.Result))
	}

	if err != nil {
		msg := fmt.Sprintf("claude: %v", err)
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			msg += ": " + lastLine(string(exit.Stderr))
		}

		return res, errors.New(msg)
	}

	return res, nil
}

func graderCost(list []Check) float64 {
	total := 0.0

	for _, c := range list {
		if c.Rubric != nil {
			total += c.Rubric.CostUSD
		}
	}

	return total
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

	if err := os.Rename(filepath.Join(dir, "f10"), storage.InRepo(dir).Path()); err != nil &&
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

// applyChange commits the patch on a branch named for the task, so a review
// run finds the task's branch checked out with a diff against main.
func applyChange(ctx context.Context, dir, task, patch string) error {
	abs, err := filepath.Abs(patch)
	if err != nil {
		return err
	}

	for _, args := range [][]string{
		{"checkout", "-q", "-b", task + "/change"},
		{"apply", abs},
		{"add", "-A"}, // prepare already excluded .f10/, and a pathspec naming it would fail as ignored
		{"commit", "-qm", task + ": change"},
	} {
		if out, err := git(ctx, dir, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", args[0], err, out)
		}
	}

	return nil
}

// applyHistory makes each commit on the checked-out branch, so the run
// finds tasks that touched the files before it.
func applyHistory(ctx context.Context, dir string, commits []HistoryCommit) error {
	for _, c := range commits {
		f, err := os.OpenFile(filepath.Join(dir, c.File), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}

		_, err = f.WriteString(c.Append)
		if cerr := f.Close(); err == nil {
			err = cerr
		}

		if err != nil {
			return err
		}

		for _, args := range [][]string{{"add", "--", c.File}, {"commit", "-qm", c.Message}} {
			if out, err := git(ctx, dir, args...).CombinedOutput(); err != nil {
				return fmt.Errorf("git %s: %w: %s", args[0], err, out)
			}
		}
	}

	return nil
}

// seedReviews writes the review rounds into the run's ledger at path, under
// the project the run will record itself as.
func seedReviews(ctx context.Context, dir, path string, reviews []SeedReview) error {
	if len(reviews) == 0 {
		return nil
	}

	project := ledger.Where(ctx, dir).Project
	at := time.Now().Add(-24 * time.Hour).UTC()

	var events []ledger.Event

	for _, r := range reviews {
		for n := 1; n <= r.Rounds; n++ {
			e, err := ledger.NewEvent(ledger.ReviewRoundOpened,
				// review writes rounds 1 and 2 only; a later pass is a follow-up PR's series
				&ledger.ReviewRoundOpenedPayload{
					Round: strconv.Itoa(min(n, 2)), Reviewed: fmt.Sprintf("seed%03d", n), Categories: r.Categories,
				})
			if err != nil {
				return err
			}

			e.ID = fmt.Sprintf("seed-%s-%02d", r.Task, n)
			e.At, e.Project, e.Session, e.Task = at, project, "seed-"+r.Task, r.Task
			events = append(events, e)
		}
	}

	l, err := ledger.Open(ctx, path, time.Second, false)
	if err != nil {
		return err
	}

	defer l.Close()

	l.Append(ctx, events...)

	return nil
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

// profilesFor is the profiles a scenario runs on: the flag's alone, else
// each the scenario lists, else one run on the kind's default. Each is
// resolved here, so a misspelt family is corrected before the spend.
func profilesFor(s Scenario, only string) ([]agentctl.Profile, error) {
	raws := s.Agents
	if only != "" {
		raws = []string{only}
	}

	if len(raws) == 0 {
		raws = []string{""}
	}

	var out []agentctl.Profile

	for _, raw := range raws {
		parsed, err := agentctl.ParseProfile(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: agent %w", s.Name, err)
		}

		p, _, ok := parsed.Resolve()
		if !ok {
			return nil, fmt.Errorf("%s: agent %q names no model claude knows", s.Name, raw)
		}

		out = append(out, p)
	}

	return out, nil
}

// reportName is the report file for one run: the scenario's name, the
// profile with its colons as hyphens when it is not the kind's default, and
// the repeat index when there is one, so every run of a set gets its own file.
func reportName(scenario string, p agentctl.Profile, repeat int) string {
	name := scenario
	if !p.IsDefault() {
		name += "." + strings.ReplaceAll(p.Short(), ":", "-")
	}

	if repeat > 0 {
		name += "." + strconv.Itoa(repeat)
	}

	return name + ".json"
}

// resolveDir joins a relative dir to the checkout root, because `just eval`
// runs from cli/ and a path typed at the repo root means the repo root.
func resolveDir(root, dir string) string {
	if filepath.IsAbs(dir) {
		return dir
	}

	return filepath.Join(root, dir)
}

// claudeArgs is the command line for one turn: the profile's flags through
// its kind, beside the prompt mode, the plugin and the budget. A session
// resumes that conversation, for a scenario's later turns.
func claudeArgs(root string, o options, prompt string, p agentctl.Profile, session string) []string {
	args := []string{
		"-p", "--plugin-dir", root, "--output-format", "json",
		"--permission-mode", o.permission, "--max-budget-usd", o.budget,
	}
	args = append(args, p.Args()...)

	if session != "" {
		args = append(args, "--resume", session)
	}

	return append(args, prompt)
}

// droppedEnv is unset for every claude call the runner makes. CLAUDECODE goes
// so a run started inside a session is not refused as nested.
// ANTHROPIC_API_KEY goes so claude uses the subscription, never the API.
var droppedEnv = []string{"CLAUDECODE=", "ANTHROPIC_API_KEY="}

func claudeEnv(environ []string) []string {
	env := make([]string, 0, len(environ))

	for _, kv := range environ {
		if !slices.ContainsFunc(droppedEnv, func(p string) bool { return strings.HasPrefix(kv, p) }) {
			env = append(env, kv)
		}
	}

	return env
}

// runEnv is the run's environment: f10's ledger, run state, asks, daemon
// and user dirs all inside tmp, so the nested run never reaches the user's
// own, its agents map included.
func runEnv(environ []string, tmp string) []string {
	env := claudeEnv(environ)

	return append(env,
		"F10_LEDGER="+filepath.Join(tmp, "f10.db"),
		"F10_STATE_DIR="+filepath.Join(tmp, "state"),
		"F10_INBOX_DIR="+filepath.Join(tmp, "inbox"),
		"F10_RUN_DIR="+filepath.Join(tmp, "run"),
		"F10_USER_DIR="+filepath.Join(tmp, "user"),
	)
}
