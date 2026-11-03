package facts

import (
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
	e.resolvePaces(&fm)

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
