// Command f10eval runs f10's prompt scenarios against the real model and
// checks what each run leaves behind. It costs model spend, so it runs on
// demand and before a release (`just eval`), never in CI. Its release reports
// under evals/reports/<version>/ are kept in git, and `f10eval compare` reads
// two report sets side by side.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	Repeat   int       `json:"repeat,omitempty"` // 1-based under -repeat above 1, else 0
	At       time.Time `json:"at"`
	Pass     bool      `json:"pass"`
	Checks   []Check   `json:"checks"`
	CostUSD  float64   `json:"cost_usd"`
	// GraderCostUSD is the rubric check's spend, apart from the run's, so
	// cost_usd compares across releases with and without a rubric.
	GraderCostUSD float64 `json:"grader_cost_usd,omitempty"`
	DurationMS    int64   `json:"duration_ms"`
	// Plumbing is nil for a run with no transcript and for a report from
	// before the field, so compare tells "not measured" from zero.
	Plumbing *Plumbing `json:"plumbing,omitempty"`
	Error    string    `json:"error,omitempty"`
	// RunDir is the run's folder, relative to the report's directory, so a
	// committed report carries no local path.
	RunDir string `json:"run_dir,omitempty"`
	// RunDirError is a failure to write the run folder after the checks ran.
	// It stays apart from Error and Pass, because compare reads those as the
	// run's outcome and a local copy failing says nothing about the agent.
	RunDirError string `json:"run_dir_error,omitempty"`
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
	if sub, ok := subcommands[flag.Arg(0)]; ok {
		if err := sub(os.Stdout, o.root, flag.Args()[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "f10eval %s: %v\n", flag.Arg(0), err)
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

// subcommands follow the flags and share -root, so `just eval compare <a> <b>`
// and `just eval show <report>` arrive as `-root .. <word> <args>`.
var subcommands = map[string]func(io.Writer, string, []string) error{
	"compare": compareMain,
	"html":    htmlMain,
	"show":    showMain,
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
	if err := checkOut(out, o, scenarios); err != nil {
		return err
	}

	type planned struct {
		s       Scenario
		p       agentctl.Profile
		repeat  int
		path    string
		display job
	}

	// every profile resolves before the first run, so a misspelt one fails
	// before any spend
	var plan []planned

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

				name := s.Name
				if repeat > 0 {
					name += fmt.Sprintf(" #%d", repeat)
				}

				plan = append(plan, planned{
					s: s, p: p, repeat: repeat, path: filepath.Join(out, reportName(s.Name, p, repeat)),
					display: job{name: name, profile: cmp.Or(p.Short(), "default")},
				})
			}
		}
	}

	jobs := make([]job, len(plan))
	for i, r := range plan {
		jobs[i] = r.display
	}

	v := newView(os.Stdout, newStyles(os.Stdout), root, jobs)
	failed := 0

	for i, r := range plan {
		v.begin(i)

		folder := strings.TrimSuffix(r.path, ".json")
		rep := runScenario(ctx, root, r.s, o, r.p, runOut{folder: folder, onTurn: v.turn, onEvent: v.event, say: v.say})
		rep.Version, rep.Commit, rep.Repeat = version, commit, r.repeat

		if err := writeReport(r.path, rep); err != nil {
			v.close(out)

			return err
		}

		// the report and the folder are the record, so a page that fails to
		// write leaves the run standing, and `just eval html` rebuilds it
		if rep.RunDir != "" {
			if err := writeRunPage(r.path); err != nil {
				v.say("page: " + err.Error())
			}
		}

		if !rep.Pass {
			failed++
		}

		kept := folder
		if rep.RunDir == "" {
			kept = ""
		}

		v.finish(i, rep, r.path, kept)
	}

	if err := writeSetIndex(out); err != nil {
		v.say("index page: " + err.Error())
	}

	v.close(out)

	if failed > 0 {
		return fmt.Errorf("%d of %d runs failed", failed, len(plan))
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

// runScenario runs one scenario, and with out.folder keeps its run folder
// beside the report.
func runScenario(ctx context.Context, root string, s Scenario, o options, p agentctl.Profile, out runOut) Report {
	if out.folder == "" {
		return runIn(ctx, root, s, o, p, out, nil)
	}

	rep := Report{Scenario: s.Name, Agent: p.Short(), At: time.Now().UTC()}

	// the folder opens before setup, so a run that errors in setup still
	// keeps its inputs and the cause can be read there
	folder, err := openRunFolder(out.folder)
	if err != nil {
		rep.Error = "run folder: " + err.Error()

		return rep
	}

	scenarios := filepath.Join(root, "evals", "scenarios")
	if err := folder.inputs(
		filepath.Join(scenarios, s.Name),
		filepath.Join(scenarios, cmp.Or(s.Fixture, s.Name), "repo"),
		s.Task,
	); err != nil {
		rep.Error = "run folder: " + err.Error()
	} else {
		rep = runIn(ctx, root, s, o, p, out, folder)
	}

	rep.RunDir = filepath.Base(out.folder)

	if err := folder.close(); err != nil && rep.RunDirError == "" {
		rep.RunDirError = err.Error()
	} else if err == nil {
		// after the close, because only it flushes the transcript
		rep.Plumbing = folderPlumbing(out.folder)
	}

	return rep
}

// runIn points f10's ledger, run state, asks and daemon dirs inside the temp
// dir, so the nested run never reaches the user's own. The repo has no origin,
// so nothing can be pushed. A non-nil folder keeps what the run saw and left.
func runIn(
	ctx context.Context,
	root string,
	s Scenario,
	o options,
	p agentctl.Profile,
	out runOut,
	folder *runFolder,
) Report {
	rep := Report{Scenario: s.Name, Agent: p.Short(), At: time.Now().UTC()}

	scenarios := filepath.Join(root, "evals", "scenarios")
	fixture := filepath.Join(scenarios, cmp.Or(s.Fixture, s.Name), "repo")

	tmp, err := os.MkdirTemp("", "f10eval-"+s.Name+"-")
	if err != nil {
		rep.Error = err.Error()

		return rep
	}

	if o.keep {
		out.print("kept: " + tmp)
	} else {
		defer os.RemoveAll(tmp)
	}

	dir := filepath.Join(tmp, "repo")

	if err := prepare(ctx, fixture, dir); err != nil {
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

	var tr *transcript

	if folder != nil {
		if err := folder.before(dir); err != nil {
			rep.Error = "run folder: " + err.Error()

			return rep
		}

		tr = folder.tr
	}

	s.env = runEnv(os.Environ(), tmp)
	tr.begin()

	turns, session := s.turns(), ""

	for i, prompt := range turns {
		if out.onTurn != nil {
			out.onTurn(prompt)
		}

		res, err := runTurn(ctx, dir, s.env, claudeArgs(root, o, prompt, p, session), tr, out.onEvent)
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

	if folder != nil {
		if err := folder.after(ctx, dir); err != nil {
			rep.RunDirError = err.Error()
		}
	}

	return rep
}

// runOut is where a run reports besides its report: the folder it keeps,
// the view its events feed and the lines it prints. The zero value keeps no
// folder and prints to stdout.
type runOut struct {
	folder  string
	onTurn  func(prompt string)
	onEvent func(event)
	say     func(string)
}

func (o runOut) print(line string) {
	if o.say != nil {
		o.say(line)

		return
	}

	fmt.Println(line)
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

// reportName is the report file for one run. A profile other than the kind's
// default adds its name with colons as hyphens. A repeat index adds itself, so
// every run of a set gets its own file.
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

// checkOut refuses a run whose set would mix with reports already in -out,
// because a compare reads every file there and a stale repeat counts as a run.
// Repeats need -out, so they never land beside a committed release report.
// Without -out, the release dir is overwritten in place.
func checkOut(out string, o options, scenarios []Scenario) error {
	if o.repeat > 1 && o.out == "" {
		return errors.New("-repeat above 1 needs -out, so its set stays apart from the release reports")
	}

	if o.out == "" {
		return nil
	}

	for _, s := range scenarios {
		old, _ := filepath.Glob(filepath.Join(out, s.Name+".*json"))
		for _, f := range old {
			if r, err := readReport(f); err == nil && r.Scenario == s.Name {
				return fmt.Errorf("%s already holds reports of %s: pick a fresh -out or clear it", out, s.Name)
			}
		}
	}

	return nil
}

func readReport(path string) (Report, error) {
	var r Report

	body, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}

	err = json.Unmarshal(body, &r)

	return r, err
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
		"-p", "--plugin-dir", root, "--output-format", "stream-json", "--verbose",
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
