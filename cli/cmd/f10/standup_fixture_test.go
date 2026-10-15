package main

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// Upkeep classification checked on real histories: each fixture under
// testdata/standup is every git and host call `f10 standup` made in a real
// checkout, with its answer, and the class each item of the report must
// get. Replaying a fixture runs the real collectors and Group over it.
//
// Record a new one, then check every class in its expect map by hand:
//
//	F10_STANDUP_RECORD='<checkout>|<since, RFC 3339>|<name>|<id prefix>' \
//	  go test ./cmd/f10 -run TestRecordStandupFixture
//
// Record only from public amberpixels repositories.

type standupFixture struct {
	Since  time.Time              `json:"since"`
	Prefix string                 `json:"prefix"`
	Host   string                 `json:"host"`
	Calls  map[string]fixtureCall `json:"calls"`
	Expect map[string]string      `json:"expect"` // item to "work" or "upkeep <reason>"
}

// fixtureCall is one recorded answer, as shell.Result carries it.
type fixtureCall struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr,omitempty"`
	Code   int    `json:"code,omitempty"`
}

func TestStandupFixtures(t *testing.T) {
	paths, err := filepath.Glob("testdata/standup/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}

	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			var fx standupFixture
			if err := json.Unmarshal(data, &fx); err != nil {
				t.Fatal(err)
			}

			f := newFakes(t)
			for key, res := range fx.Calls {
				f.answers[key] = []shell.Result{shell.Result(res)}
			}

			got := classes(t, fx, ".")
			for _, item := range slices.Sorted(maps.Keys(got)) {
				if want, ok := fx.Expect[item]; !ok {
					t.Errorf("%s = %q, missing from expect", item, got[item])
				} else if got[item] != want {
					t.Errorf("%s = %q, want %q", item, got[item], want)
				}
			}

			for item := range fx.Expect {
				if _, ok := got[item]; !ok {
					t.Errorf("%s expected, not in the report", item)
				}
			}
		})
	}
}

// TestRecordStandupFixture writes a fixture from a real checkout, with
// expect filled from what the code says today: a draft to check by hand.
func TestRecordStandupFixture(t *testing.T) {
	spec := os.Getenv("F10_STANDUP_RECORD")
	if spec == "" {
		t.Skip("F10_STANDUP_RECORD is not set")
	}

	parts := strings.Split(spec, "|")
	if len(parts) != 4 {
		t.Fatalf("F10_STANDUP_RECORD = %q, want checkout|since|name|prefix", spec)
	}

	since, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		t.Fatal(err)
	}

	fx := standupFixture{Since: since, Prefix: parts[3], Host: "gh", Calls: map[string]fixtureCall{}}

	prevExec, prevCapture := gitx.Exec, shell.Capture
	t.Cleanup(func() { gitx.Exec, shell.Capture = prevExec, prevCapture })

	gitx.Exec = func(ctx context.Context, dir string, args ...string) (string, error) {
		out, err := prevExec(ctx, dir, args...)

		res := fixtureCall{Stdout: out}
		if err != nil {
			res = fixtureCall{Code: 1, Stderr: err.Error()}
		}

		fx.Calls["git "+strings.Join(args, " ")] = res

		return out, err
	}

	shell.Capture = func(ctx context.Context, dir, name string, args ...string) (shell.Result, error) {
		res, err := prevCapture(ctx, dir, name, args...)
		fx.Calls[filepath.Base(name)+" "+strings.Join(args, " ")] = fixtureCall(res)

		return res, err
	}

	fx.Expect = classes(t, fx, parts[0])

	data, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join("testdata", "standup", parts[2]+".json"),
		append(data, '\n'),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
}

// classes runs the standup over dir and names the class of every item in
// the report: a commit by its sha, a force-push by "push <after>", a task
// by "task <id>".
func classes(t *testing.T, fx standupFixture, dir string) map[string]string {
	t.Helper()

	in, err := collectStandup(t.Context(), standupSource{
		dir: dir, main: dir, prefix: fx.Prefix, host: host{name: fx.Host, dir: dir},
	}, fx.Since)
	if err != nil {
		t.Fatal(err)
	}

	r := standup.Group(in)
	out := map[string]string{}

	class := func(c, reason string) string { return strings.TrimSpace(c + " " + reason) }

	commits := func(cs []standup.Commit) {
		for _, c := range cs {
			out[c.SHA] = class(c.Class, c.Upkeep)
		}
	}

	prs := func(ps []standup.PR) {
		for _, p := range ps {
			commits(p.Commits)

			for _, fp := range p.ForcePushes {
				out["push "+fp.After] = class(fp.Class, fp.Upkeep)
			}
		}
	}

	for _, task := range r.Tasks {
		out["task "+task.ID] = task.Class
		prs(task.PRs)
		commits(task.Commits)
	}

	prs(r.Other.PRs)
	commits(r.Other.Commits)

	return out
}
