package facts

import (
	"errors"
	"strings"
	"testing"
)

func decodePaces(t *testing.T, yml string) Declared {
	t.Helper()

	d, err := decodeDeclared([]byte(yml))
	if err != nil {
		t.Fatalf("decode %q: %v", yml, err)
	}

	return d
}

// Each shorthand reads as the dials it stands for.
func TestPaceShorthands(t *testing.T) {
	d := decodePaces(t, "paces:\n  quick: { judge: off, review: ci, finish: on, pr: { marker: fast } }\npace: quick\n")
	p := d.Paces["quick"]

	if !p["judge"].Off || p["review"].By != "ci" || p["finish"].Finish != "on" || p["pr"].Marker != "fast" {
		t.Errorf("quick = %+v", p)
	}

	if d.Pace != "quick" {
		t.Errorf("pace = %q", d.Pace)
	}

	if bad := d.check(); len(bad) > 0 {
		t.Errorf("check = %v", bad)
	}
}

func TestPaceRefusals(t *testing.T) {
	for yml, want := range map[string]string{
		"paces:\n  x: { implement: off }\n":          "implement cannot be off",
		"paces:\n  x: { judge: { rounds: 2 } }\n":    "rounds is a review dial",
		"paces:\n  x: { review: { by: remote } }\n":  "not local, host, ci, local+ci, ci? or local+ci?",
		"paces:\n  x: { review: { rounds: 3 } }\n":   "rounds 3 is not 1 or 2",
		"paces:\n  x: { review: { depth: deep } }\n": "depth deep is not full or light",
		"paces:\n  x: { judge: { effort: opus } }\n": "not an effort word",
		"paces:\n  x: { finish: yes }\n":             "finish is on or off",
		"paces:\n  x: { finish: { by: local } }\n":   "finish is on or off",
		"paces:\n  x: { catchup: off }\n":            "catchup is never a flow entry",
		"paces:\n  x: { judge: low }\n":              `"low" is not a shorthand`,
		"paces:\n  Fast: { judge: off }\n":           "not a lowercase name",
	} {
		bad := strings.Join(decodePaces(t, yml).check(), "\n")
		if !strings.Contains(bad, want) {
			t.Errorf("%q: check = %q, want %q", yml, bad, want)
		}
	}

	if _, err := decodeDeclared([]byte("paces:\n  x: { review: { speed: 2 } }\n")); err == nil ||
		!strings.Contains(err.Error(), "is not a dial") {
		t.Errorf("an unknown dial = %v", err)
	}
}

func TestBuiltinPacesPass(t *testing.T) {
	for name, p := range BuiltinPaces() {
		if bad := CheckPace(name, p); len(bad) > 0 {
			t.Errorf("%s: %v", name, bad)
		}
	}
}

// A worktree tuning one key of a pace keeps main's other keys, and each
// key remembers its layer; the project's keys lie over the built-ins'.
func TestPacesMergeKeyByKey(t *testing.T) {
	var fm layered

	fm.merge(decodePaces(t, "paces:\n  fast: { review: { rounds: 2 }, judge: off }\n"), "main")
	fm.merge(decodePaces(t, "paces:\n  fast: { review: { rounds: 1, depth: light } }\n"), "worktree")

	e := &Effective{Flows: map[string][]string{"default": strings.Fields(DefaultFlow)}}
	e.resolvePaces(&fm, section{})

	if got := e.Paces["fast"]; got["review"].Depth != "light" || !got["judge"].Off {
		t.Errorf("fast = %+v", got)
	}

	if e.PacesOrigin["fast.review"] != "declared (worktree, frontmatter)" ||
		e.PacesOrigin["fast.judge"] != "declared (main, frontmatter)" {
		t.Errorf("origins = %v", e.PacesOrigin)
	}

	s := NewPaceSet()
	s.Over(e.Paces, func(name, key string) string { return e.PacesOrigin[name+"."+key] })

	if s.Paces["fast"]["finish"].Finish != "on" || s.Origins["fast.finish"] != PaceOriginBuiltin {
		t.Errorf("the built-in finish under the project's fast = %+v, %q", s.Paces["fast"], s.Origins["fast.finish"])
	}
}

func TestResolveFlow(t *testing.T) {
	flow := strings.Fields("plan judge implement cleanup pr review:local judge pr")
	s := NewPaceSet()

	for _, tc := range []struct {
		pace, run, finish string
	}{
		{"", "plan judge implement cleanup pr review:local judge pr", ""},
		{"medium", "plan judge implement cleanup pr review:local judge pr", ""},
		{"slow", "plan judge implement cleanup pr review:local judge pr", "off"}, // the argument keeps local
		{"fast", "plan judge implement cleanup pr review:local judge pr", "on"},
		{"ultra", "implement pr review:local pr", "on"},
	} {
		r, err := s.ResolveFlow("default", flow, tc.pace)
		if err != nil {
			t.Fatalf("%s: %v", tc.pace, err)
		}

		finish := ""
		if r.Finish != nil {
			finish = r.Finish.Value
		}

		if got := strings.Join(r.Run(), " "); got != tc.run || finish != tc.finish {
			t.Errorf("%s: run %q finish %q, want %q %q", tc.pace, got, finish, tc.run, tc.finish)
		}
	}

	// the entry's argument fixes the half; the pace's other dials still apply
	r, _ := s.ResolveFlow("default", flow, "slow")
	if e := r.Entries[5]; e.Entry != "review:local" || len(e.Dials) != 2 || e.Dials[0].Origin != "entry argument" ||
		e.Dials[1].Name != "rounds" || e.Dials[1].Value != "2" {
		t.Errorf("review under slow = %+v", e)
	}

	// a bare review takes the pace's half
	s.Over(map[string]Pace{"remote": {"review": {By: "ci"}}}, func(string, string) string { return "test" })

	if r, _ := s.ResolveFlow("x", []string{"implement", "pr", "review"}, "remote"); r.Run()[2] != "review:ci" {
		t.Errorf("bare review under remote = %v", r.Run())
	}
}

// finish applies only where the run would stop at an open PR.
func TestPaceFinishOnlyAtAPR(t *testing.T) {
	s := NewPaceSet()

	for flow, want := range map[string]bool{
		"implement pr":            true,
		"implement pr review:ci":  true,
		"implement":               false,
		"implement commit":        false,
		"implement pr deploy:stg": false,
	} {
		r, err := s.ResolveFlow("x", strings.Fields(flow), "fast")
		if err != nil {
			t.Fatal(err)
		}

		if (r.Finish != nil) != want {
			t.Errorf("%q: finish %+v, want %v", flow, r.Finish, want)
		}
	}
}

// A pace that leaves a flow invalid fails, naming the pace and the flow,
// and shows as a problem at load.
func TestPaceThatBreaksAFlow(t *testing.T) {
	s := NewPaceSet()
	s.Over(map[string]Pace{"noplan": {"plan": {Off: true}}}, func(string, string) string { return "test" })

	flows := map[string][]string{"default": strings.Fields("plan judge implement pr")}

	if _, err := s.ResolveFlow("default", flows["default"], "noplan"); err == nil ||
		!strings.Contains(err.Error(), "pace noplan on the default flow") {
		t.Errorf("err = %v", err)
	}

	if bad := s.PaceProblems(flows); len(bad) != 1 {
		t.Errorf("problems = %v", bad)
	}

	if _, err := s.ResolveFlow("default", flows["default"], "nope"); err == nil ||
		!strings.Contains(err.Error(), "fast, medium, noplan, slow, ultra") {
		t.Errorf("unknown pace = %v", err)
	}
}

// A ceiling reads every pace shorthand plus on, with except beside it.
func TestCeilingDecode(t *testing.T) {
	d := decodePaces(t, "ceiling: { finish: off, judge: on, review: { rounds: 2 }, except: [ultra] }\n")

	c := d.Ceiling
	if c == nil || c.Pace["finish"].Finish != "off" || !c.Pace["judge"].On || c.Pace["review"].Rounds != 2 ||
		strings.Join(c.Except, ",") != "ultra" {
		t.Fatalf("ceiling = %+v", c)
	}

	if bad := d.check(); len(bad) > 0 {
		t.Errorf("check = %v", bad)
	}

	if single := decodePaces(
		t,
		"ceiling: { finish: off, except: ultra }\n",
	); strings.Join(
		single.Ceiling.Except,
		",",
	) != "ultra" {
		t.Errorf("a single except = %+v", single.Ceiling)
	}
}

func TestCeilingRefusals(t *testing.T) {
	for yml, want := range map[string]string{
		"ceiling: { judge: off }\n":                  "a ceiling never skips a step",
		"ceiling: { review: { by: local } }\n":       "by has no order",
		"ceiling: { pr: { marker: fast } }\n":        "marker has no order",
		"ceiling: { judge: { rounds: 2 } }\n":        "rounds is a review dial",
		"ceiling: { finish: maybe }\n":               "finish is on or off",
		"ceiling: { catchup: on }\n":                 "catchup is never a flow entry",
		"ceiling: { finish: off, except: [Fast] }\n": "not a lowercase name",
		"paces:\n  x: { judge: on }\n":               "on is a ceiling's word",
	} {
		bad := strings.Join(decodePaces(t, yml).check(), "\n")
		if !strings.Contains(bad, want) {
			t.Errorf("%q: check = %q, want %q", yml, bad, want)
		}
	}
}

// The worktree's ceiling key replaces main's and keeps main's others;
// except is one list, replaced whole.
func TestCeilingMergeKeyByKey(t *testing.T) {
	var fm layered

	fm.merge(decodePaces(t, "ceiling: { finish: off, judge: on, except: [ultra] }\n"), "main")
	fm.merge(decodePaces(t, "ceiling: { judge: { effort: high }, except: [fast] }\n"), "worktree")

	e := &Effective{Flows: map[string][]string{"default": strings.Fields(DefaultFlow)}}
	e.resolvePaces(&fm, section{})

	if e.Ceiling["finish"].Finish != "off" || e.Ceiling["judge"].Effort != "high" ||
		strings.Join(e.CeilingExcept, ",") != "fast" {
		t.Errorf("ceiling = %+v, except %v", e.Ceiling, e.CeilingExcept)
	}

	if e.CeilingOrigin["finish"] != "declared (main, frontmatter)" ||
		e.CeilingOrigin["judge"] != "declared (worktree, frontmatter)" ||
		e.CeilingExceptOrigin != "declared (worktree, frontmatter)" {
		t.Errorf("origins = %v, except %q", e.CeilingOrigin, e.CeilingExceptOrigin)
	}
}

// A ceiling slows every pace but the excepted one, a silent pace included,
// and each capped dial says what the pace had.
func TestCeilingCaps(t *testing.T) {
	flow := strings.Fields("plan judge implement cleanup pr review judge pr")
	set := NewPaceSet()
	set.Ceiling = Pace{
		PaceFinishKey: {Finish: "off"},
		"cleanup":     {On: true},
		"review":      {Rounds: 2, Depth: "full"},
	}
	set.CeilingOrigins = map[string]string{PaceFinishKey: "main", "cleanup": "main", "review": "main"}

	for pace, want := range map[string]string{
		"medium": "plan judge implement cleanup pr review judge pr",
		"fast":   "plan judge implement cleanup pr review:local judge pr",
		"ultra":  "implement cleanup pr review:local pr",
	} {
		r, err := set.ResolveFlow("default", flow, pace)
		if err != nil {
			t.Fatalf("%s: %v", pace, err)
		}

		if got := strings.Join(r.Run(), " "); got != want {
			t.Errorf("%s: run %q, want %q", pace, got, want)
		}

		if r.Finish == nil || r.Finish.Value != "off" || r.Finish.Origin != "ceiling (main)" || r.Finish.Capped == "" {
			t.Errorf("%s: finish %+v", pace, r.Finish)
		}

		for _, e := range r.Entries {
			if e.Step != "review" {
				continue
			}

			if got := dialValue(e.Dials, "rounds"); got != "2" {
				t.Errorf("%s: review rounds %q", pace, got)
			}

			if pace == "ultra" && dialValue(e.Dials, "depth") != "full" {
				t.Errorf("ultra: depth %+v", e.Dials)
			}
		}
	}

	set.Except = []string{"ultra"}

	r, err := set.ResolveFlow("default", flow, "ultra")
	if err != nil || r.Finish == nil || r.Finish.Value != "on" || strings.Contains(strings.Join(r.Run(), " "), "plan") {
		t.Errorf("an excepted pace = %+v, %v", r, err)
	}

	// a flow ending at implement takes no finish, capped or not
	local, err := set.ResolveFlow("local", strings.Fields("implement commit"), "fast")
	if err != nil || local.Finish != nil {
		t.Errorf("local = %+v, %v", local.Finish, err)
	}
}

// A ceiling's effort raises a pace's lower effort and leaves a silent one.
func TestCeilingEffort(t *testing.T) {
	set := NewPaceSet()
	set.Ceiling = Pace{"judge": {Effort: "high"}}

	if p, caps := set.Capped(
		"fast",
	); p["judge"].Effort != "high" ||
		caps["judge.effort"].Capped != "pace fast said low" {
		t.Errorf("fast = %+v, %+v", p["judge"], caps)
	}

	if p, _ := set.Capped("medium"); p["judge"].Effort != "" {
		t.Errorf("medium = %+v", p["judge"])
	}
}

// A ceiling judge: on under a pace that drops plan keeps a plan judge with
// no plan, which the flow check refuses.
func TestCeilingThatBreaksAFlow(t *testing.T) {
	set := NewPaceSet()
	set.Ceiling = Pace{"judge": {On: true}}

	_, err := set.ResolveFlow("default", strings.Fields("plan judge implement pr"), "ultra")
	if err == nil || !strings.Contains(err.Error(), "pace ultra") {
		t.Errorf("err = %v", err)
	}
}

func dialValue(dials []Dial, name string) string {
	for _, d := range dials {
		if d.Name == name {
			return d.Value
		}
	}

	return ""
}

// drive_finish and a Rounds line read as the default pace's dials, with a
// warning; a dial the paces declare wins.
func TestOldPaceKeysConvert(t *testing.T) {
	eff := buildWith(t, "---\ndrive_finish: off\n---\n# demo\n\n## Review\n\nRounds: 2\n", "")

	if eff.Paces["medium"]["finish"].Finish != "off" || eff.Paces["medium"]["review"].Rounds != 2 {
		t.Errorf("medium = %+v", eff.Paces["medium"])
	}

	if !strings.HasPrefix(eff.PacesOrigin["medium.finish"], "converted from drive_finish") ||
		!strings.HasPrefix(eff.PacesOrigin["medium.review"], "converted from Rounds: 2") {
		t.Errorf("origins = %v", eff.PacesOrigin)
	}

	if len(eff.Problems) != 2 ||
		!strings.Contains(eff.Problems[0].Message+eff.Problems[1].Message, "f10 init --migrate") {
		t.Errorf("problems = %+v", eff.Problems)
	}

	slow := buildWith(
		t,
		"---\npace: slow\npaces:\n  slow: { review: { by: ci } }\n---\n# demo\n\n## Review\n\n- **Rounds**: 2\n",
		"",
	)
	if r := slow.Paces["slow"]["review"]; r.By != "ci" || r.Rounds != 2 {
		t.Errorf("slow review = %+v", r)
	}

	both := buildWith(t, "---\ndrive_finish: off\npaces:\n  medium: { finish: on }\n---\n# demo\n", "")
	if both.Paces["medium"]["finish"].Finish != "on" || len(both.Problems) != 1 ||
		!strings.Contains(both.Problems[0].Message, "drive_finish ignored") {
		t.Errorf("finish %+v, problems %+v", both.Paces["medium"], both.Problems)
	}
}

// --migrate moves drive_finish and a Rounds line into the default pace,
// keeping comments, and a second run has nothing to do.
func TestMigratePaces(t *testing.T) {
	file := "---\n# merges\ndrive_finish: off\nmerge_method: squash\n---\n# demo\n\n## Review\n\nRounds: 2\n"

	m, err := Migrate([]byte(file), "project.md")
	if err != nil {
		t.Fatal(err)
	}

	want := "---\n# merges\npaces:\n  medium:\n    finish: off\n    review:\n      rounds: 2\nmerge_method: squash\n---\n"
	if !strings.HasPrefix(m.Content, want) {
		t.Errorf("content =\n%s\nwant prefix\n%s", m.Content, want)
	}

	if strings.Join(m.Moved, ",") != "paces" || len(m.Superseded) != 1 || len(m.Notes) != 1 ||
		!strings.Contains(m.Notes[0], "ceiling") {
		t.Errorf("moved %v, superseded %v, notes %v", m.Moved, m.Superseded, m.Notes)
	}

	if _, err := Migrate([]byte(m.Content), "project.md"); !errors.Is(err, ErrHasFrontmatter) {
		t.Errorf("a second migration = %v", err)
	}

	// the half shorthand becomes a map beside rounds, on the declared pace
	fast, err := Migrate(
		[]byte("---\npace: fast\npaces:\n  fast: { review: local }\n---\n# demo\n\n## Review\n\nRounds: 2\n"),
		"project.md",
	)
	if err != nil {
		t.Fatal(err)
	}

	d, err := decodeDeclared([]byte(strings.Split(fast.Content, "---\n")[1]))
	if err != nil || d.Paces["fast"]["review"].By != "local" || d.Paces["fast"]["review"].Rounds != 2 {
		t.Errorf("fast = %+v, %v\n%s", d.Paces["fast"], err, fast.Content)
	}

	// drive_finish on is what medium already does: the key goes, no dial
	on, err := Migrate([]byte("---\ndrive_finish: on\nmerge_method: squash\n---\n"), "project.md")
	if err != nil || on.Content != "---\nmerge_method: squash\n---\n" {
		t.Errorf("on = %+v, %v", on, err)
	}
}

// An optional CI review settles where the flow resolves: kept with a remote
// reviewer and a PR before it, dropped otherwise, with the reason.
func TestResolveOptionalCI(t *testing.T) {
	for name, tc := range map[string]struct {
		flow, pace string
		remote     bool
		run, why   string
	}{
		"slow, reviewer":       {flow: "implement pr review", pace: "slow", remote: true, run: "implement pr review:local+ci"},
		"slow, no reviewer":    {flow: "implement pr review", pace: "slow", run: "implement pr review:local", why: "no remote reviewer declared"},
		"argument, no pace":    {flow: "implement pr review:local+ci?", run: "implement pr review:local", why: "no remote reviewer declared"},
		"ci alone, before pr":  {flow: "implement review:ci? pr", remote: true, run: "implement pr", why: "no pr or push before it"},
		"ci alone, after push": {flow: "implement push review:ci?", remote: true, run: "implement push review:ci"},
	} {
		s := NewPaceSet()
		s.Remote = tc.remote

		r, err := s.ResolveFlow("x", strings.Fields(tc.flow), tc.pace)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if got := strings.Join(r.Run(), " "); got != tc.run {
			t.Errorf("%s: run %q, want %q", name, got, tc.run)
		}

		var shown []string
		for _, e := range r.Entries {
			shown = append(shown, e.Skipped)
			for _, d := range e.Dials {
				shown = append(shown, d.Origin)
			}
		}

		if joined := strings.Join(shown, "; "); tc.why != "" && !strings.Contains(joined, tc.why) {
			t.Errorf("%s: origins %q, want %q", name, joined, tc.why)
		}

		if tc.why == "" && tc.pace == "" && r.Changed() != (tc.run != tc.flow) {
			t.Errorf("%s: changed = %v", name, r.Changed())
		}
	}
}

// YAML refuses a plain ci? inside { }; the error says to quote it, and the
// quoted form reads.
func TestOptionalCIInFlowStyle(t *testing.T) {
	if _, err := decodeDeclared([]byte("paces:\n  x: { review: local+ci? }\n")); err == nil ||
		!strings.Contains(err.Error(), `quote an optional CI review`) {
		t.Errorf("unquoted = %v", err)
	}

	if d := decodePaces(t, "paces:\n  x: { review: \"local+ci?\" }\n"); d.Paces["x"]["review"].By != "local+ci?" ||
		len(d.check()) > 0 {
		t.Errorf("quoted = %+v, %v", d.Paces["x"], d.check())
	}
	// an unknown key beside a block-style ci? is not a quoting problem
	if _, err := decodeDeclared([]byte("flows:\n  default: implement pr review:ci?\ntrakcer: x\n")); err == nil ||
		strings.Contains(err.Error(), "quote") {
		t.Errorf("unknown key = %v", err)
	}
}
