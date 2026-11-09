package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/state"
)

// f10 flow and f10 config read the same flows, and the human list names
// what selects each.
func TestFlowAndConfigAgree(t *testing.T) {
	isolateEnv(t)

	repo := mkRepo(t)
	path := filepath.Join(repo, ".f10", "instructions", "project.md")

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("---\nflows:\n  default: plan judge implement pr\n"+
		"  local: plan implement commit\n  direct: implement push\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var flows flowView
	if err := json.Unmarshal([]byte(runApp(t, "-C", repo, "flow", "--json").must(t)), &flows); err != nil {
		t.Fatal(err)
	}

	var config view
	if err := json.Unmarshal([]byte(runApp(t, "-C", repo, "config", "--json").must(t)), &config); err != nil {
		t.Fatal(err)
	}

	if len(flows.Flows) != 3 || flows.Flows[0].Name != "default" {
		t.Fatalf("flows = %+v", flows)
	}

	for _, f := range flows.Flows {
		if strings.Join(config.Flows[f.Name], " ") != strings.Join(f.Entries, " ") {
			t.Errorf("%s: flow says %v, config says %v", f.Name, f.Entries, config.Flows[f.Name])
		}
	}

	human := runApp(t, "-C", repo, "flow").must(t)
	for _, want := range []string{
		"(f10 start, f10 drive)", "(--local, --flow local)", "(--flow direct)",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("f10 flow misses %q:\n%s", want, human)
		}
	}

	if cfg := runApp(t, "-C", repo, "config").must(t); strings.Contains(cfg, "Ship pipeline") ||
		strings.Contains(cfg, "Drive chain") {
		t.Errorf("config still lists the old sections:\n%s", cfg)
	}
}

// A rerun of drive keeps the flow a branch records; a branch with none,
// or one recording a flow no longer declared, takes the run's.
func TestDriveKeepsTheRecordedFlow(t *testing.T) {
	declared := map[string]shipFlow{"": {}, "local": {name: "local"}}
	resolve := func(name string) (shipFlow, error) {
		if fl, ok := declared[name]; ok {
			return fl, nil
		}

		return shipFlow{}, os.ErrNotExist
	}

	for _, tc := range []struct {
		recorded, want, note string
		writes               bool
	}{
		{recorded: "", want: "default", writes: true},
		{recorded: "default", want: "default"},
		{recorded: "local", want: "local", note: "kept the local flow"},
		{recorded: "gone", want: "default", note: "no longer declares", writes: true},
	} {
		f := newFakes(t)
		f.meta.Seed("GH-1", branchmeta.Flow, tc.recorded)

		in := &driveInput{main: "/code/repo", resolve: resolve}

		fl, note, err := in.keepFlow(t.Context(), "GH-1")
		if err != nil || fl.recorded() != tc.want || !strings.Contains(note, tc.note) ||
			f.called("set branch.GH-1."+string(branchmeta.Flow)+" default") != tc.writes {
			t.Errorf("recorded %q: flow %q, note %q, err %v, calls %v", tc.recorded, fl.recorded(), note, err, f.calls)
		}
	}
}

func TestCompletingFlow(t *testing.T) {
	for args, want := range map[string]bool{
		"f10 start --flow --generate-shell-completion":       true,
		"f10 drive GH-1 --flow --generate-shell-completion":  true,
		"f10 start --local --generate-shell-completion":      false,
		"f10 start --flow local --generate-shell-completion": false,
	} {
		if got := completingFlow(strings.Fields(args)); got != want {
			t.Errorf("%q = %v, want %v", args, got, want)
		}
	}
}

// A task that keeps a flow from its branch runs that flow's chain: a
// planless run still plans a task whose recorded flow leads with plan.
func TestChainForAKeptFlow(t *testing.T) {
	direct := shipFlow{name: "direct", planless: true}
	in := &driveInput{flow: direct, chain: driveChain(direct)}

	if got := strings.Join(in.chainFor(direct), " "); got != "ship finish" {
		t.Errorf("the run's own flow = %q", got)
	}

	if got := strings.Join(in.chainFor(shipFlow{}), " "); got != "plan ship finish" {
		t.Errorf("a kept default flow = %q, want plan first and finish kept", got)
	}
}

// With flow_by_area declared and no flow named, ship picks the flow from
// the task's area: start and drive send ship alone, and record nothing.
func TestFlowByArea(t *testing.T) {
	eff := &facts.Effective{
		Flows:      map[string][]string{"default": {"plan", "implement", "pr"}, "direct": {"implement", "push"}},
		FlowByArea: map[string]string{"ui": "direct"},
	}

	fl, err := resolveShipFlow(eff, "")
	if err != nil || !fl.byArea || fl.plans() || fl.recorded() != "" {
		t.Fatalf("no flow named = %+v, %v", fl, err)
	}

	if named, _ := resolveShipFlow(eff, "default"); named.byArea || !named.plans() {
		t.Errorf("a named flow wins over the area map: %+v", named)
	}

	if got := prompt(modeDefault, fl, "GH-1", nil, "", nil, ""); got != "/f10:ship GH-1 --driven" {
		t.Errorf("prompt = %q, want ship alone", got)
	}

	if got := strings.Join(driveChain(fl), " "); got != "ship finish" {
		t.Errorf("drive chain = %q, want ship plans as the area's flow says", got)
	}

	f := newFakes(t)

	in := &driveInput{main: "/code/repo", flow: fl}
	if kept, note, err := in.keepFlow(t.Context(), "GH-1"); err != nil || kept != fl || note != "" ||
		slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "set branch.") }) {
		t.Errorf("keepFlow = %+v, %q, %v; calls %v", kept, note, err, f.calls)
	}
}

// f10 flow prints a labelled entry as written, the list ship runs and
// passes to its badge, with or without a pace; JSON keeps entries bare.
func TestFlowPrintsEntryLabels(t *testing.T) {
	isolateEnv(t)

	repo := mkRepo(t)
	path := filepath.Join(repo, ".f10", "instructions", "project.md")

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("---\nflows:\n  default: plan implement pr review:local pr[pr-polish]\n---\n"),
		0o600); err != nil {
		t.Fatal(err)
	}

	// medium's finish line follows the last entry
	lines := strings.Split(strings.TrimSpace(runApp(t, "-C", repo, "flow", "default").must(t)), "\n")
	if last := lines[len(lines)-2]; last != "pr[pr-polish]" {
		t.Errorf("f10 flow default's last entry is %q, want pr[pr-polish]:\n%s", last, strings.Join(lines, "\n"))
	}

	paced := runApp(t, "-C", repo, "flow", "default", "--pace", "fast").must(t)
	if !strings.Contains(paced, "pr[pr-polish]") {
		t.Errorf("under a pace the label is gone:\n%s", paced)
	}

	all := runApp(t, "-C", repo, "flow").must(t)
	if !strings.Contains(all, "plan implement pr review:local pr[pr-polish]") {
		t.Errorf("f10 flow:\n%s", all)
	}

	var v flowView
	if err := json.Unmarshal([]byte(runApp(t, "-C", repo, "flow", "--json").must(t)), &v); err != nil {
		t.Fatal(err)
	}

	f := v.Flows[0]
	if got := strings.Join(f.Entries, " "); got != "plan implement pr review:local pr" ||
		state.LabelAt(f.Labels, 4) != "pr-polish" {
		t.Errorf("json flow = %+v", f)
	}
}
