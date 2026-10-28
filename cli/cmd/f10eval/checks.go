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
)

// A Check is one assertion on what a scenario's run left in its repo:
// artifacts, never wording.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// checks maps a scenario's check names to what they assert. dir is the
// fixture's repo after the run.
var checks = map[string]func(ctx context.Context, dir string, s Scenario) Check{
	"plan":     checkPlan,
	"intent":   checkIntent,
	"accept":   checkAccept,
	"verify":   checkVerify,
	"commit":   checkCommit,
	"findings": checkFindings,
	"verdict":  checkVerdict,
	"lore":     checkLore,
}

func result(name string, problems []string) Check {
	return Check{Name: name, Pass: len(problems) == 0, Detail: strings.Join(problems, "; ")}
}

// checkPlan: the plan file exists with its Roles line, a stage, and the
// Consequences and Gaps sections.
func checkPlan(_ context.Context, dir string, s Scenario) Check {
	body, err := os.ReadFile(filepath.Join(dir, ".f10", "plans", s.Task+".md"))
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

// checkVerify: every verify command the scenario names passes on the result.
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

// checkAccept: every acceptance command prints what it must. Verify alone
// passes on an untouched fixture; this is what proves the feature exists.
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

// checkCommit: the flow left a commit past the fixture's initial one.
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

// checkFindings: every review finding's where: line names a file that
// exists and a line it has.
func checkFindings(_ context.Context, dir string, s Scenario) Check {
	files, _ := filepath.Glob(filepath.Join(dir, ".f10", "reviews", s.Task, "*.md"))
	if len(files) == 0 {
		return result("findings", []string{"no findings file"})
	}

	var problems []string

	for _, f := range files {
		eachLine(f, func(line string) {
			m := whereRE.FindStringSubmatch(strings.TrimSpace(line))
			if m == nil {
				return
			}

			n, _ := strconv.Atoi(m[2])
			if lines, err := countLines(filepath.Join(dir, m[1])); err != nil || n < 1 || n > lines {
				problems = append(problems, fmt.Sprintf("%s: where: %s:%d does not resolve", filepath.Base(f), m[1], n))
			}
		})
	}

	return result("findings", problems)
}

var verdicts = []string{"proceed", "proceed with changes", "rethink", "stop"}

// checkVerdict: judge wrote a verdict file with a verdict the hook parses.
func checkVerdict(_ context.Context, dir string, s Scenario) Check {
	files, _ := filepath.Glob(filepath.Join(dir, ".f10", "verdicts", s.Task, "*.md"))
	if len(files) == 0 {
		return result("verdict", []string{"no verdict file"})
	}

	var problems []string

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
	}

	return result("verdict", problems)
}

// checkIntent: plan wrote the change's intent, the lore entry show and why
// print first. Presence is the check; the schema is checkLore's.
func checkIntent(_ context.Context, dir string, s Scenario) Check {
	files, _ := filepath.Glob(filepath.Join(dir, ".f10", "lore", s.Task, "*-intent.md"))
	if len(files) == 0 {
		return result("intent", []string{"no intent entry"})
	}

	return result("intent", nil)
}

// checkLore: the change's lore entries pass f10's own schema lint.
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
