package layout

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A run reads only the steps f10-load-project.sh prints, so a step a skill
// follows, or one a step defers to ("as steps/commit.md point 2 says"), runs
// without its text unless every load of the caller names it too. This pins
// skills/ and steps/ to their load calls.

var (
	// a load call naming steps; `<step>` placeholders and --overlays print no step
	loadCallRe = regexp.MustCompile("f10-load-project\\.sh((?: [a-z]+)+)[`;]")
	stepRefRe  = regexp.MustCompile("`steps/([a-z]+)\\.md`")
	boldRe     = regexp.MustCompile(`\*\*([a-z]+)\*\*`)
	pointRe    = regexp.MustCompile(`^\s*points?\b`)
)

// The ship skill loads each flow entry at run time, named in project.md, so its
// loads are prose, not calls: this mirrors skills/ship/SKILL.md Run points 1 and 3.
var (
	pipelineSteps = []string{
		"implement",
		"cleanup",
		"commit",
		"push",
		"pr",
		"review",
		"resolve",
		"judge",
		"demo",
		"deploy",
		"catchup",
	}
	shipLoads = func() [][]string {
		loads := [][]string{{"capture", "fetch", "plan"}, {"fetch", "plan"}, {"review", "resolve"}}
		for _, s := range pipelineSteps {
			// a review entry loads both, and resolve is never named
			if s != "review" && s != "resolve" {
				loads = append(loads, []string{s})
			}
		}
		// the catchup guard joins the load of each entry it guards
		for _, s := range []string{"implement", "pr", "judge"} {
			loads = append(loads, []string{"catchup", s})
		}
		return loads
	}()
)

type loadSite struct {
	where string
	steps []string
}

type mdLine struct {
	where string
	text  string
}

// loadCalls returns the step names of each load call on a line.
func loadCalls(line string) [][]string {
	var calls [][]string
	for _, m := range loadCallRe.FindAllStringSubmatch(line, -1) {
		calls = append(calls, strings.Fields(m[1]))
	}

	return calls
}

// delegations returns the steps a line defers to: a `steps/X.md` mention
// followed by "point" or "points", or preceded by "as ". A plain mention
// ("the local alternative to steps/pr.md") is not one.
func delegations(line string) []string {
	var refs []string
	for _, m := range stepRefRe.FindAllStringSubmatchIndex(line, -1) {
		before, after := line[:m[0]], line[m[1]:]
		if pointRe.MatchString(after) || strings.HasSuffix(before, "as ") {
			refs = append(refs, line[m[2]:m[3]])
		}
	}

	return refs
}

// boldSteps returns the step names a line sets in bold, the form a skill
// names a step to follow in ("Follow the **review** step").
func boldSteps(line string, steps []string) []string {
	var names []string
	for _, m := range boldRe.FindAllStringSubmatch(line, -1) {
		if slices.Contains(steps, m[1]) {
			names = append(names, m[1])
		}
	}

	return names
}

func readLines(t *testing.T, root, rel string) []mdLine {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}

	var lines []mdLine
	for i, text := range strings.Split(string(raw), "\n") {
		lines = append(lines, mdLine{where: fmt.Sprintf("%s:%d", rel, i+1), text: text})
	}

	return lines
}

func TestStepReferences(t *testing.T) {
	root := filepath.Join(filepath.Dir(scriptPath(t)), "..")

	stepFiles, err := filepath.Glob(filepath.Join(root, "steps", "*.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var steps []string
	for _, f := range stepFiles {
		steps = append(steps, strings.TrimSuffix(filepath.Base(f), ".md"))
	}

	skillFiles, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	files := map[string][]mdLine{}
	for _, s := range steps {
		rel := filepath.Join("steps", s+".md")
		files[rel] = readLines(t, root, rel)
	}

	skillLoads := map[string][]string{}
	for _, f := range skillFiles {
		rel, _ := filepath.Rel(root, f)
		files[rel] = readLines(t, root, rel)
		skillLoads[rel] = nil
	}

	const ship = "skills/ship/SKILL.md"

	var sites []loadSite
	for _, s := range shipLoads {
		sites = append(sites, loadSite{where: ship + " (flow entry load)", steps: s})
	}

	for rel, lines := range files {
		for _, l := range lines {
			for _, call := range loadCalls(l.text) {
				sites = append(sites, loadSite{where: l.where, steps: call})
				if _, ok := skillLoads[rel]; ok {
					skillLoads[rel] = append(skillLoads[rel], call...)
				}
			}
		}
	}

	for _, s := range shipLoads {
		skillLoads[ship] = append(skillLoads[ship], s...)
	}

	var shipText strings.Builder
	for _, l := range files[ship] {
		shipText.WriteString(l.text + "\n")
	}

	if !strings.Contains(shipText.String(), "loads `review resolve`") {
		t.Errorf("%s no longer says a review entry loads `review resolve`: update shipLoads with it", ship)
	}

	// every name a load or a delegation uses has a step file
	for _, site := range sites {
		for _, s := range site.steps {
			if !slices.Contains(steps, s) {
				t.Errorf("%s: loads %q, which has no file in steps/", site.where, s)
			}
		}
	}

	for _, lines := range files {
		for _, l := range lines {
			for _, m := range stepRefRe.FindAllStringSubmatch(l.text, -1) {
				if !slices.Contains(steps, m[1]) {
					t.Errorf("%s: names steps/%s.md, which does not exist", l.where, m[1])
				}
			}
		}
	}

	// a skill loads every step it follows
	for rel, loads := range skillLoads {
		for _, l := range files[rel] {
			for _, s := range boldSteps(l.text, steps) {
				if rel == ship && slices.Contains(pipelineSteps, s) {
					continue
				}

				if !slices.Contains(loads, s) {
					t.Errorf(
						"%s: follows the %s step, but the skill loads %v",
						l.where,
						s,
						slices.Compact(slices.Sorted(slices.Values(loads))),
					)
				}
			}

			for _, s := range delegations(l.text) {
				if !slices.Contains(loads, s) {
					t.Errorf(
						"%s: defers to %s, but the skill loads %v",
						l.where,
						s,
						slices.Compact(slices.Sorted(slices.Values(loads))),
					)
				}
			}
		}
	}

	// every load of a step also loads each step it defers to
	for _, step := range steps {
		for _, l := range files[filepath.Join("steps", step+".md")] {
			for _, s := range delegations(l.text) {
				if s == step {
					continue
				}

				for _, site := range sites {
					if slices.Contains(site.steps, step) && !slices.Contains(site.steps, s) {
						t.Errorf("%s: defers to %s, but %s loads %v", l.where, s, site.where, site.steps)
					}
				}
			}
		}
	}
}

func TestStepReferenceExtractors(t *testing.T) {
	steps := []string{"commit", "judge", "review"}

	cases := []struct {
		line  string
		loads [][]string
		refs  []string
		bold  []string
	}{
		{line: "commit as `steps/commit.md` point 2 says", refs: []string{"commit"}},
		{line: "discuss as brainstorm does (`steps/judge.md` points 3 to 6)", refs: []string{"judge"}},
		{line: "detect as `steps/review.md` does", refs: []string{"review"}},
		{line: "The local alternative to `steps/commit.md` and `steps/judge.md`:", refs: nil},
		{line: "(`steps/review.md` → **As a guard**)", refs: nil},
		{line: "1. Run `f10-load-project.sh review resolve` first", loads: [][]string{{"review", "resolve"}}},
		{line: "`f10-state.sh set plan running judge; f10-load-project.sh judge`, then", loads: [][]string{{"judge"}}},
		{line: "`f10-state.sh set ship running <entry>; f10-load-project.sh <step>`", loads: nil},
		{line: "`(cd <dir> && f10-load-project.sh --overlays standup)`", loads: nil},
		{line: "Run `f10-load-project.sh` first", loads: nil},
		{line: "2. Follow the **review** step it prints, then **Arguments**", bold: []string{"review"}},
	}

	for _, c := range cases {
		if got := loadCalls(c.line); !slices.EqualFunc(got, c.loads, slices.Equal) {
			t.Errorf("loadCalls(%q) = %v, want %v", c.line, got, c.loads)
		}

		if got := delegations(c.line); !slices.Equal(got, c.refs) {
			t.Errorf("delegations(%q) = %v, want %v", c.line, got, c.refs)
		}

		if got := boldSteps(c.line, steps); !slices.Equal(got, c.bold) {
			t.Errorf("boldSteps(%q) = %v, want %v", c.line, got, c.bold)
		}
	}
}
