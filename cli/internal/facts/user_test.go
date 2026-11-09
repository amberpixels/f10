package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
)

// buildWithUser is buildWith with the user's files: each name to its
// frontmatter, written into a fresh user dir.
func buildWithUser(t *testing.T, main string, files map[string]string) (*Effective, string) {
	t.Helper()

	dir := t.TempDir()
	for name, block := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("---\n"+block+"---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	lay := &layout.Layout{Project: "demo", StorageRoot: "/repo/.f10", Layering: layout.LayeringExtends}
	lay.Layers = append(lay.Layers, layout.Layer{Label: "main", Dir: dirWithProjectMD(t, main)})

	return Build(lay, &probe.Probes{CLI: probe.Signal{Value: "gh"}}, dir), dir
}

// The user's files lie under the project's, key by key, each key naming
// the layer that set it; a default pace only the user names still applies.
func TestUserLayerUnderTheProject(t *testing.T) {
	eff, dir := buildWithUser(t,
		"---\nflows:\n  default: plan judge implement pr\nagents:\n  judge: opus:max\n"+
			"paces:\n  brisk: { judge: { effort: high } }\n---\n",
		map[string]string{
			UserAgentsFile: "agents:\n  default: sonnet\n  judge: haiku\n  review: opus\n",
			UserPacesFile:  "paces:\n  brisk: { judge: off, review: { rounds: 1 }, finish: off }\npace: brisk\n",
		})
	if eff.Errors() != "" {
		t.Fatalf("errors: %s", eff.Errors())
	}

	agents, paces := filepath.Join(dir, UserAgentsFile), filepath.Join(dir, UserPacesFile)

	for key, want := range map[string]string{
		"agents.default":      "sonnet @ declared (user, " + agents + ")",
		"agents.judge":        "opus:max @ declared (main, frontmatter)",
		"agents.review":       "opus @ declared (user, " + agents + ")",
		"paces.brisk.judge":   "effort high @ declared (main, frontmatter)",
		"paces.brisk.review":  "rounds 1 @ declared (user, " + paces + ")",
		"paces.brisk.finish":  "off @ declared (user, " + paces + ")",
		"paces.fast.finish":   "on @ built-in",
		"pace":                "brisk @ declared (user, " + paces + ")",
		"paces.medium.finish": "on @ built-in",
		"paces.ultra.plan":    "off @ built-in",
		"paces.slow.review":   "by local+ci?, rounds 2 @ built-in",
		"paces.fast.judge":    "effort low @ built-in",
		"paces.ultra.pr":      "marker fast @ built-in",
		"paces.ultra.review":  "by local, depth light @ built-in",
		"paces.fast.review":   "by local, rounds 1 @ built-in",
		"paces.slow.finish":   "off @ built-in",
		"paces.ultra.finish":  "on @ built-in",
		"paces.ultra.cleanup": "off @ built-in",
		"paces.ultra.judge":   "off @ built-in",
		"paces.ultra.demo":    "off @ built-in",
	} {
		if f := factFor(t, eff, key); f.Value+" @ "+f.Origin.String() != want {
			t.Errorf("%s = %s @ %s, want %s", key, f.Value, f.Origin, want)
		}
	}

	if eff.PaceSet.Default != "brisk" || eff.PaceSet.Paces["brisk"].Steps["judge"].Off {
		t.Errorf("pace set: default %q, brisk %+v", eff.PaceSet.Default, eff.PaceSet.Paces["brisk"])
	}
}

// A refused user file is one problem naming the file, and adds nothing.
func TestUserFileRefused(t *testing.T) {
	for name, tc := range map[string]struct{ file, block, want, key string }{
		"the run's key":  {UserAgentsFile, "agents:\n  --agent: opus\n", "agents.--agent", "agents.--agent"},
		"a bad profile":  {UserAgentsFile, "agents:\n  review: opus:huge\n", "agents.review", "agents.review"},
		"implement off":  {UserPacesFile, "paces:\n  bad: { implement: off }\n", "implement cannot be off", "paces.bad"},
		"a ceiling":      {UserPacesFile, "ceiling: { finish: off }\n", "a ceiling is the project's", "ceiling.finish"},
		"an unknown key": {UserPacesFile, "pacez: {}\n", "field pacez not found", "pacez"},
	} {
		eff, dir := buildWithUser(t, "---\nflows:\n  default: plan implement pr\n---\n",
			map[string]string{tc.file: tc.block})

		var hits int
		for _, p := range eff.Problems {
			if p.Error && strings.Contains(p.Message, filepath.Join(dir, tc.file)) {
				hits++

				if !strings.Contains(p.Message, tc.want) {
					t.Errorf("%s: problem %q, want %q", name, p.Message, tc.want)
				}
			}
		}

		if hits != 1 {
			t.Errorf("%s: %d problems naming the file in %+v", name, hits, eff.Problems)
		}

		for _, f := range eff.Facts {
			if strings.HasPrefix(f.Key, tc.key) {
				t.Errorf("%s: the refused file still set %s", name, f.Key)
			}
		}
	}
}

// A pace that breaks a flow is reported once, after every layer merged.
func TestPaceProblemReportedOnce(t *testing.T) {
	eff, _ := buildWithUser(t,
		"---\nflows:\n  default: plan judge implement pr\npaces:\n  lazy: { pr: { marker: fast } }\n---\n",
		map[string]string{UserPacesFile: "paces:\n  lazy: { plan: off }\n"})

	var hits int
	for _, p := range eff.Problems {
		if strings.Contains(p.Message, "pace lazy") {
			hits++
		}
	}

	if hits != 1 {
		t.Errorf("%d problems for pace lazy in %+v", hits, eff.Problems)
	}
}

// A default pace no layer declares is an error.
func TestDefaultPaceUnknown(t *testing.T) {
	eff, _ := buildWithUser(t, "---\nflows:\n  default: plan implement pr\n---\n",
		map[string]string{UserPacesFile: "pace: brisk\n"})

	if !strings.Contains(eff.Errors(), `pace: no pace "brisk"`) {
		t.Errorf("errors = %q", eff.Errors())
	}
}

// A pace reads back as it was written, finish beside the step dials.
func TestPaceRoundTrip(t *testing.T) {
	var p Pace
	if err := yaml.Unmarshal([]byte("{ review: { by: local, rounds: 2 }, plan: off, finish: off }"), &p); err != nil {
		t.Fatal(err)
	}

	out, err := yaml.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}

	var back Pace
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}

	if back.Finish != "off" || !back.Steps["plan"].Off || back.Steps["review"].Rounds != 2 ||
		back.Steps["review"].By != "local" || len(back.Steps) != 2 {
		t.Errorf("round trip = %+v from %s", back, out)
	}
}

// Every dial's step, check and order come from its row.
func TestDialTable(t *testing.T) {
	for _, s := range dials {
		var d Dials

		v := "x"
		if len(s.order) > 0 {
			v = s.order[len(s.order)-1]
		}

		if s.name == "by" {
			v = "local"
		}

		s.set(&d, v)

		if got := s.get(d); got != v {
			t.Errorf("%s: set %q, got %q", s.name, v, got)
		}

		if s.check(v) != "" {
			t.Errorf("%s: %q refused: %s", s.name, v, s.check(v))
		}

		bad := CheckPace("p", Pace{Steps: map[string]Dials{"elsewhere": d}})
		if len(bad) == 0 || !strings.Contains(strings.Join(bad, ";"), s.name+" is a "+s.step+" dial") {
			t.Errorf("%s off its step: %v", s.name, bad)
		}

		capped := CheckCeiling(Ceiling{Pace: Pace{Steps: map[string]Dials{s.step: d}}})
		if refused := strings.Contains(strings.Join(capped, ";"), "no order"); refused != (s.order == nil) {
			t.Errorf("%s: ceiling refusal %v, order %v", s.name, capped, s.order)
		}
	}
}
