package drive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/tasksource"
)

// planFixture is a parent, GH-10, whose subtasks the task describes, plus a
// lone task GH-30 with a bug label.
func planFixture(t *testing.T) PlanInput {
	t.Helper()

	rows := []stack.ChildRow{
		{ID: "GH-11", Title: "copy one"},
		{ID: "GH-12", Title: "copy two"},
		{ID: "GH-13", Title: "copy three"},
		{ID: "GH-14", Title: "ledger kind"},
		{ID: "GH-15", Title: "forge"},
		{ID: "GH-16", Title: "docs"},
		{ID: "GH-17", After: "GH-14", Depth: 1},
		{ID: "GH-18", After: "GH-14", Depth: 1},
		{ID: "GH-19", Labels: []string{"spike"}},
		{ID: "GH-20", Labels: []string{"migration"}},
		{ID: "GH-21", Labels: []string{"draft"}},
		{ID: "GH-22", Finished: true},
	}

	root := storage.Root(t.TempDir())
	if err := os.MkdirAll(root.Plans(), 0o750); err != nil {
		t.Fatal(err)
	}

	// a saved plan's header settles its areas
	plan := "# GH-14: ledger kind\n\nRoles: x\nAreas: ledger\nSize: small\n\n## Brief\n\nAreas: not this\n"
	if err := os.WriteFile(root.Plan("GH-14"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}

	return PlanInput{
		Storage:         root,
		MaxLines:        300,
		DefaultParallel: 4,
		FlowByArea:      map[string]string{"docs": "prose"},
		Tracker: stack.Tracker{
			Resolve: func(ctx context.Context, tok string) (ref.Ref, error) {
				return ref.Lookup{Prefix: "GH"}.Find(ctx, tok)
			},
			Children: func(_ context.Context, r ref.Ref) ([]stack.ChildRow, error) {
				if r.ID == "GH-10" {
					return rows, nil
				}

				return nil, tasksource.ErrNoParentLink
			},
			Info: func(_ context.Context, r ref.Ref) (tasksource.TaskInfo, error) {
				if r.ID == "GH-30" {
					return tasksource.TaskInfo{Body: "fix\n\nAfter: GH-14\n", Labels: []string{"regression"}}, nil
				}

				if r.ID == "GH-14" {
					return tasksource.TaskInfo{Body: "a ledger kind"}, nil
				}

				return tasksource.TaskInfo{}, errors.New("no such issue")
			},
		},
	}
}

func writeSheet(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "facts.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func planArgs(t *testing.T, args ...string) drivectl.DriveArgs {
	t.Helper()

	a, err := drivectl.ParsePropose(args)
	if err != nil {
		t.Fatal(err)
	}

	return a
}

func TestPlanFromAParent(t *testing.T) {
	t.Parallel()

	in := planFixture(t)
	sheet := writeSheet(t, `{
		"GH-11": {"areas": ["ui"], "lines": 20}, "GH-12": {"areas": ["ui"], "lines": 20},
		"gh-13": {"areas": ["ui"], "lines": 20},
		"GH-14": {"areas": ["ignored, the plan wins"], "lines": 80},
		"GH-15": {"areas": ["forge"], "lines": 80}, "GH-16": {"areas": ["docs"], "lines": 80},
		"GH-17": {"areas": ["status"], "lines": 40}, "GH-18": {"areas": ["status"], "lines": 30},
		"GH-20": {"lines": 50}
	}`)

	res, err := Plan(context.Background(), in, planArgs(t, "GH-10", "--pace", "fast", "--facts", sheet))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"GH-11+GH-12+GH-13", "GH-14,GH-17+GH-18", "GH-15", "GH-16", "GH-19", "GH-20", "--parallel", "4"}
	if !slices.Equal(res.Plan.Args, want) {
		t.Errorf("args %v, want %v", res.Plan.Args, want)
	}

	if cmd := res.Plan.Command(res.Extra...); !strings.HasSuffix(cmd, "--parallel 4 --pace fast") {
		t.Errorf("command %q carries no --pace", cmd)
	}

	// the command is one drive takes as it is
	a, err := drivectl.ParseDriveArgs(append(slices.Clone(res.Plan.Args), res.Extra...))
	if err != nil || len(a.Tasks) != 7 || a.Parallel != 4 || a.Pace != "fast" {
		t.Errorf("drive reads %+v, %v", a, err)
	}

	profiled, err := Plan(context.Background(), in, planArgs(t, "GH-30:opus", "GH-14:opus", "--facts",
		writeSheet(t, `{"GH-30": {"lines": 10, "areas": ["x"]}, "GH-14": {"lines": 10}}`)))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := drivectl.ParseDriveArgs(profiled.Plan.Args); err != nil {
		t.Errorf("drive refuses %v: %v", profiled.Plan.Args, err)
	}

	if !slices.Contains(res.Notes, "left out: GH-21 is a draft; name it to plan it") || len(res.Notes) != 1 {
		t.Errorf("notes %v", res.Notes)
	}

	byID := map[string]string{}
	for _, task := range res.Tasks {
		byID[task.ID] = task.Flow + " " + strings.Join(task.Areas, ",") + " " + task.Kind
	}

	if byID["GH-14"] != "default ledger " || byID["GH-16"] != "prose docs " || byID["GH-19"] != "default  spike" {
		t.Errorf("facts %v", byID)
	}
}

func TestPlanWithoutASheet(t *testing.T) {
	t.Parallel()

	res, err := Plan(context.Background(), planFixture(t), planArgs(t, "GH-30", "GH-14"))
	if err != nil {
		t.Fatal(err)
	}

	if res.Plan.Bundles != 0 || res.Plan.Saving() != "" {
		t.Errorf("no sheet sized nothing, yet %+v", res.Plan)
	}

	if bug := res.Tasks[0]; bug.Kind != "bug" || bug.Base != "GH-14" {
		t.Errorf("GH-30 = %+v", bug)
	}

	if !slices.Equal(res.Plan.Args, []string{"GH-14,GH-30"}) {
		t.Errorf("args %v, want GH-30 stacked on GH-14", res.Plan.Args)
	}
}

// the printed command is read back by the start's own parser, so the order
// the proposal sets is the order a start runs; buildsOn reads one id or a list
func TestPlanCommandParsesBackToItsOrder(t *testing.T) {
	t.Parallel()

	in := planFixture(t)
	in.Joins = true
	info := in.Tracker.Info
	in.Tracker.Info = func(ctx context.Context, r ref.Ref) (tasksource.TaskInfo, error) {
		if r.ID == "GH-15" || r.ID == "GH-16" {
			return tasksource.TaskInfo{Body: r.ID}, nil
		}

		return info(ctx, r)
	}
	sheet := writeSheet(t, `{
		"GH-15": {"alone": true, "files": ["x.go"]},
		"GH-16": {"alone": true, "buildsOn": "GH-15"},
		"GH-30": {"alone": true, "files": ["x.go"], "buildsOn": ["GH-16"]}
	}`)

	res, err := Plan(context.Background(), in, planArgs(t, "GH-15", "GH-16", "GH-30", "--facts", sheet))
	if err != nil {
		t.Fatal(err)
	}

	got, err := drivectl.ParseDriveArgs(res.Plan.Args)
	if err != nil {
		t.Fatalf("start refuses %v: %v", res.Plan.Args, err)
	}

	bases := map[string][]string{}
	for i, id := range got.Tasks {
		bases[id] = got.Bases[i]
	}

	// GH-30's body names GH-14, open outside the list, so the start would
	// refuse a join on it: GH-14 is kept and GH-16 named as dropped.
	// GH-15 is reached through GH-16, so the shared file adds nothing.
	want := map[string][]string{"GH-15": nil, "GH-16": {"GH-15"}, "GH-30": {"GH-14"}}
	for id, w := range want {
		if !slices.Equal(bases[id], w) {
			t.Errorf("%s: bases %v, want %v (args %v)", id, bases[id], w, res.Plan.Args)
		}
	}

	var out strings.Builder
	if err := res.Plan.Write(&out, nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "GH-30 not after GH-16") ||
		!strings.Contains(out.String(), "GH-14 is open outside the list") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestPlanRefusesAnUnknownSheetField(t *testing.T) {
	t.Parallel()

	sheet := writeSheet(t, `{"GH-14": {"line": 20}}`)
	if _, err := Plan(context.Background(), planFixture(t), planArgs(t, "GH-14", "--facts", sheet)); err == nil {
		t.Error("a misspelt field read as not sized")
	}
}

// with no sheet, saved small plans in one area are enough for the hint; a
// typed bundle alone never is
func TestPlanHintWithoutASheet(t *testing.T) {
	t.Parallel()

	in := planFixture(t)

	for _, id := range []string{"GH-11", "GH-12"} {
		plan := "# " + id + "\n\nRoles: x\nAreas: ui\nSize: small - one place\n\n## Stage 1\n"
		if err := os.WriteFile(in.Storage.Plan(id), []byte(plan), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	res, err := Plan(context.Background(), in, planArgs(t, "GH-10", "--hint"))
	if err != nil {
		t.Fatal(err)
	}

	const saves = "bundling 2 tasks into 1 bundle would save about 1 CI wait: f10 start "
	if hint := res.Plan.Hint(); !strings.HasPrefix(hint, saves) {
		t.Errorf("hint %q", hint)
	}

	typed, err := Plan(context.Background(), in, planArgs(t, "GH-30+GH-14", "--hint"))
	if err != nil {
		t.Fatal(err)
	}

	if hint := typed.Plan.Hint(); hint != "" {
		t.Errorf("a typed bundle hinted %q", hint)
	}
}
