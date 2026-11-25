package drivectl_test

import (
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivectl"
)

func TestParseDriveArgs(t *testing.T) {
	cases := []struct {
		args    []string
		tasks   string
		span    bool
		flow    string
		answers string
		every   time.Duration
		model   string
		models  string // each task's :model, | between, checked when set
		wantErr string
	}{
		{
			args:  []string{"GH-1", "GH-2", "--flow", "Direct"},
			tasks: "GH-1 GH-2",
			flow:  "direct",
			every: drivectl.DefaultEvery,
		},
		{args: []string{"GH-1", "--local"}, tasks: "GH-1", flow: "local", every: drivectl.DefaultEvery},
		{args: []string{"GH-1", "--local", "--flow=local"}, tasks: "GH-1", flow: "local", every: drivectl.DefaultEvery},
		{args: []string{"GH-1", "--", "GH-4"}, tasks: "GH-1 GH-4", span: true, every: drivectl.DefaultEvery},
		{
			args:  []string{"--every", "5s", "GH-1", "--answer=1. yes"},
			tasks: "GH-1", answers: "1. yes", every: 5 * time.Second,
		},
		{
			args:  []string{"GH-1", "GH-2", "--answer", "GH-1=1. yes", "--answer=GH-2=1. no"},
			tasks: "GH-1 GH-2", answers: "GH-1=1. yes|GH-2=1. no", every: drivectl.DefaultEvery,
		},
		{
			args:  []string{"GH-1:sonnet", "GH-2", "--model", "opus"},
			tasks: "GH-1 GH-2", model: "opus", models: "sonnet|", every: drivectl.DefaultEvery,
		},
		{
			args:  []string{"--model=fable", "GH-1-retry:opus"},
			tasks: "GH-1-retry", model: "fable", models: "opus", every: drivectl.DefaultEvery,
		},
		{
			args:   []string{"https://host:8080/o/r/issues/1", "https://github.com/o/r/issues/2:opus"},
			tasks:  "https://host:8080/o/r/issues/1 https://github.com/o/r/issues/2",
			models: "|opus", every: drivectl.DefaultEvery,
		},
		{
			args:   []string{"GH-1", "--", "GH-4"},
			tasks:  "GH-1 GH-4",
			span:   true,
			models: "|",
			every:  drivectl.DefaultEvery,
		},
		{
			args: []string{
				"GH-1:claude:opus:high", "--agent", "sonnet:low", "https://github.com/o/r/issues/2:opus:max",
			},
			tasks: "GH-1 https://github.com/o/r/issues/2", model: "sonnet:low",
			models: "claude:opus:high|opus:max", every: drivectl.DefaultEvery,
		},
		{args: []string{"GH-1:"}, wantErr: "names no profile"},
		{args: []string{"GH-1:opus", "--", "GH-4"}, wantErr: "run-wide --agent"},
		{args: []string{"GH-1", "--agent", "opus", "--model", "fable"}, wantErr: "give one"},
		{args: []string{"GH-1", "--model", "high"}, wantErr: "names an effort"},
		{args: []string{"GH-1", "--model", "opus:high"}, wantErr: "a model alone"},
		{args: []string{"GH-1:opus:huge"}, wantErr: "not an effort"},
		{args: []string{"GH-1", "--agent", "opencode:opus:high"}, wantErr: "not an agent kind"},
		{args: []string{"GH-1", "--model", "opus", "--model", "fable"}, wantErr: "given twice"},
		{args: []string{"GH-1", "--model"}, wantErr: "needs a value"},
		{args: []string{"GH-1", "--local", "--flow", "direct"}, wantErr: "already names the local flow"},
		{args: []string{"GH-1", "--flow"}, wantErr: "needs a value"},
		{args: []string{"GH-1", "GH-2", "--", "GH-4"}, wantErr: "a range is one id"},
		{args: []string{"--model", "opus"}, wantErr: "needs the tasks"},
		{args: []string{"GH-1", "--every", "soon"}, wantErr: "positive duration"},
		{args: []string{"GH-1", "--answer"}, wantErr: "needs a value"},
		{args: []string{"GH-1", "--force"}, wantErr: "unknown flag"},
		{args: []string{"GH-1", "--parallel", "0"}, wantErr: "a positive number"},
		{args: []string{"GH-1", "--parallel=many"}, wantErr: "a positive number"},
	}

	for _, c := range cases {
		a, err := drivectl.ParseDriveArgs(c.args)

		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%q: err = %v, want %q", c.args, err, c.wantErr)
			}

			continue
		}

		if err != nil {
			t.Errorf("%q: %v", c.args, err)

			continue
		}

		if strings.Join(a.Tasks, " ") != c.tasks || a.Span != c.span || a.Flow != c.flow ||
			strings.Join(a.Answers, "|") != c.answers || a.Every != c.every || a.Agent != c.model ||
			(c.models != "" && strings.Join(a.Profiles, "|") != c.models) {
			t.Errorf("%q = %+v", c.args, a)
		}
	}
}

// plan is a task word to f10 start, and a proposal is asked for by flag:
// --propose, or --hint alone. --json and --facts only shape one.
func TestStartProposesByFlag(t *testing.T) {
	a, err := drivectl.ParseDriveArgs([]string{"plan", "FT-1"})
	if err != nil {
		t.Fatal(err)
	}

	if a.Plan != nil || strings.Join(a.Tasks, " ") != "plan FT-1" {
		t.Errorf("args = %+v, want plan and FT-1 as tasks and no proposal arguments", a)
	}

	for _, line := range [][]string{
		{"FT-1", "FT-2", "--propose"},
		{"--propose", "FT-1", "--json", "--flow", "direct"},
		{"FT-1", "--hint"},
		{"FT-1", "--propose", "--facts=sheet.json"},
	} {
		sa, err := drivectl.ParseStart(line)
		if err != nil || sa.Kind != drivectl.ProposeStart || sa.Drive.Plan == nil {
			t.Errorf("%q = %+v, %v; want a proposal", line, sa, err)
		}
	}

	sa, err := drivectl.ParseStart([]string{"FT-1", "--propose", "--json", "--flow", "direct", "--parallel", "3"})
	if err != nil || !sa.Drive.Plan.JSON || strings.Join(sa.Drive.Plan.Pass, " ") != "--flow direct" {
		t.Errorf("proposal = %+v, %v; want --json set and --flow carried onto the command", sa.Drive.Plan, err)
	}

	for line, want := range map[string]string{
		"FT-1 --json":                    "add --propose",
		"FT-1 --facts f.json":            "add --propose",
		"FT-1 -- FT-4 --propose":         "not a range",
		"FT-1 --propose --answer yes":    "start --propose takes",
		"FT-1 --propose --resume 2":      "start --propose takes",
		"--propose":                      "needs the tasks",
		"FT-1 --propose --facts":         "needs the facts sheet",
		"FT-1 --propose --discuss":       "unknown flag",
		"FT-1 --propose --after FT-2":    "start --propose takes",
		"FT-1 FT-2 --hint --plan":        "start --propose takes",
		"FT-1 --propose --every 5s":      "start --propose takes",
		"FT-1 --propose --foreground":    "start --propose takes",
		"FT-1 --propose --base main":     "start --propose takes",
		"FT-1 --propose --flow":          "needs a value",
		"FT-1 --propose --parallel many": "a positive number",
	} {
		if _, err := drivectl.ParseStart(strings.Fields(line)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", line, err, want)
		}
	}

	// a plain start still parses as one
	if sa, err := drivectl.ParseStart([]string{"FT-1", "FT-2"}); err != nil || sa.Kind != drivectl.FlowStart {
		t.Errorf("a plain start = %+v, %v", sa, err)
	}
}

// f10 resume takes one task id, or none, and the flags --resume took.
func TestParseResume(t *testing.T) {
	r, err := drivectl.ParseResume(
		[]string{"FT-1", "--answer", "1. yes", "--every=5s", "--parallel", "2", "--foreground"},
	)
	if err != nil || r.Token != "FT-1" || !r.Fore ||
		strings.Join(r.Flags, "|") != "--answer|1. yes|--every=5s|--parallel|2|--foreground" {
		t.Errorf("resume = %+v, %v", r, err)
	}

	if r, err := drivectl.ParseResume(nil); err != nil || r.Token != "" || len(r.Flags) != 0 {
		t.Errorf("a bare resume = %+v, %v", r, err)
	}

	for line, want := range map[string]string{
		"FT-1 FT-2":         "one task id",
		"FT-1 --flow fast":  "it takes only",
		"FT-1 --resume 3":   "it takes only",
		"FT-1 --answer":     "needs a value",
		"FT-1 --propose":    "it takes only",
		"--parallel":        "needs a value",
		"FT-1 --agent opus": "it takes only",
	} {
		if _, err := drivectl.ParseResume(strings.Fields(line)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", line, err, want)
		}
	}

	// the flags resume passes on parse as --resume's own did
	if a, err := drivectl.ParseDriveArgs(append([]string{"--resume", "3"}, r.Flags...)); err != nil || a.Resume != 3 ||
		a.Parallel != 2 || !a.Fore {
		t.Errorf("the resumed line = %+v, %v", a, err)
	}

	if n, ok := drivectl.OldNumber(" 3 "); !ok || n != 3 {
		t.Errorf("OldNumber = %d, %v", n, ok)
	}

	for _, tok := range []string{"FT-3", "0", "-1", ""} {
		if _, ok := drivectl.OldNumber(tok); ok {
			t.Errorf("OldNumber took %q", tok)
		}
	}
}
