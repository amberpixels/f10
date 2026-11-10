package facts

import (
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/flow"
)

func TestDecodeFlows(t *testing.T) {
	t.Parallel()

	d, err := decodeDeclared([]byte("flows:\n" +
		"  default: plan judge implement cleanup:comments,prose pr → review:ci pr\n" +
		"  local: [plan, implement, 'review:local', commit]\n"))
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(
		d.Flows["default"],
		"|",
	); got != "plan|judge|implement|cleanup:comments,prose|pr|review:ci|pr" {
		t.Errorf("string form = %q", got)
	}

	if got := strings.Join(d.Flows["local"], "|"); got != "plan|implement|review:local|commit" {
		t.Errorf("list form = %q", got)
	}

	if bad := d.check(); len(bad) > 0 {
		t.Errorf("a valid block fails its check: %v", bad)
	}

	// `review (local)` splits into two entries, and `(local)` names no step
	old := Declared{Flows: map[string]Flow{"default": splitFlow("plan implement review (local)")}}
	if bad := old.check(); len(bad) == 0 {
		t.Error("the old review (local) form passed")
	}
}

func TestCheckFlow(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		flow Flow
		want string
	}{
		"empty":             {flow: Flow{}, want: "no entries"},
		"plan not first":    {flow: Flow{"implement", "plan"}, want: "plan comes first"},
		"colon without arg": {flow: Flow{"plan", "review:"}, want: "no argument"},
		"bad step":          {flow: Flow{"plan", "Implement"}, want: "not a step name"},
		"planless judge":    {flow: Flow{"judge", "implement", "pr"}, want: "lead with plan"},
		"review typo":       {flow: Flow{"plan", "implement", "pr", "review:loacl"}, want: "names no reviewer"},
		"catchup entry":     {flow: Flow{"plan", "implement", "catchup", "pr"}, want: "never an entry"},
	} {
		bad := Declared{Flows: map[string]Flow{"default": tc.flow}}.check()
		if !slices.ContainsFunc(bad, func(b string) bool { return strings.Contains(b, tc.want) }) {
			t.Errorf("%s: check = %v, want %q", name, bad, tc.want)
		}
	}

	ok := Declared{Flows: map[string]Flow{
		"default": {"plan", "judge", "implement", "judge", "pr"},
		"direct":  {"implement", "judge", "push"},
		"both":    {"plan", "implement", "pr", "review:local+ci", "review", "review:ci", "review:host"},
	}}.check()
	if len(ok) > 0 {
		t.Errorf("valid flows fail: %v", ok)
	}

	if bad := (Declared{Flows: map[string]Flow{"Fast": {"implement"}}}).check(); len(bad) != 1 {
		t.Errorf("an uppercase flow name passed: %v", bad)
	}
}

func TestFlowLabels(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		flow Flow
		want string
	}{
		"a step's name":    {flow: Flow{"plan", "implement", "pr", "pr[review]"}, want: "is a step's name"},
		"a flow's step":    {flow: Flow{"plan", "implement", "e2e", "pr[e2e]"}, want: "is a step's name"},
		"not kebab-case":   {flow: Flow{"plan", "implement", "pr", "pr[Polish]"}, want: "not lower kebab-case"},
		"repeated":         {flow: Flow{"plan", "implement[build]", "pr[build]"}, want: "names two entries"},
		"a trailing dash":  {flow: Flow{"plan", "implement", "pr[polish-]"}, want: "not lower kebab-case"},
		"an empty bracket": {flow: Flow{"plan", "implement", "pr[]"}, want: "a name in brackets"},
		"unclosed":         {flow: Flow{"plan", "implement", "pr[polish"}, want: "a name in brackets"},
	} {
		bad := Declared{Flows: map[string]Flow{"default": tc.flow}}.check()
		if !slices.ContainsFunc(bad, func(b string) bool { return strings.Contains(b, tc.want) }) {
			t.Errorf("%s: check = %v, want %q", name, bad, tc.want)
		}
	}

	eff := buildWith(t, "---\nflows:\n  default: plan implement pr review:local pr[pr-polish]\n"+
		"  direct: [implement, 'review:local[check]', push]\n---\n", "")
	for _, p := range eff.Problems {
		t.Errorf("a labelled flow has a problem: %s", p.Message)
	}

	if got := strings.Join(eff.Flows["default"].Strings(), " "); got != "plan implement pr review:local pr" {
		t.Errorf("entries = %q, want them bare", got)
	}

	if got := eff.Flows["default"].Labels(); len(got) != 5 || got[4] != "pr-polish" || got[2] != "" {
		t.Errorf("labels = %q", got)
	}

	if got := eff.Flows["direct"][1]; got.Label != "check" || got.String() != "review:local" {
		t.Errorf("direct entry = %+v", got)
	}

	if got := eff.Flows["default"].Written(); got[4] != "pr[pr-polish]" {
		t.Errorf("labelled entries = %q", got)
	}

	plain := buildWith(t, "---\nflows:\n  default: plan implement pr\n---\n", "")
	if got := plain.Flows["default"].Labels(); got != nil {
		t.Errorf("a flow with no label has labels: %v", got)
	}
}

func TestReviewBy(t *testing.T) {
	t.Parallel()

	for arg, want := range map[string]By{
		"":          {Local: true},
		"local":     {Local: true},
		"host":      {Local: true},
		"ci":        {CI: true},
		"CI":        {CI: true},
		"ci?":       {CI: true, CIOptional: true},
		"local+ci":  {Local: true, CI: true},
		"ci+local":  {Local: true, CI: true},
		"local+ci?": {Local: true, CI: true, CIOptional: true},
		"ci?+local": {Local: true, CI: true, CIOptional: true},
	} {
		if got, ok := ReviewBy(arg); !ok || got != want {
			t.Errorf("ReviewBy(%q) = %+v, %v, want %+v", arg, got, ok, want)
		}
	}

	for _, arg := range []string{"loacl", "local?", "local?+ci", "host+ci?", "ci??"} {
		if _, ok := ReviewBy(arg); ok {
			t.Errorf("ReviewBy(%q) accepted", arg)
		}
	}
}

// An optional CI review runs only with a remote reviewer and something
// published before it; a required one is never touched.
func TestBySettle(t *testing.T) {
	t.Parallel()

	opt := By{Local: true, CI: true, CIOptional: true}

	for name, tc := range map[string]struct {
		by                By
		remote, published bool
		arg, why          string
	}{
		"runs":           {by: opt, remote: true, published: true, arg: "local+ci"},
		"no reviewer":    {by: opt, published: true, arg: "local", why: "no remote reviewer declared"},
		"nothing pushed": {by: opt, remote: true, arg: "local", why: "no pr or push before it"},
		"ci alone":       {by: By{CI: true, CIOptional: true}, arg: "", why: "no remote reviewer declared"},
		"required":       {by: By{CI: true}, arg: "ci"},
	} {
		got, why := tc.by.Settle(tc.remote, tc.published)
		if got.Arg() != tc.arg || why != tc.why {
			t.Errorf("%s: Settle = %q, %q, want %q, %q", name, got.Arg(), why, tc.arg, tc.why)
		}
	}
}

// A required CI review that cannot run warns: no remote reviewer, or nothing
// published before it. Neither is an error. An optional one never warns.
func TestRemoteReviewWarnings(t *testing.T) {
	t.Parallel()

	const review = "review:\n  reviewer: bot `claude[bot]`\n  arrives: issue comment\n  trigger: automatic\n" +
		"  done: posted, reaction `eyes`\n"

	for name, tc := range map[string]struct {
		block, want string
	}{
		"no reviewer":           {block: "flows: {default: plan implement pr review:local+ci}\n", want: "no remote reviewer"},
		"before pr":             {block: "flows: {default: plan implement review:ci pr}\n" + review, want: "no pr or push"},
		"runs as given":         {block: "flows: {default: plan implement pr review:local+ci}\n" + review},
		"optional, no reviewer": {block: "flows:\n  default: plan implement pr review:local+ci?\n"},
		"optional, before pr":   {block: "flows:\n  default: plan implement review:ci? pr\n" + review},
	} {
		eff := buildWith(t, "---\n"+tc.block+"---\n", "")
		if got := strings.Join(eff.Flows["default"].Strings(), " "); !strings.Contains(got, "review:") {
			t.Errorf("%s: flow = %q", name, got)
		}

		var msgs []string

		for _, p := range eff.Problems {
			if p.Error {
				t.Errorf("%s: an error: %s", name, p.Message)
			}

			msgs = append(msgs, p.Message)
		}

		switch joined := strings.Join(msgs, "; "); {
		case tc.want == "" && joined != "":
			t.Errorf("%s: problems %q", name, joined)
		case tc.want != "" && !strings.Contains(joined, tc.want):
			t.Errorf("%s: problems %q, want %q", name, joined, tc.want)
		}
	}
}

func TestFlowNames(t *testing.T) {
	t.Parallel()

	e := &Effective{Flows: map[string]flow.Flow{"local": nil, "default": nil, "direct": nil}}
	if got := strings.Join(e.FlowNames(), " "); got != "default direct local" {
		t.Errorf("names = %q", got)
	}

	if _, ok := e.Flow("LOCAL"); !ok {
		t.Error("a flow is found in any case")
	}
}
