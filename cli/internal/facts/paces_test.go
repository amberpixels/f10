package facts

import (
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/flow"
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

	if !p.Steps["judge"].Off || p.Steps["review"].By != "ci" || p.Finish != "on" || p.Steps["pr"].Marker != "fast" {
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

	e := &Effective{Flows: map[string]flow.Flow{"default": flowOf(strings.Fields(DefaultFlow))}}
	e.resolvePaces(&fm)

	if got := e.Paces["fast"]; got.Steps["review"].Depth != "light" || !got.Steps["judge"].Off {
		t.Errorf("fast = %+v", got)
	}

	if e.PacesOrigin["fast.review"].String() != "declared (worktree, frontmatter)" ||
		e.PacesOrigin["fast.judge"].String() != "declared (main, frontmatter)" {
		t.Errorf("origins = %v", e.PacesOrigin)
	}

	s := NewPaceSet()
	s.Over(e.Paces, func(name, key string) Origin { return e.PacesOrigin[name+"."+key] })

	if s.Paces["fast"].Finish != "on" || s.Origins["fast.finish"] != PaceOriginBuiltin {
		t.Errorf("the built-in finish under the project's fast = %+v, %q", s.Paces["fast"], s.Origins["fast.finish"])
	}
}

func TestResolveFlow(t *testing.T) {
	written := strings.Fields("plan judge implement cleanup pr review:local judge pr")
	s := NewPaceSet()

	for _, tc := range []struct {
		pace, run, finish string
	}{
		{"", "plan judge implement cleanup pr review:local judge pr", ""},
		{"medium", "plan judge implement cleanup pr review:local judge pr", "on"},
		{"slow", "plan judge implement cleanup pr review:local judge pr", "off"}, // the argument keeps local
		{"fast", "plan judge implement cleanup pr review:local judge pr", "on"},
		{"ultra", "implement pr review:local pr", "on"},
	} {
		r, err := s.ResolveFlow("default", flowOf(written), tc.pace)
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
	r, _ := s.ResolveFlow("default", flowOf(written), "slow")
	if e := r.Entries[5]; e.Entry != "review:local" || len(e.Dials) != 2 || e.Dials[0].Origin != "entry argument" ||
		e.Dials[1].Name != "rounds" || e.Dials[1].Value != "2" {
		t.Errorf("review under slow = %+v", e)
	}

	// a bare review takes the pace's half
	s.Over(
		map[string]Pace{"remote": {Steps: map[string]Dials{"review": {By: "ci"}}}},
		func(string, string) Origin { return DeclaredIn("test") },
	)

	if r, _ := s.ResolveFlow("x", flowOf([]string{"implement", "pr", "review"}), "remote"); r.Run()[2] != "review:ci" {
		t.Errorf("bare review under remote = %v", r.Run())
	}
}

// Every built-in pace keeps handoff, ultra included: an auto-merged run is
// where the list of what to do after merge matters most.
func TestBuiltinPacesKeepHandoff(t *testing.T) {
	written := strings.Fields("plan implement handoff pr")
	s := NewPaceSet()

	for _, pace := range s.Names() {
		r, err := s.ResolveFlow("default", flowOf(written), pace)
		if err != nil {
			t.Fatalf("%s: %v", pace, err)
		}

		if !slices.Contains(r.Run(), "handoff") {
			t.Errorf("%s: run %v drops handoff", pace, r.Run())
		}

		if got := dialValue(r.Entries[3].Dials, "handoff"); got != "" {
			t.Errorf("%s: pr carries handoff %q", pace, got)
		}
	}
}

// handoff: off reaches every pr entry as a dial, because the pr step runs the
// handoff; a ceiling's handoff: on keeps the dial off them.
func TestPaceHandoffOffReachesPR(t *testing.T) {
	d := decodePaces(t, "paces:\n  quiet:\n    handoff: off\n")

	s := NewPaceSet()
	s.Over(d.Paces, func(string, string) Origin { return DeclaredIn("test") })

	r, err := s.ResolveFlow("default", flowOf(strings.Fields("implement pr review:local pr")), "quiet")
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range r.Entries {
		got := dialValue(e.Dials, "handoff")
		if want := map[bool]string{true: "off"}[e.Step == "pr"]; got != want {
			t.Errorf("%s: handoff dial %q, want %q", e.Entry, got, want)
		}

		if e.Step == "pr" && !strings.Contains(e.Dials[0].Origin, "pace quiet") {
			t.Errorf("%s: origin %q", e.Entry, e.Dials[0].Origin)
		}
	}

	s.Ceiling = Pace{Steps: map[string]Dials{"handoff": {On: true}}}

	r, err = s.ResolveFlow("default", flowOf(strings.Fields("implement pr")), "quiet")
	if err != nil {
		t.Fatal(err)
	}

	if got := dialValue(r.Entries[1].Dials, "handoff"); got != "" {
		t.Errorf("under a handoff: on ceiling, pr carries handoff %q", got)
	}
}

// handoff: off skips the entry like any step key, naming the pace.
func TestPaceHandoffOff(t *testing.T) {
	d := decodePaces(t, "paces:\n  quiet:\n    handoff: off\n")

	s := NewPaceSet()
	s.Over(d.Paces, func(string, string) Origin { return DeclaredIn("test") })

	r, err := s.ResolveFlow("default", flowOf(strings.Fields("implement handoff pr")), "quiet")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(r.Run(), " "); got != "implement pr" {
		t.Errorf("run = %q, want implement pr", got)
	}

	if e := r.Entries[1]; e.Step != "handoff" || !strings.Contains(e.Skipped, "pace quiet") {
		t.Errorf("handoff entry = %+v", e)
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
		r, err := s.ResolveFlow("x", flowOf(strings.Fields(flow)), "fast")
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
	s.Over(
		map[string]Pace{"noplan": {Steps: map[string]Dials{"plan": {Off: true}}}},
		func(string, string) Origin { return DeclaredIn("test") },
	)

	flows := map[string][]string{"default": strings.Fields("plan judge implement pr")}

	if _, err := s.ResolveFlow("default", flowOf(flows["default"]), "noplan"); err == nil ||
		!strings.Contains(err.Error(), "pace noplan on the default flow") {
		t.Errorf("err = %v", err)
	}

	if bad := s.PaceProblems(map[string]flow.Flow{"default": flow.Of(flows["default"])}); len(bad) != 1 {
		t.Errorf("problems = %v", bad)
	}

	if _, err := s.ResolveFlow("default", flowOf(flows["default"]), "nope"); err == nil ||
		!strings.Contains(err.Error(), "fast, medium, noplan, slow, ultra") {
		t.Errorf("unknown pace = %v", err)
	}
}

// A ceiling reads every pace shorthand plus on, with except beside it.
func TestCeilingDecode(t *testing.T) {
	d := decodePaces(t, "ceiling: { finish: off, judge: on, review: { rounds: 2 }, except: [ultra] }\n")

	c := d.Ceiling
	if c == nil || c.Pace.Finish != "off" || !c.Pace.Steps["judge"].On || c.Pace.Steps["review"].Rounds != 2 ||
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

	e := &Effective{Flows: map[string]flow.Flow{"default": flowOf(strings.Fields(DefaultFlow))}}
	e.resolvePaces(&fm)

	if e.Ceiling.Finish != "off" || e.Ceiling.Steps["judge"].Effort != "high" ||
		strings.Join(e.CeilingExcept, ",") != "fast" {
		t.Errorf("ceiling = %+v, except %v", e.Ceiling, e.CeilingExcept)
	}

	if e.CeilingOrigin["finish"].String() != "declared (main, frontmatter)" ||
		e.CeilingOrigin["judge"].String() != "declared (worktree, frontmatter)" ||
		e.CeilingExceptOrigin.String() != "declared (worktree, frontmatter)" {
		t.Errorf("origins = %v, except %q", e.CeilingOrigin, e.CeilingExceptOrigin)
	}
}

// A ceiling slows every pace but the excepted one, a silent pace included,
// and each capped dial says what the pace had.
func TestCeilingCaps(t *testing.T) {
	written := strings.Fields("plan judge implement cleanup pr review judge pr")
	set := NewPaceSet()
	set.Ceiling = Pace{
		Steps: map[string]Dials{
			"cleanup": {On: true},
			"review":  {Rounds: 2, Depth: "full"},
		},
		Finish: "off",
	}
	main := DeclaredFrontmatter("main")
	set.CeilingOrigins = map[string]Origin{PaceFinishKey: main, "cleanup": main, "review": main}

	for pace, want := range map[string]string{
		"medium": "plan judge implement cleanup pr review judge pr",
		"fast":   "plan judge implement cleanup pr review:local judge pr",
		"ultra":  "implement cleanup pr review:local pr",
	} {
		r, err := set.ResolveFlow("default", flowOf(written), pace)
		if err != nil {
			t.Fatalf("%s: %v", pace, err)
		}

		if got := strings.Join(r.Run(), " "); got != want {
			t.Errorf("%s: run %q, want %q", pace, got, want)
		}

		if r.Finish == nil || r.Finish.Value != "off" || r.Finish.Origin != "ceiling (declared (main, frontmatter))" ||
			r.Finish.Capped == "" {
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

	r, err := set.ResolveFlow("default", flowOf(written), "ultra")
	if err != nil || r.Finish == nil || r.Finish.Value != "on" || strings.Contains(strings.Join(r.Run(), " "), "plan") {
		t.Errorf("an excepted pace = %+v, %v", r, err)
	}

	// a flow ending at implement takes no finish, capped or not
	local, err := set.ResolveFlow("local", flowOf(strings.Fields("implement commit")), "fast")
	if err != nil || local.Finish != nil {
		t.Errorf("local = %+v, %v", local.Finish, err)
	}
}

// A ceiling's effort raises a pace's lower effort and leaves a silent one.
func TestCeilingEffort(t *testing.T) {
	set := NewPaceSet()
	set.Ceiling = Pace{Steps: map[string]Dials{"judge": {Effort: "high"}}}

	if p, caps := set.Capped(
		"fast",
	); p.Steps["judge"].Effort != "high" ||
		caps["judge.effort"].Capped != "pace fast said low" {
		t.Errorf("fast = %+v, %+v", p.Steps["judge"], caps)
	}

	if p, _ := set.Capped("medium"); p.Steps["judge"].Effort != "" {
		t.Errorf("medium = %+v", p.Steps["judge"])
	}
}

// A ceiling judge: on under a pace that drops plan keeps a plan judge with
// no plan, which the flow check refuses.
func TestCeilingThatBreaksAFlow(t *testing.T) {
	set := NewPaceSet()
	set.Ceiling = Pace{Steps: map[string]Dials{"judge": {On: true}}}

	_, err := set.ResolveFlow("default", flowOf(strings.Fields("plan judge implement pr")), "ultra")
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

		r, err := s.ResolveFlow("x", flowOf(strings.Fields(tc.flow)), tc.pace)
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

	if d := decodePaces(
		t,
		"paces:\n  x: { review: \"local+ci?\" }\n",
	); d.Paces["x"].Steps["review"].By != "local+ci?" ||
		len(d.check()) > 0 {
		t.Errorf("quoted = %+v, %v", d.Paces["x"], d.check())
	}
	// an unknown key beside a block-style ci? is not a quoting problem
	if _, err := decodeDeclared([]byte("flows:\n  default: implement pr review:ci?\ntrakcer: x\n")); err == nil ||
		strings.Contains(err.Error(), "quote") {
		t.Errorf("unknown key = %v", err)
	}
}

func flowOf(written []string) flow.Flow { return flow.Of(written) }
