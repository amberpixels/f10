package facts

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestDecodeFlows(t *testing.T) {
	d, err := decodeDeclared([]byte("flows:\n" +
		"  default: plan judge implement cleanup:comments,prose pr → review (CI) pr\n" +
		"  local: [plan, implement, 'review (local)', commit]\n" +
		"drive_finish: off\n"))
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(
		d.Flows["default"],
		"|",
	); got != "plan|judge|implement|cleanup:comments,prose|pr|review (CI)|pr" {
		t.Errorf("string form = %q", got)
	}

	if got := strings.Join(d.Flows["local"], "|"); got != "plan|implement|review (local)|commit" {
		t.Errorf("list form = %q", got)
	}

	if bad := d.check(); len(bad) > 0 {
		t.Errorf("a valid block fails its check: %v", bad)
	}

	if got, _ := normalizeFlow(d.Flows["default"]); got[5] != "review:ci" {
		t.Errorf("paren form normalised to %q", got[5])
	}

	if got := splitFlow("implement deploy (staging eu)"); strings.Join(got, "|") != "implement|deploy (staging eu)" {
		t.Errorf("a parenthesis spanning words split: %q", got)
	}
}

func TestCheckFlow(t *testing.T) {
	for name, tc := range map[string]struct {
		flow Flow
		want string
	}{
		"empty":             {flow: Flow{}, want: "no entries"},
		"plan not first":    {flow: Flow{"implement", "plan"}, want: "plan comes first"},
		"colon without arg": {flow: Flow{"plan", "review:"}, want: "no argument"},
		"bad step":          {flow: Flow{"plan", "Implement"}, want: "not a step name"},
		"planless judge":    {flow: Flow{"judge", "implement", "pr"}, want: "lead with plan"},
	} {
		bad := Declared{Flows: map[string]Flow{"default": tc.flow}}.check()
		if !slices.ContainsFunc(bad, func(b string) bool { return strings.Contains(b, tc.want) }) {
			t.Errorf("%s: check = %v, want %q", name, bad, tc.want)
		}
	}

	ok := Declared{Flows: map[string]Flow{
		"default": {"plan", "judge", "implement", "judge", "pr"},
		"direct":  {"implement", "judge", "push"},
	}, DriveFinish: "on"}.check()
	if len(ok) > 0 {
		t.Errorf("valid flows fail: %v", ok)
	}

	if bad := (Declared{Flows: map[string]Flow{"Fast": {"implement"}}}).check(); len(bad) != 1 {
		t.Errorf("an uppercase flow name passed: %v", bad)
	}
}

func TestFlowHelpers(t *testing.T) {
	flow := []string{"plan", "judge", "implement", "review (local)", "judge", "pr"}

	if got := ShipEntries(flow); got[0] != "judge" || len(got) != 5 {
		t.Errorf("ship entries = %v", got)
	}

	if JudgeKind(flow, 1) != "plan" || JudgeKind(flow, 4) != "change" {
		t.Error("judge kind is read by position against implement")
	}

	if Planless(flow) || !Planless([]string{"implement", "pr"}) {
		t.Error("planless is a flow not led by plan")
	}

	if step, arg := SplitEntry("review (local)"); step != "review" || arg != "local" {
		t.Errorf("paren entry = %s, %s", step, arg)
	}

	if step, arg := SplitEntry("cleanup:comments,prose"); step != "cleanup" || arg != "comments,prose" {
		t.Errorf("colon entry = %s, %s", step, arg)
	}
}

func TestLegacyFlows(t *testing.T) {
	pipes := map[string][]string{
		"default": {"implement", "pr"},
		"local":   {"implement", "review (local)", "commit"},
		"direct":  {"implement", "push"},
	}
	chain := strings.Fields(legacyDriveChain)

	flows, finish := legacyFlows(pipes, []string{"direct"}, chain, true)
	if !finish {
		t.Error("a chain ending in finish converts to drive_finish on")
	}

	for name, want := range map[string]string{
		"default": "plan judge implement pr review:ci", // the chain's judge and review fold into default
		"local":   "plan implement review:local commit",
		"direct":  "implement push",
	} {
		if got := strings.Join(flows[name], " "); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// a pipeline that already reviews takes no second review from the chain
	reviews, _ := legacyFlows(map[string][]string{"default": {"implement", "pr", "review (local)", "judge", "pr"}},
		nil, chain, false)
	if got := strings.Join(reviews["default"], " "); got != "plan judge implement pr review:local judge pr" {
		t.Errorf("reviewing default = %q", got)
	}

	// a planless default takes no plan judge, which would have nothing to judge
	planless, finish := legacyFlows(map[string][]string{"default": {"implement", "pr"}}, []string{"default"},
		[]string{"judge", "ship"}, false)
	if got := strings.Join(planless["default"], " "); got != "implement pr" || finish {
		t.Errorf("planless default = %q, finish %v", got, finish)
	}
}

// Old keys convert with a warning; flows declared beside them wins and the
// old keys are named as ignored.
func TestBuildFlowsFromLegacyKeys(t *testing.T) {
	eff := buildWith(t, "---\npipelines: {default: implement → review (local) → pr}\n---\n", "")

	if got := strings.Join(eff.Flows["default"], " "); got != "plan judge implement review:local pr" {
		t.Errorf("converted default = %q", got)
	}

	if !eff.DriveFinish || eff.Errors() != "" || len(eff.Problems) != 1 ||
		!strings.Contains(eff.Problems[0].Message, "pipelines converted to flows") {
		t.Errorf("finish %v, problems %+v", eff.DriveFinish, eff.Problems)
	}

	both := buildWith(t, "---\nflows: {default: plan implement pr}\ndrive_chain: [plan, ship]\n---\n", "")
	if got := strings.Join(both.Flows["default"], " "); got != "plan implement pr" || both.DriveFinish {
		t.Errorf("flows beside drive_chain = %q, finish %v", got, both.DriveFinish)
	}

	if len(both.Problems) != 1 || !strings.Contains(both.Problems[0].Message, "drive_chain ignored") {
		t.Errorf("problems = %+v", both.Problems)
	}

	paren := buildWith(t, "---\nflows: {default: plan implement review (local) pr}\n---\n", "")
	if got := strings.Join(paren.Flows["default"], " "); got != "plan implement review:local pr" ||
		len(paren.Problems) != 1 || !strings.Contains(paren.Problems[0].Message, "old `step (arg)` entry form") {
		t.Errorf("paren flow = %q, problems %+v", got, paren.Problems)
	}

	none := buildWith(t, "---\nvisibility: stealth\n---\n", "")
	if got := strings.Join(none.Flows["default"], " "); got != DefaultFlow || none.DriveFinish {
		t.Errorf("default flow = %q, finish %v", got, none.DriveFinish)
	}
}

func TestFlowNames(t *testing.T) {
	e := &Effective{Flows: map[string][]string{"local": nil, "default": nil, "direct": nil}}
	if got := strings.Join(e.FlowNames(), " "); got != "default direct local" {
		t.Errorf("names = %q", got)
	}

	if _, ok := e.Flow("LOCAL"); !ok {
		t.Error("a flow is found in any case")
	}
}

// A block with the old keys is rewritten in place: flows where pipelines
// stood, its comment kept, the other old keys gone, review_traces renamed.
func TestMigrateLegacyBlock(t *testing.T) {
	file := "---\nmerge_method: squash\n# how tasks ship\npipelines:\n  default: implement → review (local) → pr\n" +
		"  local: [implement, commit]\nplanless: [local]\npipeline_by_area: {ui: local}\n" +
		"drive_chain: plan → judge → ship → finish\nreview_traces: on\n---\n# demo\n"

	m, err := Migrate([]byte(file), "project.md")
	if err != nil {
		t.Fatal(err)
	}

	want := "---\nmerge_method: squash\n# how tasks ship\nflows:\n" +
		"  default: plan judge implement review:local pr\n  local: implement commit\n" +
		"drive_finish: on\nflow_by_area: {ui: local}\ntraces: on\n---\n# demo\n"
	if m.Content != want {
		t.Errorf("content =\n%s\nwant\n%s", m.Content, want)
	}

	if got := strings.Join(m.Moved, ","); got != "flows,drive_finish,flow_by_area,traces" {
		t.Errorf("moved = %s", got)
	}

	if _, err := Migrate([]byte(m.Content), "project.md"); !errors.Is(err, ErrHasFrontmatter) {
		t.Errorf("a second migration = %v", err)
	}

	// old-form entries inside flows are rewritten too
	paren, err := Migrate([]byte("---\nflows:\n  default: plan implement review (CI) pr\n---\n"), "project.md")
	if err != nil || paren.Content != "---\nflows:\n  default: plan implement review:ci pr\n---\n" {
		t.Errorf("paren = %+v, %v", paren, err)
	}
}

// A block with no old keys but a prose Ship pipeline(s) section, which
// config warns about, migrates: the block gains flows and the section is
// named for deletion.
func TestMigrateProseUnderABlock(t *testing.T) {
	file := "---\nmerge_method: squash\n---\n# demo\n\n## Ship pipelines\n\n- **default**: `implement → pr`\n"

	m, err := Migrate([]byte(file), "project.md")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(m.Content, "flows:\n  default: plan judge implement pr review:local\ndrive_finish: on\n") ||
		strings.Join(m.Superseded, ",") != "flows: the Ship pipeline(s) section" {
		t.Errorf("migration = %+v", m)
	}
}
