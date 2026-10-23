package facts

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/frontmatter"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
)

func TestDecodeDeclared(t *testing.T) {
	d, err := decodeDeclared([]byte(
		"merge_method: squash\n" +
			"pipelines:\n  default: implement → review (local) → pr\n  local: [implement, commit]\n" +
			"planless: [local]\n" +
			"drive_chain: plan -> ship, finish\n" +
			"tracker: {kind: github, id: FT-###}\n",
	))
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(d.Pipelines["default"], "|"); got != "implement|review (local)|pr" {
		t.Errorf("arrow form = %q", got)
	}

	if got := strings.Join(d.Pipelines["local"], "|"); got != "implement|commit" {
		t.Errorf("list form = %q", got)
	}

	if got := strings.Join(d.DriveChain, "|"); got != "plan|ship|finish" {
		t.Errorf("drive chain = %q", got)
	}

	if bad := d.check(); len(bad) > 0 {
		t.Errorf("a valid block fails its check: %v", bad)
	}

	if _, err := decodeDeclared([]byte("merge_mehtod: squash\n")); err == nil {
		t.Error("an unknown key decoded")
	}

	bad := Declared{
		MergeMethod: "fast-forward",
		Tracker:     TrackerDecl{ID: "tasks"},
		Planless:    []string{"direct"},
	}.check()
	for _, want := range []string{"merge_method", "tracker.id", "planless"} {
		if !slices.ContainsFunc(bad, func(b string) bool { return strings.HasPrefix(b, want) }) {
			t.Errorf("check misses %s: %v", want, bad)
		}
	}
}

// buildWith runs Build over one main layer, and a worktree layer when given.
func buildWith(t *testing.T, main, worktree string) *Effective {
	t.Helper()

	lay := &layout.Layout{Project: "demo", StorageRoot: "/repo/.f10", Layering: layout.LayeringExtends}
	lay.Layers = append(lay.Layers, layout.Layer{Label: "main", Dir: dirWithProjectMD(t, main)})

	if worktree != "" {
		lay.Layers = append(lay.Layers, layout.Layer{Label: "worktree", Dir: dirWithProjectMD(t, worktree)})
	}

	return Build(lay, &probe.Probes{CLI: probe.Signal{Value: "gh"}})
}

func TestBuildFrontmatter(t *testing.T) {
	eff := buildWith(t, "---\n"+
		"visibility: public\ntracker: {kind: gitlab, id: AB-###}\nmerge_method: rebase\ncatchup: merge\n"+
		"pipelines: {default: [implement, pr], local: [implement, commit]}\ndrive_chain: [plan, ship]\n"+
		"review:\n  reviewer: bot `claude[bot]`\n  arrives: issue comment\n  trigger: automatic\n  done: posted, reaction `eyes`\n"+
		"---\n# demo\n\n## Verify\n\n`just test`\n", "")

	if len(eff.Problems) > 0 {
		t.Fatalf("problems: %+v", eff.Problems)
	}

	if eff.Visibility != "public" || eff.TrackerKind != "GitLab work items" || eff.IDPrefix != "AB" ||
		eff.MergeMethod != "rebase" || eff.Catchup != "merge" || len(eff.Pipelines) != 2 ||
		strings.Join(eff.DriveChain, " ") != "plan ship" || eff.Review.Reviewer != "claude[bot]" {
		t.Errorf("typed facts = %+v", eff)
	}

	for _, f := range eff.Facts {
		if f.Key == "merge_method" && f.Origin != "declared (main, frontmatter)" {
			t.Errorf("merge_method origin = %q", f.Origin)
		}
	}

	// the prose below the block still parses as it always has
	if f := fieldByName(t, eff, "Verify"); f.Origin != "declared (main)" {
		t.Errorf("Verify = %+v", f)
	}
}

// A key a later layer sets wins outright; one it leaves alone keeps main's.
func TestBuildFrontmatterLayersKeyByKey(t *testing.T) {
	eff := buildWith(t,
		"---\nmerge_method: squash\npipelines: {local: [implement, commit]}\n---\n",
		"---\nmerge_method: rebase\n---\n")

	if eff.MergeMethod != "rebase" {
		t.Errorf("merge method = %q, want the worktree's rebase", eff.MergeMethod)
	}

	if _, ok := eff.Pipelines["local"]; !ok {
		t.Errorf("pipelines = %v, want main's local kept", eff.Pipelines)
	}
}

func TestBuildProseFallback(t *testing.T) {
	eff := buildWith(t, "# demo\n\n## Hosting & PR\n\ngithub.com. **merge method**: `squash`.\n\n"+
		"## Ship pipelines\n\n- **default**: `implement → pr`\n- **local**: implement → commit\n"+
		"- **direct** (planless): `implement → push`\n\n## Drive chain\n\n`plan` → ship\n", "")

	if eff.MergeMethod != "squash" || strings.Join(eff.DriveChain, " ") != "plan ship" {
		t.Errorf("merge method %q, chain %v", eff.MergeMethod, eff.DriveChain)
	}

	if got := strings.Join(
		eff.Pipelines["direct"],
		" ",
	); got != "implement push" ||
		!slices.Equal(eff.Planless, []string{"direct"}) {
		t.Errorf("direct = %q, planless = %v", got, eff.Planless)
	}

	if eff.Errors() != "" || len(eff.Problems) != 1 ||
		!strings.Contains(eff.Problems[0].Message, "f10 init --migrate") {
		t.Errorf("want one migrate warning, got %+v", eff.Problems)
	}
}

func TestBuildBothPlaces(t *testing.T) {
	differ := buildWith(t, "---\nmerge_method: rebase\n---\n## Hosting & PR\n\nmerge method: squash\n", "")
	if !strings.Contains(differ.Errors(), "merge_method") || differ.MergeMethod != "rebase" {
		t.Errorf("a disagreement is not an error: %+v", differ.Problems)
	}

	same := buildWith(t, "---\nmerge_method: squash\n---\n## Hosting & PR\n\nmerge method: squash\n", "")
	if same.Errors() != "" || len(same.Problems) != 1 ||
		!strings.Contains(same.Problems[0].Message, "delete the prose") {
		t.Errorf("agreeing places should warn once: %+v", same.Problems)
	}
}

func TestBuildFrontmatterErrors(t *testing.T) {
	eff := buildWith(t, "---\nmerge_method: fast\nshipping: yes\n---\n", "")
	if !strings.Contains(eff.Errors(), "shipping") {
		t.Errorf("errors = %q", eff.Errors())
	}
}

func TestMigrate(t *testing.T) {
	prose := "# demo\n\n## Visibility\n\nstealth\n\n## Tracker\n\nGitHub Issues. Task ids `DM-###`.\n\n" +
		"## Hosting & PR\n\nOpen PRs with gh. merge method: squash.\n\n" +
		"## Ship pipeline(s)\n\n- **local**: `implement → commit`\n\n" +
		"## Review\n\n- **Reviewer** - bot `claude[bot]`\n- **Arrives as** - issue comment\n" +
		"- **Trigger** - automatic\n- **Done, handled** - posted, reaction `eyes`\n"

	m, err := Migrate([]byte(prose), "project.md")
	if err != nil {
		t.Fatal(err)
	}

	block, body, ok := frontmatter.Split([]byte(m.Content))
	if !ok || string(body) != prose {
		t.Fatalf("the prose did not survive under the block:\n%s", m.Content)
	}

	d, err := decodeDeclared(block)
	if err != nil {
		t.Fatal(err)
	}

	if d.Visibility != "stealth" || d.Tracker.ID != "DM-###" || d.Tracker.Kind != "github" ||
		d.MergeMethod != "squash" || len(d.Pipelines["local"]) != 2 || d.Review.Reviewer != "bot `claude[bot]`" {
		t.Errorf("migrated = %+v", d)
	}

	// the migrated file reads the same facts, now from the frontmatter
	before, after := buildWith(t, prose, ""), buildWith(t, m.Content, "")
	if before.Review != after.Review || before.MergeMethod != after.MergeMethod || before.IDPrefix != after.IDPrefix {
		t.Errorf("facts drifted across the migration:\n%+v\n%+v", before, after)
	}

	if _, err := Migrate([]byte(m.Content), "project.md"); !errors.Is(err, ErrHasFrontmatter) {
		t.Errorf("a second migration = %v", err)
	}
}

// standup.upkeep extends the default upkeep paths; a glob standup cannot
// read stops the commands like any value outside its vocabulary.
func TestStandupUpkeep(t *testing.T) {
	eff := buildWith(t, "---\nstandup:\n  upkeep: [db/schema.rb, \"api/**/*.gen.ts\"]\n---\n# demo\n", "")

	if len(eff.Problems) > 0 {
		t.Fatalf("problems: %+v", eff.Problems)
	}

	if got := strings.Join(eff.UpkeepPaths, "|"); got != "db/schema.rb|api/**/*.gen.ts" {
		t.Errorf("upkeep paths = %q", got)
	}

	if !slices.ContainsFunc(eff.Facts, func(f Fact) bool {
		return f.Key == "standup.upkeep" && f.Origin == "declared (main, frontmatter)"
	}) {
		t.Errorf("no attributed standup.upkeep fact in %+v", eff.Facts)
	}

	bad := buildWith(t, "---\nstandup:\n  upkeep: [/abs/path, \"x/[y\"]\n---\n# demo\n", "")
	if n := len(bad.Problems); n != 2 {
		t.Errorf("problems = %+v, want one per bad glob", bad.Problems)
	}

	if _, err := decodeDeclared([]byte("standup:\n  upkep: [x]\n")); err == nil {
		t.Error("an unknown standup key decoded")
	}
}

// traces is off unless declared, takes only on or off, and a later layer's
// value wins with its origin.
func TestTraces(t *testing.T) {
	none := buildWith(t, "# demo\n", "")
	if f := tracesFact(t, none); none.Traces || f.Value != "off" || f.Origin != OriginDefault {
		t.Errorf("undeclared = %v, %+v", none.Traces, f)
	}

	on := buildWith(t, "---\ntraces: on\n---\n# demo\n", "")
	if f := tracesFact(t, on); !on.Traces || f.Value != "on" || f.Origin != "declared (main, frontmatter)" {
		t.Errorf("declared on = %v, %+v", on.Traces, f)
	}

	layered := buildWith(t, "---\ntraces: on\n---\n", "---\ntraces: off\n---\n")
	if f := tracesFact(t, layered); layered.Traces || f.Origin != "declared (worktree, frontmatter)" {
		t.Errorf("worktree off = %v, %+v", layered.Traces, f)
	}

	bad := buildWith(t, "---\ntraces: yes\n---\n", "")
	if !strings.Contains(bad.Errors(), "traces") {
		t.Errorf("errors = %q", bad.Errors())
	}
}

func tracesFact(t *testing.T, eff *Effective) Fact {
	t.Helper()

	i := slices.IndexFunc(eff.Facts, func(f Fact) bool { return f.Key == "traces" })
	if i < 0 {
		t.Fatalf("no traces fact in %+v", eff.Facts)
	}

	return eff.Facts[i]
}

// review_traces, the old spelling, still reads as traces with a warning
// naming the migration; folded per layer, so a worktree's old spelling
// still overrides main's new one. Both in one layer is an error.
func TestReviewTracesIsDeprecatedTraces(t *testing.T) {
	old := buildWith(t, "---\nreview_traces: on\n---\n# demo\n", "")
	if f := tracesFact(t, old); !old.Traces || f.Origin != "declared (main, frontmatter)" {
		t.Errorf("old spelling = %v, %+v", old.Traces, f)
	}

	if old.Errors() != "" {
		t.Errorf("the old spelling is an error: %q", old.Errors())
	}

	if !slices.ContainsFunc(old.Problems, func(p Problem) bool {
		return !p.Error && strings.Contains(p.Message, "review_traces is now traces")
	}) {
		t.Errorf("no migration warning in %+v", old.Problems)
	}

	layered := buildWith(t, "---\ntraces: off\n---\n", "---\nreview_traces: on\n---\n")
	if f := tracesFact(t, layered); !layered.Traces || f.Origin != "declared (worktree, frontmatter)" {
		t.Errorf("worktree's old spelling lost to main = %v, %+v", layered.Traces, f)
	}

	both := buildWith(t, "---\ntraces: on\nreview_traces: off\n---\n", "")
	if !strings.Contains(both.Errors(), "both declared") {
		t.Errorf("errors = %q", both.Errors())
	}

	bad := buildWith(t, "---\nreview_traces: yes\n---\n", "")
	if !strings.Contains(bad.Errors(), "review_traces") {
		t.Errorf("errors = %q", bad.Errors())
	}
}

// tracker.priority maps host labels onto the contract's levels: a string or
// a list per level, indexed case-insensitively, shown in vocabulary order
// with its origin. A level outside the vocabulary or a label under two
// levels is an error; an empty level is not.
func TestTrackerPriority(t *testing.T) {
	eff := buildWith(t, "---\ntracker:\n  kind: github\n  id: FT-###\n  priority:\n"+
		"    low: P3\n    high: [P1, \"priority:high\"]\n    medium: []\n---\n# demo\n", "")

	if len(eff.Problems) > 0 {
		t.Fatalf("problems: %+v", eff.Problems)
	}

	if eff.IDPrefix != "FT" || eff.TrackerKind != "GitHub Issues" {
		t.Errorf("tracker beside the map = %q, %q", eff.IDPrefix, eff.TrackerKind)
	}

	want := map[string]string{"p3": "low", "p1": "high", "priority:high": "high"}
	if len(eff.PriorityLabels) != len(want) {
		t.Errorf("index = %v, want %v", eff.PriorityLabels, want)
	}

	for label, level := range want {
		if eff.PriorityLabels[label] != level {
			t.Errorf("index[%q] = %q, want %q", label, eff.PriorityLabels[label], level)
		}
	}

	i := slices.IndexFunc(eff.Facts, func(f Fact) bool { return f.Key == "tracker.priority" })
	if i < 0 || eff.Facts[i].Value != "high: P1, priority:high; low: P3" ||
		eff.Facts[i].Origin != "declared (main, frontmatter)" {
		t.Errorf("fact = %+v", eff.Facts)
	}

	none := buildWith(t, "---\ntracker: {kind: github, id: FT-###}\n---\n# demo\n", "")
	if none.PriorityLabels != nil ||
		slices.ContainsFunc(none.Facts, func(f Fact) bool { return f.Key == "tracker.priority" }) {
		t.Errorf("undeclared map = %v, %+v", none.PriorityLabels, none.Facts)
	}

	// a worktree that declares tracker: replaces the whole block, map included
	wt := buildWith(t, "---\ntracker:\n  id: FT-###\n  priority: {high: P1}\n---\n",
		"---\ntracker: {id: FT-###}\n---\n")
	if wt.PriorityLabels != nil {
		t.Errorf("worktree tracker without a map kept main's: %v", wt.PriorityLabels)
	}

	bad := buildWith(t, "---\ntracker:\n  priority:\n    highest: P0\n    high: [P1, p2]\n    low: [P2]\n---\n", "")
	for _, msg := range []string{`"highest" is not one of urgent, high, medium, low`, `label "P2" is listed under both high and low`} {
		if !strings.Contains(bad.Errors(), msg) {
			t.Errorf("errors = %q, want %q", bad.Errors(), msg)
		}
	}

	if _, err := decodeDeclared([]byte("tracker:\n  priority:\n    high: {label: P1}\n")); err == nil {
		t.Error("a mapping as a level's labels decoded")
	}
}

// A frontmatter block with the old spelling is renamed in place, nothing
// else in the file moving; one without it has nothing left to migrate.
func TestMigrateRenamesReviewTraces(t *testing.T) {
	file := "---\n# traces first\nreview_traces: on\nmerge_method: squash\n---\n# demo\n\nreview_traces: in prose stays\n"

	m, err := Migrate([]byte(file), "project.md")
	if err != nil {
		t.Fatal(err)
	}

	want := "---\n# traces first\ntraces: on\nmerge_method: squash\n---\n# demo\n\nreview_traces: in prose stays\n"
	if m.Content != want || strings.Join(m.Moved, ",") != "traces" || len(m.Superseded) != 0 {
		t.Errorf("migration = %+v", m)
	}

	if _, err := Migrate([]byte(want), "project.md"); !errors.Is(err, ErrHasFrontmatter) {
		t.Errorf("nothing left = %v", err)
	}
}
