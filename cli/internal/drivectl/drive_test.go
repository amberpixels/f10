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
