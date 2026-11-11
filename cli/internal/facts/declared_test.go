package facts

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
)

func TestDecodeDeclared(t *testing.T) {
	t.Parallel()

	d, err := decodeDeclared([]byte(
		"merge_method: squash\n" +
			"flows:\n  default: plan → implement → pr\n  local: [implement, commit]\n" +
			"tracker: {kind: github, id: FT-###}\n",
	))
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(d.Flows["default"], "|"); got != "plan|implement|pr" {
		t.Errorf("arrow form = %q", got)
	}

	if got := strings.Join(d.Flows["local"], "|"); got != "implement|commit" {
		t.Errorf("list form = %q", got)
	}

	if bad := d.check(); len(bad) > 0 {
		t.Errorf("a valid block fails its check: %v", bad)
	}

	if _, err := decodeDeclared([]byte("merge_mehtod: squash\n")); err == nil {
		t.Error("an unknown key decoded")
	}

	// the keys flows and paces replaced are refused, each naming its replacement
	for old, want := range map[string]string{
		"drive_finish: off":                 "ceiling: { finish: off }",
		"review_traces: on":                 "`traces`",
		"pipelines: {default: [implement]}": "`flows`",
		"planless: [local]":                 "`flows`",
		"pipeline_by_area: {ui: local}":     "`flow_by_area`",
		"drive_chain: plan ship":            "`flows.default`",
	} {
		_, err := decodeDeclared([]byte(old + "\n"))
		if err == nil || !strings.Contains(err.Error(), "is retired") || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want the retirement naming %s", old, err, want)
		}
	}

	bad := Declared{
		MergeMethod: "fast-forward",
		Tracker:     TrackerDecl{ID: "tasks"},
	}.check()
	for _, want := range []string{"merge_method", "tracker.id"} {
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

	return Build(lay, &probe.Probes{CLI: probe.Signal{Value: "gh"}}, "")
}

func TestBuildFrontmatter(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\n"+
		"visibility: public\ntracker: {kind: gitlab, id: AB-###}\nmerge_method: rebase\ncatchup: merge\ncatchup_stacked: rebase\n"+
		"flows: {default: plan implement pr, local: [implement, commit]}\nwatch_catchup: on\n"+
		"review:\n  reviewer: bot `claude[bot]`\n  arrives: issue comment\n  trigger: automatic\n  done: posted, reaction `eyes`\n"+
		"---\n# demo\n\n## Verify\n\n`just test`\n", "")

	if len(eff.Problems) > 0 {
		t.Fatalf("problems: %+v", eff.Problems)
	}

	if eff.Visibility != "public" || eff.TrackerKind != TrackerGitLab || eff.IDPrefix != "AB" ||
		eff.MergeMethod != "rebase" || eff.Catchup != "merge" || eff.CatchupStacked != "rebase" || len(eff.Flows) != 2 ||
		!eff.WatchCatchup || eff.Review.Reviewer != "claude[bot]" {
		t.Errorf("typed facts = %+v", eff)
	}

	for _, f := range eff.Facts {
		if f.Key == "merge_method" && f.Origin.String() != "declared (main, frontmatter)" {
			t.Errorf("merge_method origin = %q", f.Origin)
		}
	}

	// the prose below the block still parses as it always has
	if f := fieldByName(t, eff, "Verify"); f.Origin.String() != "declared (main)" {
		t.Errorf("Verify = %+v", f)
	}
}

// A key a later layer sets wins outright; one it leaves alone keeps main's.
func TestBuildFrontmatterLayersKeyByKey(t *testing.T) {
	t.Parallel()

	eff := buildWith(t,
		"---\nmerge_method: squash\nflows: {local: [implement, commit]}\n---\n",
		"---\nmerge_method: rebase\n---\n")

	if eff.MergeMethod != "rebase" {
		t.Errorf("merge method = %q, want the worktree's rebase", eff.MergeMethod)
	}

	if _, ok := eff.Flows["local"]; !ok {
		t.Errorf("flows = %v, want main's local kept", eff.Flows)
	}
}

func TestBuildTrackerNameInCode(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\ntracker:\n  kind: github\n---\n"+
		"## Tracker\n\n- **Create** - invoke the `create-notion-task` skill\n", "")
	if eff.TrackerKind != TrackerGitHub || len(eff.Problems) != 0 {
		t.Errorf("a skill name is no tracker: kind %q, problems %+v", eff.TrackerKind, eff.Problems)
	}
}

func TestBuildFrontmatterErrors(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\nmerge_method: fast\nshipping: yes\n---\n", "")
	if !strings.Contains(eff.Errors(), "shipping") {
		t.Errorf("errors = %q", eff.Errors())
	}
}

// standup.upkeep extends the default upkeep paths; a glob standup cannot
// read stops the commands like any value outside its vocabulary.
func TestStandupUpkeep(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\nstandup:\n  upkeep: [db/schema.rb, \"api/**/*.gen.ts\"]\n---\n# demo\n", "")

	if len(eff.Problems) > 0 {
		t.Fatalf("problems: %+v", eff.Problems)
	}

	if got := strings.Join(eff.UpkeepPaths, "|"); got != "db/schema.rb|api/**/*.gen.ts" {
		t.Errorf("upkeep paths = %q", got)
	}

	if !slices.ContainsFunc(eff.Facts, func(f Fact) bool {
		return f.Key == "standup.upkeep" && f.Origin.String() == "declared (main, frontmatter)"
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

// verify is a list of commands, each with optional paths; a later layer
// replaces the list whole, and an entry with no command or a glob standup
// cannot read stops the commands.
func TestVerifyList(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\nverify:\n  - run: just lint\n    paths: [cli/**]\n  - run: just test\n---\n# demo\n", "")

	if len(eff.Problems) > 0 {
		t.Fatalf("problems: %+v", eff.Problems)
	}

	if len(eff.Verify) != 2 || eff.Verify[0].Run != "just lint" || eff.Verify[1].Paths != nil ||
		eff.VerifyOrigin.String() != "declared (main)" {
		t.Errorf("verify = %+v from %q", eff.Verify, eff.VerifyOrigin)
	}

	if !slices.ContainsFunc(eff.Facts, func(f Fact) bool {
		return f.Key == "verify" && f.Value == "just lint (cli/**); just test (every diff)" &&
			f.Origin.String() == "declared (main, frontmatter)"
	}) {
		t.Errorf("no attributed verify fact in %+v", eff.Facts)
	}

	wt := buildWith(
		t,
		"---\nverify:\n  - run: just lint\n  - run: just test\n---\n",
		"---\nverify:\n  - run: go test ./...\n---\n",
	)
	if len(wt.Verify) != 1 || wt.Verify[0].Run != "go test ./..." || wt.VerifyOrigin.String() != "declared (worktree)" {
		t.Errorf("worktree verify = %+v from %q", wt.Verify, wt.VerifyOrigin)
	}

	bad := buildWith(t, "---\nverify:\n  - paths: [/abs]\n  - run: x\n    paths: [\"x/[y\"]\n---\n", "")
	if n := len(bad.Problems); n != 3 {
		t.Errorf("problems = %+v, want an empty run and two bad globs", bad.Problems)
	}

	if empty := buildWith(t, "---\nverify: []\n---\n", ""); len(empty.Problems) != 1 {
		t.Errorf("an empty list: problems = %+v, want one", empty.Problems)
	}

	if _, err := decodeDeclared([]byte("verify:\n  - run: x\n    path: [y]\n")); err == nil {
		t.Error("an unknown verify key decoded")
	}

	if none := buildWith(t, "# demo\n\n## Verify\n\n`just test`\n", ""); none.Verify != nil {
		t.Errorf("prose alone declared a list: %+v", none.Verify)
	}
}

// traces is off unless declared, takes only on or off, and a later layer's
// value wins with its origin.
func TestTraces(t *testing.T) {
	t.Parallel()

	none := buildWith(t, "# demo\n", "")
	if f := tracesFact(t, none); none.Traces || f.Value != "off" || f.Origin.Kind != OriginDefault {
		t.Errorf("undeclared = %v, %+v", none.Traces, f)
	}

	on := buildWith(t, "---\ntraces: on\n---\n# demo\n", "")
	if f := tracesFact(t, on); !on.Traces || f.Value != "on" || f.Origin.String() != "declared (main, frontmatter)" {
		t.Errorf("declared on = %v, %+v", on.Traces, f)
	}

	layered := buildWith(t, "---\ntraces: on\n---\n", "---\ntraces: off\n---\n")
	if f := tracesFact(t, layered); layered.Traces || f.Origin.String() != "declared (worktree, frontmatter)" {
		t.Errorf("worktree off = %v, %+v", layered.Traces, f)
	}

	bad := buildWith(t, "---\ntraces: yes\n---\n", "")
	if !strings.Contains(bad.Errors(), "traces") {
		t.Errorf("errors = %q", bad.Errors())
	}
}

func tracesFact(t *testing.T, eff *Effective) Fact {
	t.Helper()

	return factFor(t, eff, "traces")
}

func factFor(t *testing.T, eff *Effective, key string) Fact {
	t.Helper()

	i := slices.IndexFunc(eff.Facts, func(f Fact) bool { return f.Key == key })
	if i < 0 {
		t.Fatalf("no %s fact in %+v", key, eff.Facts)
	}

	return eff.Facts[i]
}

// lore is local unless declared and takes only local or committed.
func TestLore(t *testing.T) {
	t.Parallel()

	none := buildWith(t, "# demo\n", "")
	if f := factFor(t, none, "lore"); none.Lore != "local" || f.Origin.Kind != OriginDefault {
		t.Errorf("undeclared = %q, %+v", none.Lore, f)
	}

	committed := buildWith(t, "---\nlore: committed\n---\n# demo\n", "")
	if f := factFor(
		t,
		committed,
		"lore",
	); committed.Lore != "committed" ||
		f.Origin.String() != "declared (main, frontmatter)" {
		t.Errorf("declared committed = %q, %+v", committed.Lore, f)
	}

	bad := buildWith(t, "---\nlore: shared\n---\n", "")
	if !strings.Contains(bad.Errors(), "lore") {
		t.Errorf("errors = %q", bad.Errors())
	}
}

// tracker.priority: a level outside the vocabulary or a label under two
// levels is an error; an empty level is not.
func TestTrackerPriority(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\ntracker:\n  kind: github\n  id: FT-###\n  priority:\n"+
		"    low: P3\n    high: [P1, \"priority:high\"]\n    medium: []\n---\n# demo\n", "")

	if len(eff.Problems) > 0 {
		t.Fatalf("problems: %+v", eff.Problems)
	}

	if eff.IDPrefix != "FT" || eff.TrackerKind != TrackerGitHub {
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
		eff.Facts[i].Origin.String() != "declared (main, frontmatter)" {
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

// The report-back facts: the status map, the area map onto declared
// flows, and report_back on unless declared off; a worktree's area map
// replaces main's whole and may name main's flows.
func TestBuildReportBackFacts(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\n"+
		"tracker: {kind: notion, id: MR-###, status: {started: In progress, shipped: Published}}\n"+
		"flows: {default: implement publish, engine: implement cleanup pr}\n"+
		"flow_by_area: {report work: default, framework work: engine}\n"+
		"---\n", "")

	if len(eff.Problems) > 0 {
		t.Fatalf("problems: %+v", eff.Problems)
	}

	if eff.TrackerStatus["shipped"] != "Published" || eff.FlowByArea["framework work"] != "engine" || !eff.ReportBack {
		t.Errorf("status %v, by area %v, report back %v", eff.TrackerStatus, eff.FlowByArea, eff.ReportBack)
	}

	layered := buildWith(
		t,
		"---\nflows: {default: [implement, pr], engine: [implement, pr]}\nflow_by_area: {a: engine, b: engine}\n---\n",
		"---\nflow_by_area: {c: engine}\nreport_back: off\n---\n",
	)

	if len(layered.Problems) > 0 || len(layered.FlowByArea) != 1 || layered.ReportBack {
		t.Errorf(
			"layered: by area %v, report back %v, problems %+v",
			layered.FlowByArea,
			layered.ReportBack,
			layered.Problems,
		)
	}

	// one layer, no flows of its own: the map is checked against the default
	unknown := buildWith(t, "---\nflow_by_area: {report work: publish}\n---\n", "")
	if !strings.Contains(unknown.Errors(), `flow_by_area: "report work" maps to "publish"`) {
		t.Errorf("an area mapped to no flow passed: %+v", unknown.Problems)
	}

	bad := Declared{
		ReportBack: "yes",
		Tracker:    TrackerDecl{Status: map[string]string{"done": "Done", "shipped": " "}},
	}.check()
	for _, want := range []string{"report_back", `tracker.status: "done"`, "tracker.status: shipped"} {
		if !slices.ContainsFunc(bad, func(b string) bool { return strings.HasPrefix(b, want) }) {
			t.Errorf("check misses %s: %v", want, bad)
		}
	}
}

// catchup: off turns ship's guard off; the Hosting & PR prose sets
// nothing, and an unknown method is still an error.
func TestCatchupOff(t *testing.T) {
	t.Parallel()

	if eff := buildWith(t, "---\ncatchup: off\n---\n", ""); eff.Catchup != "off" || eff.Errors() != "" {
		t.Errorf("catchup %q, errors %q", eff.Catchup, eff.Errors())
	}

	if eff := buildWith(t, "# demo\n\n## Hosting & PR\n\ngithub.com. catchup: off\n", ""); eff.Catchup != "rebase" {
		t.Errorf("prose catchup read: %q", eff.Catchup)
	}

	if eff := buildWith(t, "---\ncatchup: squash\n---\n", ""); !strings.Contains(eff.Errors(), "catchup") {
		t.Errorf("an unknown method is not an error: %q", eff.Errors())
	}
}

// catchup_stacked picks the method for a base that is another task's
// branch: merge unless declared, rebase on request, nothing else.
func TestCatchupStacked(t *testing.T) {
	t.Parallel()

	none := buildWith(t, "# demo\n", "")
	if f := factFor(t, none, "catchup_stacked"); none.CatchupStacked != "merge" || f.Origin.Kind != OriginDefault {
		t.Errorf("default = %q (%s), want merge by default", none.CatchupStacked, f.Origin)
	}

	eff := buildWith(t, "---\ncatchup_stacked: rebase\n---\n", "")
	if eff.CatchupStacked != "rebase" || eff.Errors() != "" {
		t.Errorf("declared: catchup_stacked %q, errors %q", eff.CatchupStacked, eff.Errors())
	}

	if eff := buildWith(t, "---\ncatchup_stacked: off\n---\n", ""); !strings.Contains(eff.Errors(), "catchup_stacked") {
		t.Errorf("off is not an error: %q", eff.Errors())
	}
}

func TestStaleAfter(t *testing.T) {
	t.Parallel()

	none := buildWith(t, "# demo\n", "")
	if f := factFor(
		t,
		none,
		"stale_after",
	); none.StaleAfter.On() || f.Origin.Kind != OriginDefault ||
		f.Value != "off" {
		t.Errorf("undeclared = %q, %+v", none.StaleAfter, f)
	}

	for _, v := range []string{"10d", "3w"} {
		if eff := buildWith(
			t,
			"---\nstale_after: "+v+"\n---\n",
			"",
		); eff.StaleAfter.String() != v ||
			eff.Errors() != "" {
			t.Errorf("%s: stale_after %q, errors %q", v, eff.StaleAfter, eff.Errors())
		}
	}

	for _, v := range []string{"0d", "3m", "3", "w", "-1w", "1.5w", "99999999999999999999d"} {
		if eff := buildWith(t, "---\nstale_after: "+v+"\n---\n", ""); !strings.Contains(eff.Errors(), "stale_after") {
			t.Errorf("%s is not an error: %q", v, eff.Errors())
		}
	}

	if eff := buildWith(
		t,
		"---\nstale_after: 3w\n---\n",
		"---\nstale_after: 10d\n---\n",
	); eff.StaleAfter.String() != "10d" {
		t.Errorf("stale_after = %q, want the worktree's 10d", eff.StaleAfter)
	}

	if d, err := ParseStaleAfter("3w"); err != nil || d != 21*24*time.Hour {
		t.Errorf("3w = %v, %v", d, err)
	}
}

func TestDriverTimeout(t *testing.T) {
	t.Parallel()

	none := buildWith(t, "# demo\n", "")
	if f := factFor(
		t,
		none,
		"driver_timeout",
	); none.DriverTimeout != 30*time.Second || f.Origin.Kind != OriginDefault ||
		f.Value != "30s" {
		t.Errorf("undeclared = %s, %+v", none.DriverTimeout, f)
	}

	for v, want := range map[string]time.Duration{"45s": 45 * time.Second, "2m": 2 * time.Minute} {
		if eff := buildWith(
			t,
			"---\ndriver_timeout: "+v+"\n---\n",
			"",
		); eff.DriverTimeout != want ||
			eff.Errors() != "" {
			t.Errorf("%s: driver_timeout %s, errors %q", v, eff.DriverTimeout, eff.Errors())
		}
	}

	for _, v := range []string{"0s", "-5s", "30", "soon", "1d"} {
		if eff := buildWith(
			t,
			"---\ndriver_timeout: "+v+"\n---\n",
			"",
		); !strings.Contains(
			eff.Errors(),
			"driver_timeout",
		) {
			t.Errorf("%s is not an error: %q", v, eff.Errors())
		}
	}

	if eff := buildWith(
		t,
		"---\ndriver_timeout: 2m\n---\n",
		"---\ndriver_timeout: 10s\n---\n",
	); eff.DriverTimeout != 10*time.Second {
		t.Errorf("driver_timeout = %s, want the worktree's 10s", eff.DriverTimeout)
	}
}

// tracker.title is read into the effective template and shown as a fact; a
// template with no {title} is an error and leaves titles bare.
func TestTrackerTitle(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\ntracker: {id: FT-###, title: \"{id}: {title}\"}\n---\n", "")
	if eff.TitleTemplate != "{id}: {title}" {
		t.Errorf("TitleTemplate = %q", eff.TitleTemplate)
	}

	if !slices.ContainsFunc(eff.Facts, func(f Fact) bool { return f.Key == "tracker.title" }) {
		t.Errorf("no tracker.title fact: %+v", eff.Facts)
	}

	bad := buildWith(t, "---\ntracker: {title: \"{id}\"}\n---\n", "")
	if !strings.Contains(bad.Errors(), "tracker.title:") || bad.TitleTemplate != "" {
		t.Errorf("a template without {title} passed: %q, %+v", bad.TitleTemplate, bad.Problems)
	}

	if none := buildWith(t, "---\ntracker: {id: FT-###}\n---\n", ""); none.TitleTemplate != "" {
		t.Errorf("no key gave template %q", none.TitleTemplate)
	}
}

func TestDriveEnv(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\ndrive_env: [ZIG, AWS_PROFILE]\n---\n", "")
	if !slices.Equal(eff.DriveEnv, []string{"ZIG", "AWS_PROFILE"}) || eff.Errors() != "" {
		t.Errorf("drive_env %v, errors %q", eff.DriveEnv, eff.Errors())
	}

	if f := factFor(t, eff, "drive_env"); f.Value != "ZIG, AWS_PROFILE" {
		t.Errorf("fact = %+v", f)
	}

	if eff := buildWith(t, "---\ndrive_env: [\"A-B\"]\n---\n", ""); !strings.Contains(eff.Errors(), "drive_env") {
		t.Errorf("a bad key passed: %q", eff.Errors())
	}
}

func TestWatch(t *testing.T) {
	t.Parallel()

	if eff := buildWith(t, "", ""); eff.Watch != "finish" {
		t.Errorf("default watch = %q, want finish", eff.Watch)
	}

	eff := buildWith(t, "---\nwatch: report\n---\n", "")
	if eff.Watch != "report" || eff.Errors() != "" {
		t.Errorf("watch %q, errors %q", eff.Watch, eff.Errors())
	}

	if f := factFor(t, eff, "watch"); f.Value != "report" {
		t.Errorf("fact = %+v", f)
	}

	if eff := buildWith(t, "---\nwatch: always\n---\n", ""); !strings.Contains(eff.Errors(), "watch") {
		t.Errorf("a bad value passed: %q", eff.Errors())
	}
}

// watch_catchup is off unless declared, and takes only on or off.
func TestWatchCatchup(t *testing.T) {
	t.Parallel()

	none := buildWith(t, "", "")
	if f := factFor(t, none, "watch_catchup"); none.WatchCatchup || f.Origin.Kind != OriginDefault {
		t.Errorf("undeclared = %v, %+v", none.WatchCatchup, f)
	}

	on := buildWith(t, "---\nwatch_catchup: on\n---\n", "")
	if f := factFor(t, on, "watch_catchup"); !on.WatchCatchup || on.Errors() != "" ||
		f.Origin.String() != "declared (main, frontmatter)" {
		t.Errorf("declared on = %v, errors %q, %+v", on.WatchCatchup, on.Errors(), f)
	}

	if bad := buildWith(t, "---\nwatch_catchup: yes\n---\n", ""); !strings.Contains(bad.Errors(), "watch_catchup") {
		t.Errorf("errors = %q", bad.Errors())
	}
}

// lore_gate warns unless declared and takes only warn or block.
func TestLoreGate(t *testing.T) {
	t.Parallel()

	none := buildWith(t, "# demo\n", "")
	if f := factFor(t, none, "lore_gate"); none.LoreGate != "warn" || f.Origin.Kind != OriginDefault {
		t.Errorf("undeclared = %q, %+v", none.LoreGate, f)
	}

	block := buildWith(t, "---\nlore_gate: block\n---\n# demo\n", "")
	if f := factFor(
		t,
		block,
		"lore_gate",
	); block.LoreGate != "block" ||
		f.Origin.String() != "declared (main, frontmatter)" {
		t.Errorf("declared block = %q, %+v", block.LoreGate, f)
	}

	bad := buildWith(t, "---\nlore_gate: off\n---\n", "")
	if !strings.Contains(bad.Errors(), "lore_gate") {
		t.Errorf("errors = %q", bad.Errors())
	}
}

func TestAgents(t *testing.T) {
	t.Parallel()

	eff := buildWith(t, "---\nflows:\n  default: plan judge implement cleanup pr review:local judge pr\n"+
		"agents:\n  default: opus:high\n  review: opus:high\n  cleanup: sonnet:low\n  plan.bug: opus:medium\n---\n",
		"---\nagents:\n  cleanup: haiku:low\n  judge: opus:max\n---\n")
	if eff.Errors() != "" {
		t.Fatalf("errors: %s", eff.Errors())
	}

	want := map[string]string{
		"default": "opus:high", "review": "opus:high", "cleanup": "haiku:low",
		"plan.bug": "opus:medium", "judge": "opus:max",
	}
	if !maps.Equal(eff.Agents, want) {
		t.Errorf("agents %v, want %v", eff.Agents, want)
	}

	for key, origin := range map[string]string{
		"default": "declared (main, frontmatter)",
		"cleanup": "declared (worktree, frontmatter)",
		"judge":   "declared (worktree, frontmatter)",
	} {
		if eff.AgentsOrigin[key].String() != origin {
			t.Errorf("agents.%s origin %q, want %q", key, eff.AgentsOrigin[key], origin)
		}
	}

	if f := factFor(t, eff, "agents.cleanup"); f.Origin.String() != "declared (worktree, frontmatter)" ||
		f.Value != "haiku:low" {
		t.Errorf("agents.cleanup fact %+v", f)
	}

	for _, c := range []struct{ block, want string }{
		{"agents:\n  reveiw: opus:high\n", "agents.reveiw: no flow has a reveiw entry"},
		{"agents:\n  plan.feature.x: opus\n", "agents.plan.feature.x: feature.x is not a task kind"},
		{"agents:\n  implement.huge: opus\n", "huge is not a task kind (feature, bug, refactor, spike)"},
		{"agents:\n  implement: opus:huge\n", "agents.implement: \"opus:huge\": huge is not an effort claude knows"},
		{"agents:\n  implement: opencode:opus:high\n", "opencode is not an agent kind"},
		{"agents:\n  --agent: opus:high\n", "agents.--agent: a key is default, a step, or step.kind"},
	} {
		if eff := buildWith(t, "---\n"+c.block+"---\n", ""); !strings.Contains(eff.Errors(), c.want) {
			t.Errorf("%q: errors %q, want %q", c.block, eff.Errors(), c.want)
		}
	}

	flowSteps := "---\nagents:\n  pr: sonnet:low\n  implement.bug: opus:medium\n---\n"
	if eff := buildWith(t, flowSteps, ""); eff.Errors() != "" {
		t.Errorf("the default flow's steps are keys: %s", eff.Errors())
	}

	// audit and judge --blind spawn agents with no flow entry behind them
	standalone := "---\nflows:\n  default: implement pr\nagents:\n  audit: opus:high\n  judge: opus:max\n---\n"
	if eff := buildWith(t, standalone, ""); eff.Errors() != "" {
		t.Errorf("standalone spawners are keys without a flow entry: %s", eff.Errors())
	}

	if eff := buildWith(t, "---\nvisibility: public\n---\n", ""); eff.Agents != nil {
		t.Errorf("no map declared leaves agents nil: %v", eff.Agents)
	}
}

// Every single-word key is in the scalar registry, since check, merge,
// scaffold and resolve only see the keys it lists.
func TestScalarKeysCoverDeclared(t *testing.T) {
	t.Parallel()

	for f := range reflect.TypeFor[Declared]().Fields() {
		if f.Type.Kind() != reflect.String {
			continue
		}

		key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if !slices.ContainsFunc(scalarKeys, func(k scalarKey) bool { return k.key == key }) {
			t.Errorf("%s (%s) is a string key the scalar registry lacks", key, f.Name)
		}
	}
}
