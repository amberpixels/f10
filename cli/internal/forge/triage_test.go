package forge

import (
	"slices"
	"strings"
	"testing"
)

// A job cancelled because another failed is never the one to read first,
// and a check's Actions run is what a rerun names.
func TestFirstSkipsACancelledFollower(t *testing.T) {
	cancelled := `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"CANCELLED",` +
		`"detailsUrl":"https://github.com/o/r/actions/runs/77/job/1","isRequired":false}`
	lint := `{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE",` +
		`"detailsUrl":"https://github.com/o/r/actions/runs/77/job/2","isRequired":false}`
	script(t, map[string]string{ghChecks: rollup(cancelled + "," + lint)})

	got, err := Host{Name: "gh"}.HeadChecks(t.Context(), PR{Number: 9})
	if err != nil {
		t.Fatal(err)
	}

	first, ok := got.First()
	if !ok || first.Name != "lint" || first.Run != "77" || !got.Failed[0].Cancelled {
		t.Errorf("First = %+v, %v from %+v; want lint in run 77, test marked cancelled", first, ok, got.Failed)
	}

	cancelledOnly := HeadChecks{Failed: []Check{{Name: "a", Cancelled: true}}}
	if only, ok := cancelledOnly.First(); !ok || only.Name != "a" {
		t.Errorf("First with only a cancelled job = %+v, %v; want it", only, ok)
	}
}

func TestDefaultJobOnGitHub(t *testing.T) {
	call := "gh api graphql -f query=" + DefaultJobQuery + " -F owner={owner} -F name={repo} -f check=lint"
	history := func(runs ...string) string {
		nodes := make([]string, len(runs))
		for i, r := range runs {
			nodes[i] = `{"checkSuites":{"nodes":[{"checkRuns":{"nodes":[` + r + `]}}]}}`
		}

		return `{"data":{"repository":{"defaultBranchRef":{"target":{"history":{"nodes":[` +
			strings.Join(nodes, ",") + `]}}}}}}`
	}

	for _, c := range []struct {
		name, payload, want string
	}{
		{"green", history(`{"status":"COMPLETED","conclusion":"SUCCESS"}`), Green},
		{"red behind a running tip", history(`{"status":"IN_PROGRESS"}`, `{"status":"COMPLETED","conclusion":"FAILURE"}`), Red},
		{"never ran", history(``, ``), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			script(t, map[string]string{call: c.payload})

			got, err := Host{Name: "gh"}.DefaultJob(t.Context(), "lint")
			if err != nil || got != c.want {
				t.Errorf("DefaultJob = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestDefaultJobOnGitLab(t *testing.T) {
	script(t, map[string]string{
		"glab repo view -F json": `{"default_branch":"main"}`,
		"glab api projects/:fullpath/pipelines?ref=main&scope=finished&per_page=5": `[{"id":8},{"id":7}]`,
		"glab api projects/:fullpath/pipelines/8/jobs?per_page=100":                `[{"name":"test","status":"success"}]`,
		"glab api projects/:fullpath/pipelines/7/jobs?per_page=100":                `[{"name":"lint","status":"failed"}]`,
	})

	got, err := Host{Name: "glab"}.DefaultJob(t.Context(), "lint")
	if err != nil || got != Red {
		t.Errorf("DefaultJob = %q, %v; want red from the older pipeline", got, err)
	}
}

// One rerun per run, however many of its jobs failed; a check from another
// app has no run and is left alone.
func TestRerun(t *testing.T) {
	script(t, map[string]string{"gh run rerun 77 --failed": ""})

	runs, err := Host{
		Name: "gh",
	}.Rerun(
		t.Context(),
		[]Check{{Name: "a", Run: "77"}, {Name: "b", Run: "77"}, {Name: "ext"}},
	)
	if err != nil || !slices.Equal(runs, []string{"77"}) {
		t.Errorf("Rerun = %v, %v; want run 77 once", runs, err)
	}

	script(t, map[string]string{"glab api -X POST projects/:fullpath/pipelines/5/retry": "{}"})

	if runs, err := (Host{Name: "glab"}).Rerun(
		t.Context(),
		[]Check{{Name: "lint", Run: "5"}},
	); err != nil ||
		len(runs) != 1 {
		t.Errorf("Rerun on GitLab = %v, %v; want pipeline 5 retried", runs, err)
	}
}

func TestFailedLog(t *testing.T) {
	script(t, map[string]string{
		"gh run view --job 12 --log-failed": "lint\tstep\terror: 504",
		"gh run view 77 --log-failed":       "from the run",
	})

	if got, err := (Host{Name: "gh"}).FailedLog(
		t.Context(),
		Check{
			Name: "lint",
			URL:  "https://github.com/o/r/actions/runs/77/job/12",
			Run:  "77",
		},
	); err != nil || got != "lint\tstep\terror: 504" {
		t.Errorf("FailedLog by job = %q, %v", got, err)
	}

	if got, err := (Host{Name: "gh"}).FailedLog(
		t.Context(),
		Check{Name: "lint", Run: "77"},
	); err != nil ||
		got != "from the run" {
		t.Errorf("FailedLog by run = %q, %v", got, err)
	}

	if _, err := (Host{Name: "gh"}).FailedLog(
		t.Context(),
		Check{Name: "ext", URL: "https://ci.example/x"},
	); err == nil {
		t.Error("a check with no job or run must say its log cannot be read")
	}

	script(t, map[string]string{"glab ci trace 31": "trace"})

	if got, err := (Host{Name: "glab"}).FailedLog(
		t.Context(),
		Check{URL: "https://gl/g/p/-/jobs/31"},
	); err != nil ||
		got != "trace" {
		t.Errorf("FailedLog on GitLab = %q, %v", got, err)
	}
}
