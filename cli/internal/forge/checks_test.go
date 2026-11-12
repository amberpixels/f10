package forge

import (
	"testing"
)

const ghChecks = "gh api graphql -f query=" + HeadChecksQuery + " -F owner={owner} -F name={repo} -F number=9"

func rollup(contexts string) string {
	return `{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"h3ad",` +
		`"statusCheckRollup":{"contexts":{"nodes":[` + contexts + `]}}}}]}}}}}`
}

const (
	lintFailed = `{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE",` +
		`"detailsUrl":"https://ci/lint","isRequired":false}`
	testRunning = `{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS","conclusion":"",` +
		`"detailsUrl":"https://ci/test","isRequired":true}`
	buildPassed = `{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"SUCCESS",` +
		`"isRequired":true}`
	docsSkipped = `{"__typename":"CheckRun","name":"docs","status":"COMPLETED","conclusion":"SKIPPED","isRequired":false}`
	deployError = `{"__typename":"StatusContext","context":"deploy","state":"ERROR","targetUrl":"https://ci/deploy",` +
		`"isRequired":false}`
	statusPending = `{"__typename":"StatusContext","context":"ext","state":"EXPECTED","isRequired":false}`
)

// The required checks count when the target branch declares any, else all
// of them: a red check outside a declared required set merges.
func TestHeadChecksOnGitHub(t *testing.T) {
	for _, c := range []struct {
		name, contexts, state string
		required              bool
		failed, running       string
	}{
		{"none", ``, "", false, "", ""},
		{"green", buildPassed + "," + docsSkipped, Green, true, "", ""},
		{"required only", buildPassed + "," + lintFailed, Green, true, "", ""},
		{"all when none required", lintFailed + "," + docsSkipped, Red, false, "lint", ""},
		{"red beats pending", lintFailed + "," + statusPending, Red, false, "lint", "ext"},
		{"required pending", buildPassed + "," + testRunning + "," + lintFailed, Pending, true, "", "test"},
		{"status context", deployError, Red, false, "deploy", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			script(t, map[string]string{ghChecks: rollup(c.contexts)})

			got, err := Host{Name: "gh"}.HeadChecks(t.Context(), PR{Number: 9, HeadSHA: "old"})
			if err != nil {
				t.Fatal(err)
			}

			if got.State != c.state || got.Required != c.required || got.SHA != "h3ad" ||
				Names(got.Failed) != c.failed || Names(got.Running) != c.running {
				t.Errorf("HeadChecks = %+v, want %s required=%v failed %q running %q",
					got, c.state, c.required, c.failed, c.running)
			}
		})
	}
}

func TestHeadChecksKeepsTheLogURL(t *testing.T) {
	script(t, map[string]string{ghChecks: rollup(lintFailed + "," + deployError)})

	got, err := Host{Name: "gh"}.HeadChecks(t.Context(), PR{Number: 9})
	if err != nil || len(got.Failed) != 2 || got.Failed[0].URL != "https://ci/lint" ||
		got.Failed[1].URL != "https://ci/deploy" {
		t.Errorf("HeadChecks = %+v, %v; want each failed check with its url", got, err)
	}
}

// A commit with no rollup has no checks: the PR's own head stands.
func TestHeadChecksWithNoRollup(t *testing.T) {
	script(t, map[string]string{
		ghChecks: `{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"h3ad","statusCheckRollup":null}}]}}}}}`,
	})

	got, err := Host{Name: "gh"}.HeadChecks(t.Context(), PR{Number: 9})
	if err != nil || got.State != "" || got.SHA != "h3ad" {
		t.Errorf("HeadChecks = %+v, %v; want no checks on h3ad", got, err)
	}
}

// GitLab's head pipeline decides; a job allowed to fail is never named.
func TestHeadChecksOnGitLab(t *testing.T) {
	for _, c := range []struct {
		name, status, state, failed, running string
	}{
		{"green", "success", Green, "", ""},
		{"failed", "failed", Red, "lint", ""},
		{"running", "running", Pending, "", "test"},
		{"skipped", "skipped", Green, "", ""},
		{"manual", "manual", Green, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			script(t, map[string]string{
				"glab mr view GH-1 -F json": `{"sha":"h3ad","head_pipeline":{"id":5,"sha":"h3ad","status":"` + c.status + `"}}`,
				"glab api --paginate projects/:fullpath/pipelines/5/jobs?per_page=100": `[
				  {"name":"lint","status":"failed","web_url":"https://gl/lint"},
				  {"name":"flaky","status":"failed","allow_failure":true},
				  {"name":"test","status":"` + map[bool]string{true: "running", false: "success"}[c.status == "running"] + `"},
				  {"name":"deploy","status":"manual"}]`,
			})

			got, err := Host{Name: "glab"}.HeadChecks(t.Context(), PR{Number: 4, Head: "GH-1"})
			if err != nil {
				t.Fatal(err)
			}

			if got.State != c.state || got.SHA != "h3ad" || Names(got.Running) != c.running ||
				(c.state == Red && Names(got.Failed) != c.failed) {
				t.Errorf("HeadChecks = %+v, want %s failed %q running %q", got, c.state, c.failed, c.running)
			}
		})
	}
}

func TestHeadChecksWithNoPipeline(t *testing.T) {
	script(t, map[string]string{"glab mr view GH-1 -F json": `{"sha":"h3ad","head_pipeline":null}`})

	got, err := Host{Name: "glab"}.HeadChecks(t.Context(), PR{Number: 4, Head: "GH-1"})
	if err != nil || got.State != "" || got.SHA != "h3ad" {
		t.Errorf("HeadChecks = %+v, %v; want no checks", got, err)
	}
}
