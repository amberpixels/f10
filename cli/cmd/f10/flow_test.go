package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIn runs f10 with -C dir and returns what it printed.
func runIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer = &out

	if err := app.Run(context.Background(), append([]string{"f10", "-C", dir}, args...)); err != nil {
		t.Fatalf("f10 %v: %v", args, err)
	}

	return out.String()
}

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
		"  local: plan implement commit\n  direct: implement push\ndrive_finish: on\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var flows flowView
	if err := json.Unmarshal([]byte(runIn(t, repo, "flow", "--json")), &flows); err != nil {
		t.Fatal(err)
	}

	var config view
	if err := json.Unmarshal([]byte(runIn(t, repo, "config", "--json")), &config); err != nil {
		t.Fatal(err)
	}

	if len(flows.Flows) != 3 || flows.Flows[0].Name != "default" || !flows.DriveFinish || !config.DriveFinish {
		t.Fatalf("flows = %+v", flows)
	}

	for _, f := range flows.Flows {
		if strings.Join(config.Flows[f.Name], " ") != strings.Join(f.Entries, " ") {
			t.Errorf("%s: flow says %v, config says %v", f.Name, f.Entries, config.Flows[f.Name])
		}
	}

	human := runIn(t, repo, "flow")
	for _, want := range []string{
		"(f10 start, f10 drive)", "(--local, --flow local)", "(--flow direct)",
		"drive_finish  on: f10 drive ends each task with finish",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("f10 flow misses %q:\n%s", want, human)
		}
	}

	if cfg := runIn(t, repo, "config"); strings.Contains(cfg, "Ship pipeline") || strings.Contains(cfg, "Drive chain") {
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
		f.script("git config --get branch.GH-1."+cfgFlow, tc.recorded)

		in := &driveInput{main: "/code/repo", resolve: resolve}

		fl, note, err := in.keepFlow(t.Context(), "GH-1")
		if err != nil || fl.recorded() != tc.want || !strings.Contains(note, tc.note) ||
			f.called("git config branch.GH-1."+cfgFlow+" default") != tc.writes {
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
