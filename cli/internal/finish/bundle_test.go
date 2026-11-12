package finish

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A bundle's merge waits for the riders' answer, before anything changes.
func TestFinishAsksWhichRidersClose(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.Task = testkit.Bundle12

	_, err := fx.run(t)

	var re RidersError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "riders GH-2") {
		t.Fatalf("err = %v, want the riders asked", err)
	}

	if fx.f.Called("gh pr merge") || fx.f.Called("gh pr edit") {
		t.Errorf("something changed before the answer: %v", fx.f.Calls)
	}
}

// A rider left open loses its closing keyword before the merge; one the PR
// delivers keeps it, and the report names it closed.
func TestFinishKeepsARiderOpen(t *testing.T) {
	for name, tc := range map[string]struct {
		close  []string
		edited bool
		want   string
	}{
		"left open": {close: []string{}, edited: true, want: "left open: GH-2"},
		"closed":    {close: []string{"GH-2"}, want: "GH-2"},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.openPR()
			fx.in.Task, fx.in.Close, fx.in.HostCloses = testkit.Bundle12, tc.close, true
			fx.f.Script("gh pr view GH-1 --json body", `{"body":"Closes #1\nCloses #2"}`)
			fx.f.Absent = append(fx.f.Absent, "gh pr edit GH-1 --body-file ")

			out, err := fx.run(t)
			if err != nil {
				t.Fatal(err)
			}

			if fx.f.Called("gh pr edit") != tc.edited {
				t.Errorf("body edited = %v, want %v: %v", !tc.edited, tc.edited, fx.f.Calls)
			}

			if edit := testkit.IndexAfter(fx.f.Calls, "gh pr edit", -1); tc.edited &&
				testkit.IndexAfter(fx.f.Calls, testkit.GhMerge, -1) < edit {
				t.Errorf("the body was edited after the merge: %v", fx.f.Calls)
			}

			if !strings.Contains(out, tc.want) {
				t.Errorf("report missing %q:\n%s", tc.want, out)
			}
		})
	}
}

// After a merge that closed every member, a rider left open is reopened.
func TestFinishReopensAKeptRiderAfterTheMerge(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, mergedWithBody("Closes #1\nCloses #2"))
	fx.f.Script(testkit.LsRem, "")
	fx.f.Script("gh issue reopen 2", "")
	fx.in.Task, fx.in.Close, fx.in.HostCloses = testkit.Bundle12, []string{}, true

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called("gh issue reopen 2") || !strings.Contains(out, "reopened GH-2") {
		t.Errorf("GH-2 was not reopened: %v\n%s", fx.f.Calls, out)
	}
}

func jsonBody(body string) string { return `"body":"` + strings.ReplaceAll(body, "\n", `\n`) + `"` }

func openWithBody(body string) string {
	return `{"state":"OPEN","url":"https://gh/pr/9","number":9,"baseRefName":"main",` + jsonBody(body) + `}`
}

func mergedWithBody(body string) string {
	return `{"state":"MERGED","url":"https://gh/pr/9","number":9,"baseRefName":"main",` +
		`"mergeCommit":{"oid":"m3rge"},` + jsonBody(body) + `}`
}

// The drive and the watcher close the riders the PR body closes and leave
// those under Part of open, with no body edit before the merge and no
// reopen after it.
func TestFinishFollowsTheBody(t *testing.T) {
	for name, tc := range map[string]struct {
		body   string
		merged bool
		want   string
	}{
		"every rider closes":           {body: "Closes #1\nCloses #2", want: "closed:"},
		"a part-of rider stays open":   {body: "Closes #1\nPart of #2", want: "left open: GH-2"},
		"after the merge, closed":      {body: "Closes #1\nCloses #2", merged: true, want: "closed:       GH-2"},
		"after the merge, part of":     {body: "Closes #1\nPart of #2", merged: true, want: "left open: GH-2"},
		"a rider the body never names": {body: "Closes #1", want: "left open: GH-2"},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.in.Task, fx.in.HostCloses, fx.in.FollowBody = testkit.Bundle12, true, true

			if tc.merged {
				fx.in.Flags.MergedOnly = true
				fx.f.Script(testkit.GhView, mergedWithBody(tc.body))
				fx.f.Script(testkit.LsRem, "")
			} else {
				fx.f.Script(testkit.GhView, openWithBody(tc.body), mergedWithBody(tc.body))
				fx.f.Script(testkit.GhRepo, testkit.AllowAll)
				fx.f.Script(testkit.GhMerge, "")
				fx.f.Script("gh pr view GH-1 --json body", `{`+jsonBody(tc.body)+`}`)
			}

			out, err := fx.run(t)
			if err != nil {
				t.Fatal(err)
			}

			if fx.f.Called("gh pr edit") || fx.f.Called("gh issue reopen") {
				t.Errorf("the body's choice was overridden: %v", fx.f.Calls)
			}

			if !strings.Contains(out, tc.want) {
				t.Errorf("report missing %q:\n%s", tc.want, out)
			}
		})
	}
}

// After a merge that left a rider under Part of, --close closes it, and the
// report says the finish did it.
func TestFinishClosesAChosenRiderTheMergeLeftOpen(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, mergedWithBody("Closes #1\nPart of #2"))
	fx.f.Script(testkit.LsRem, "")
	fx.f.Script("gh issue close 2", "")
	fx.in.Task, fx.in.Close, fx.in.HostCloses = testkit.Bundle12, []string{"GH-2"}, true

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called("gh issue close 2") || !strings.Contains(out, "closed GH-2, which the merge had left open") {
		t.Errorf("GH-2 was not closed: %v\n%s", fx.f.Calls, out)
	}
}

// A tracker that is not the host never reads the body, so the question
// offers no body choice and the drive leaves every rider open.
func TestFinishOnAnotherTrackerIgnoresTheBody(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, openWithBody("Closes #2"))
	fx.f.Script(testkit.GhRepo, testkit.AllowAll)
	fx.in.Task = testkit.Bundle12

	_, err := fx.run(t)
	if err == nil || strings.Contains(err.Error(), "PR body") {
		t.Errorf("err = %v, want the question without a body choice", err)
	}
}

// A manual finish still asks, and the question names the body's choice.
func TestFinishAsksWithTheBodysChoice(t *testing.T) {
	for body, want := range map[string]string{
		"Closes #1\nCloses #2":  "the PR body closes GH-2: --close GH-2 follows it",
		"Closes #1\nPart of #2": "the PR body closes none of them: --close none follows it",
	} {
		fx := newFinishFixture(t)
		fx.f.Script(testkit.GhView, openWithBody(body))
		fx.f.Script(testkit.GhRepo, testkit.AllowAll)
		fx.in.Task, fx.in.HostCloses = testkit.Bundle12, true

		_, err := fx.run(t)

		var re RidersError
		if !errors.As(err, &re) || !strings.Contains(err.Error(), want) {
			t.Errorf("body %q: err = %v, want %q", body, err, want)
		}
	}
}

func TestBodyCloses(t *testing.T) {
	riders := []ref.Ref{{ID: "GH-2", Number: "2"}, {ID: "GH-3", Number: "3"}, {ID: "GH-4", Number: "4"}}
	body := "Closes #1\nfixes https://github.com/a/b/issues/2\nPart of #3\nCloses #44"

	if got := bodyCloses(body, riders); !slices.Equal(got, []string{"GH-2"}) {
		t.Errorf("BodyCloses = %v, want [GH-2]", got)
	}
}

func TestKeepOpen(t *testing.T) {
	body := "Closes #12\nfixes: #12\nResolved https://github.com/a/b/issues/12\nCloses #123\nSee #12\nCloses #3"
	want := "Part of #12\nPart of #12\nPart of https://github.com/a/b/issues/12\nCloses #123\nSee #12\nCloses #3"

	if got := KeepOpen(body, "12"); got != want {
		t.Errorf("keepOpen =\n%s\nwant\n%s", got, want)
	}
}

func TestCloseChoice(t *testing.T) {
	task := ref.Ref{ID: "GH-1", Bundle: []string{"GH-1", "GH-2", "GH-3"}}
	resolve := func(tok string) (ref.Ref, error) {
		return ref.Ref{ID: "GH-" + strings.TrimPrefix(strings.ToUpper(tok), "GH-")}, nil
	}

	for value, want := range map[string]string{"all": "GH-2 GH-3", "none": "", "3": "GH-3", "gh-2, 3": "GH-2 GH-3"} {
		got, err := CloseChoice(task, value, resolve)
		if err != nil || strings.Join(got, " ") != want || got == nil {
			t.Errorf("closeChoice(%q) = %v, %v; want [%s]", value, got, err, want)
		}
	}

	if _, err := CloseChoice(task, "GH-1", resolve); err == nil {
		t.Error("the lead was taken as a rider")
	}
}

// A tracker that is not the host closes nothing on merge, a driver's or a
// declared one without a driver: no closed row, no body edit, and the notes
// name what to close there.
func TestFinishWithATrackerDriverClosesNothing(t *testing.T) {
	for _, withDriver := range []bool{true, false} {
		fx := newFinishFixture(t)
		fx.openPR()
		fx.in.Task, fx.in.Close = testkit.Bundle12, []string{"GH-2"}

		if withDriver {
			fx.in.Driver = testkit.FakeDriver(t)
			fx.f.Fail("driver branch 1", shell.Result{Code: 3})
		}

		out, err := fx.run(t)
		if err != nil {
			t.Fatal(err)
		}

		if strings.Contains(out, "closed:") || fx.f.Called("gh pr view GH-1 --json body") {
			t.Errorf("driver %v: a non-host tracker reported or edited a close:\n%s\n%v", withDriver, out, fx.f.Calls)
		}

		if !strings.Contains(out, "close GH-2 there") {
			t.Errorf("driver %v: report does not name the rider to close by hand:\n%s", withDriver, out)
		}
	}
}

// A rerun after a failed merge closes a rider an earlier run kept open.
func TestCloseAgain(t *testing.T) {
	body := KeepOpen("Closes #1\nCloses #2", "2")
	if got := closeAgain(body, "2"); got != "Closes #1\nCloses #2" {
		t.Errorf("closeAgain = %q, want the keyword back", got)
	}

	if got := closeAgain("Part of #22", "2"); got != "Part of #22" {
		t.Errorf("closeAgain touched another issue: %q", got)
	}
}
