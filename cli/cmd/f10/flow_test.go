package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// f10 flow and f10 config read the same flows, and the human list names
// what selects each.
func TestFlowAndConfigAgree(t *testing.T) {
	testkit.IsolateEnv(t)

	repo := testkit.MkRepo(t)
	path := filepath.Join(repo, ".f10", "instructions", "project.md")

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("---\nflows:\n  default: plan judge implement pr\n"+
		"  local: plan implement commit\n  direct: implement push\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var flows runplan.FlowView
	if err := json.Unmarshal([]byte(runApp(t, "-C", repo, "flow", "--json").must(t)), &flows); err != nil {
		t.Fatal(err)
	}

	var config configcmd.View
	if err := json.Unmarshal([]byte(runApp(t, "-C", repo, "config", "--json").must(t)), &config); err != nil {
		t.Fatal(err)
	}

	if len(flows.Flows) != 3 || flows.Flows[0].Name != "default" {
		t.Fatalf("flows = %+v", flows)
	}

	for _, f := range flows.Flows {
		if strings.Join(config.Flows[f.Name].Strings(), " ") != strings.Join(f.Entries, " ") {
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

// f10 flow prints a labelled entry as written, the list ship runs and
// passes to its badge, with or without a pace; JSON keeps entries bare.
func TestFlowPrintsEntryLabels(t *testing.T) {
	testkit.IsolateEnv(t)

	repo := testkit.MkRepo(t)
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

	var v runplan.FlowView
	if err := json.Unmarshal([]byte(runApp(t, "-C", repo, "flow", "--json").must(t)), &v); err != nil {
		t.Fatal(err)
	}

	f := v.Flows[0]
	if got := strings.Join(f.Entries, " "); got != "plan implement pr review:local pr" ||
		len(f.Labels) != 5 || f.Labels[4] != "pr-polish" {
		t.Errorf("json flow = %+v", f)
	}
}
