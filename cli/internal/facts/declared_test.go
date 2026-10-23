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
		"- **Trigger** - automatic\n- **Done, handled** - posted, reaction `eyes`\n" +
		"- **Settled** - reaction `rocket`\n"

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
		d.MergeMethod != "squash" || len(d.Pipelines["local"]) != 2 || d.Review.Reviewer != "bot `claude[bot]`" ||
		d.Review.Settled != "reaction `rocket`" {
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

// review_traces is off unless declared, takes only on or off, and a later
// layer's value wins with its origin.
func TestReviewTraces(t *testing.T) {
	traces := func(eff *Effective) Fact {
		t.Helper()

		i := slices.IndexFunc(eff.Facts, func(f Fact) bool { return f.Key == "review_traces" })
		if i < 0 {
			t.Fatalf("no review_traces fact in %+v", eff.Facts)
		}

		return eff.Facts[i]
	}

	none := buildWith(t, "# demo\n", "")
	if f := traces(none); none.ReviewTraces || f.Value != "off" || f.Origin != OriginDefault {
		t.Errorf("undeclared = %v, %+v", none.ReviewTraces, f)
	}

	on := buildWith(t, "---\nreview_traces: on\n---\n# demo\n", "")
	if f := traces(on); !on.ReviewTraces || f.Value != "on" || f.Origin != "declared (main, frontmatter)" {
		t.Errorf("declared on = %v, %+v", on.ReviewTraces, f)
	}

	layered := buildWith(t, "---\nreview_traces: on\n---\n", "---\nreview_traces: off\n---\n")
	if f := traces(layered); layered.ReviewTraces || f.Origin != "declared (worktree, frontmatter)" {
		t.Errorf("worktree off = %v, %+v", layered.ReviewTraces, f)
	}

	bad := buildWith(t, "---\nreview_traces: yes\n---\n", "")
	if !strings.Contains(bad.Errors(), "review_traces") {
		t.Errorf("errors = %q", bad.Errors())
	}
}
