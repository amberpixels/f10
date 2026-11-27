package main

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/storage"
)

// A Check is one assertion on what a scenario's run left in its repo. Every
// check reads artifacts by rule, except rubric, where a model grades wording.
type Check struct {
	Name    string        `json:"name"`
	Pass    bool          `json:"pass"`
	Detail  string        `json:"detail,omitempty"`
	Rubric  *RubricResult `json:"rubric,omitempty"`
	Planted *PlantedCount `json:"planted,omitempty"`
}

// A PlantedCount is the planted check's tally as numbers, so a comparison of
// report sets reads it without parsing the detail.
type PlantedCount struct {
	Found   int `json:"found"`
	Total   int `json:"total"`
	Outside int `json:"outside"`
}

var checks = map[string]func(ctx context.Context, dir string, s Scenario) Check{
	"plan":     checkPlan,
	"intent":   checkIntent,
	"accept":   checkAccept,
	"verify":   checkVerify,
	"commit":   checkCommit,
	"findings": checkFindings,
	"planted":  checkPlanted,
	"verdict":  checkVerdict,
	"lore":     checkLore,
	"history":  checkHistory,
	"rubric":   checkRubric,
	"captured": checkCaptured,
}

func result(name string, problems []string) Check {
	return Check{Name: name, Pass: len(problems) == 0, Detail: strings.Join(problems, "; ")}
}

func checkPlan(_ context.Context, dir string, s Scenario) Check {
	body, err := os.ReadFile(storage.InRepo(dir).Plan(s.Task))
	if err != nil {
		return result("plan", []string{err.Error()})
	}

	text := string(body)

	var problems []string

	for _, want := range []struct{ what, re string }{
		{"a Roles: line", `(?m)^Roles:`},
		{"a stage", `(?m)^#{2,4} .*\S`},
		{"## Consequences", `(?m)^## Consequences`},
		{"## Gaps", `(?m)^## Gaps`},
	} {
		if !regexp.MustCompile(want.re).MatchString(text) {
			problems = append(problems, "no "+want.what)
		}
	}

	return result("plan", problems)
}

// checkHistory passes when the plan has a ## History section naming every
// id the scenario's seeded history should have surfaced.
func checkHistory(_ context.Context, dir string, s Scenario) Check {
	body, err := os.ReadFile(storage.InRepo(dir).Plan(s.Task))
	if err != nil {
		return result("history", []string{err.Error()})
	}

	_, section, found := strings.Cut("\n"+string(body), "\n## History\n")
	if !found {
		return result("history", []string{"no ## History section"})
	}

	section, _, _ = strings.Cut(section, "\n## ")

	var problems []string

	for _, id := range s.HistoryCites {
		if !strings.Contains(section, id) {
			problems = append(problems, "## History does not cite "+id)
		}
	}

	return result("history", problems)
}

func checkVerify(ctx context.Context, dir string, s Scenario) Check {
	var problems []string

	for _, command := range s.Verify {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v: %s", command, err, lastLine(string(out))))
		}
	}

	return result("verify", problems)
}

// checkAccept proves the feature exists: verify alone passes on an untouched
// fixture.
func checkAccept(ctx context.Context, dir string, s Scenario) Check {
	problems := []string{}
	if len(s.Accept) == 0 {
		problems = append(problems, "the scenario names no accept command")
	}

	for _, a := range s.Accept {
		cmd := exec.CommandContext(ctx, "sh", "-c", a.Run) //nolint:gosec // the scenario's own accept command
		cmd.Dir = dir

		out, err := cmd.Output()
		if got := strings.TrimSpace(string(out)); err != nil || got != a.Want {
			problems = append(problems, fmt.Sprintf("%s: got %q (%v), want %q", a.Run, got, err, a.Want))
		}
	}

	return result("accept", problems)
}

func checkCommit(ctx context.Context, dir string, _ Scenario) Check {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-list", "--all", "--count").Output()
	if err != nil {
		return result("commit", []string{err.Error()})
	}

	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	if n < 2 {
		return result("commit", []string{"no commit past the initial one"})
	}

	return result("commit", nil)
}

var whereRE = regexp.MustCompile(`^where:\s*(\S+?):(\d+)`)

func checkFindings(_ context.Context, dir string, s Scenario) Check {
	found, files := findingWheres(dir, s)
	if len(files) == 0 {
		return result("findings", []string{"no findings file"})
	}

	var problems []string

	for _, w := range found {
		if lines, err := countLines(filepath.Join(dir, w.file)); err != nil || w.line < 1 || w.line > lines {
			problems = append(problems, fmt.Sprintf("%s: where: %s:%d does not resolve", w.round, w.file, w.line))
		}
	}

	return result("findings", problems)
}

// defaultMaxExtra is how many findings a review scenario allows outside its
// planted defects when it names no max_extra: a real nit is not noise.
const defaultMaxExtra = 2

// checkPlanted passes when every planted defect has a finding in its line
// range, and the findings outside every range stay within max_extra, the
// scenario's bound on false positives.
func checkPlanted(_ context.Context, dir string, s Scenario) Check {
	found, files := findingWheres(dir, s)
	if len(files) == 0 {
		c := result("planted", []string{"no findings file"})
		c.Planted = &PlantedCount{Total: len(s.Planted)}

		return c
	}

	var problems []string

	for _, p := range s.Planted {
		if !slices.ContainsFunc(found, p.covers) {
			problems = append(problems, fmt.Sprintf("missed %s:%d-%d (%s)", p.File, p.From, p.To, p.What))
		}
	}

	missed := len(problems)

	extra := 0

	for _, w := range found {
		if !slices.ContainsFunc(s.Planted, func(p Planted) bool { return p.covers(w) }) {
			extra++
		}
	}

	limit := defaultMaxExtra
	if s.MaxExtra != nil {
		limit = *s.MaxExtra
	}

	if extra > limit {
		problems = append(problems,
			fmt.Sprintf("%d findings outside the planted defects, at most %d allowed", extra, limit))
	}

	// the counts go in the detail on a pass too, so two reports compare
	problems = append(problems,
		fmt.Sprintf("%d of %d planted found, %d outside", len(s.Planted)-missed, len(s.Planted), extra))
	c := result("planted", problems)
	c.Pass = len(problems) == 1
	c.Planted = &PlantedCount{Found: len(s.Planted) - missed, Total: len(s.Planted), Outside: extra}

	return c
}

func (p Planted) covers(w where) bool {
	return w.file == p.File && w.line >= p.From && w.line <= p.To
}

// A where is one finding's where: line, and the round file it came from.
type where struct {
	round, file string
	line        int
}

// findingWheres reads the review's findings files: the task's review rounds,
// or the one file a plain arm's prompt names.
func findingWheres(dir string, s Scenario) ([]where, []string) {
	glob := filepath.Join(storage.InRepo(dir).Reviews(s.Task), "*.md")
	if plainArm(s.arm) {
		glob = filepath.Join(dir, plainFindings)
	}

	files, _ := filepath.Glob(glob)

	var found []where

	for _, f := range files {
		eachLine(f, func(line string) {
			if m := whereRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				n, _ := strconv.Atoi(m[2])
				found = append(found, where{round: filepath.Base(f), file: m[1], line: n})
			}
		})
	}

	return found, files
}

var verdicts = []string{"proceed", "proceed with changes", "rethink", "stop"}

func checkVerdict(_ context.Context, dir string, s Scenario) Check {
	files, _ := filepath.Glob(filepath.Join(storage.InRepo(dir).Verdicts(s.Task), "*.md"))
	if len(files) == 0 {
		return result("verdict", []string{"no verdict file"})
	}

	var problems []string

	// the newest file is the highest n, which a lexical glob misorders past 9
	slices.SortFunc(files, func(a, b string) int { return cmp.Compare(verdictNumber(a), verdictNumber(b)) })

	last := ""

	for _, f := range files {
		found := ""

		eachLine(f, func(line string) {
			if v, ok := strings.CutPrefix(line, "verdict: "); ok && found == "" {
				found = strings.TrimSpace(v)
			}
		})

		if !slices.Contains(verdicts, found) {
			problems = append(problems, fmt.Sprintf("%s: verdict %q", filepath.Base(f), found))
		}

		last = found
	}

	if s.WantVerdict != "" && last != s.WantVerdict {
		problems = append(problems, fmt.Sprintf("verdict %q, want %q", last, s.WantVerdict))
	}

	return result("verdict", problems)
}

func verdictNumber(path string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(path), ".md"))

	return n
}

// Captured is the shape a capture run's tasks must have. It counts the
// tasks made, the parents and subtasks among them, and those with After:.
type Captured struct {
	Tasks    int `json:"tasks"`
	Parents  int `json:"parents"`
	Subtasks int `json:"subtasks"`
	After    int `json:"after"`
}

var (
	parentRE = regexp.MustCompile(`^Parent:\s*([A-Z]+-\d+)`)
	afterRE  = regexp.MustCompile(`^After:\s*\[?([A-Z]+-\d+)`)
)

// A capturedTask is one task file the fixture driver wrote.
type capturedTask struct {
	id, parent, after string
}

// checkCaptured reads the task files the run added to the fixture tracker,
// never the tracker through f10, so it does not depend on the binary under
// test. A dependent must wait on a sibling that waits on nothing, which is
// what makes a split three levels deep.
func checkCaptured(ctx context.Context, dir string, s Scenario) Check {
	if s.Captured == nil {
		return result("captured", []string{"the scenario has no captured field"})
	}

	tasks, err := capturedTasks(ctx, dir)
	if err != nil {
		return result("captured", []string{err.Error()})
	}

	byID := map[string]capturedTask{}
	parents := map[string]bool{}
	subtasks, after := 0, 0

	for _, c := range tasks {
		byID[c.id] = c

		if c.parent != "" {
			parents[c.parent] = true
			subtasks++
		}

		if c.after != "" {
			after++
		}
	}

	var problems []string

	want, got := *s.Captured, Captured{Tasks: len(tasks), Parents: len(parents), Subtasks: subtasks, After: after}
	if got != want {
		problems = append(problems, "want "+describeCaptured(want))
	}

	for _, c := range tasks {
		if c.parent != "" && byID[c.parent].id == "" {
			problems = append(problems, fmt.Sprintf("%s: parent %s is not a task the run made", c.id, c.parent))
		}

		if c.after == "" {
			continue
		}

		base, ok := byID[c.after]

		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: After: %s is not a task the run made", c.id, c.after))
		case base.parent != c.parent:
			problems = append(problems, fmt.Sprintf("%s: After: %s sits under another parent", c.id, c.after))
		case base.after != "":
			problems = append(problems,
				fmt.Sprintf("%s: After: %s, which waits on %s itself", c.id, c.after, base.after))
		}
	}

	// what was found goes in the detail on a pass too, so two reports compare
	problems = append(problems, "found "+describeCaptured(got))
	c := result("captured", problems)
	c.Pass = len(problems) == 1

	return c
}

func describeCaptured(c Captured) string {
	return fmt.Sprintf("%d tasks, %d parents, %d subtasks, %d After:", c.Tasks, c.Parents, c.Subtasks, c.After)
}

// capturedTasks is every tasks/*.md the fixture's initial commit did not
// hold. The initial commit, not the index, because a ship later in the
// same run may commit the task files.
func capturedTasks(ctx context.Context, dir string) ([]capturedTask, error) {
	fixture := map[string]bool{}

	out, err := git(ctx, dir, "log", "--max-parents=0", "--format=", "--name-only", "--", "tasks").Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	for name := range strings.FieldsSeq(string(out)) {
		fixture[filepath.Base(name)] = true
	}

	files, _ := filepath.Glob(filepath.Join(dir, "tasks", "*.md"))

	var tasks []capturedTask

	for _, f := range files {
		if fixture[filepath.Base(f)] {
			continue
		}

		c := capturedTask{id: strings.TrimSuffix(filepath.Base(f), ".md")}

		eachLine(f, func(line string) {
			if m := parentRE.FindStringSubmatch(line); m != nil && c.parent == "" {
				c.parent = m[1]
			}

			if m := afterRE.FindStringSubmatch(line); m != nil {
				c.after = m[1]
			}
		})

		tasks = append(tasks, c)
	}

	return tasks, nil
}

// checkIntent checks presence only; the schema is checkLore's.
func checkIntent(_ context.Context, dir string, s Scenario) Check {
	files, _ := filepath.Glob(filepath.Join(storage.InRepo(dir).Lore(), s.Task, "*-intent.md"))
	if len(files) == 0 {
		return result("intent", []string{"no intent entry"})
	}

	return result("intent", nil)
}

func checkLore(ctx context.Context, dir string, s Scenario) Check {
	f10 := cmp.Or(s.f10, "f10")
	cmd := exec.CommandContext(ctx, f10, "-C", dir, "lore", "validate", s.Task)
	cmd.Env = s.env

	if out, err := cmd.CombinedOutput(); err != nil {
		return result("lore", []string{fmt.Sprintf("%v: %s", err, lastLine(string(out)))})
	}

	return result("lore", nil)
}

func eachLine(path string, fn func(string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}

	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fn(sc.Text())
	}
}

func countLines(path string) (int, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	return strings.Count(string(body), "\n") + 1, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")

	return lines[len(lines)-1]
}
