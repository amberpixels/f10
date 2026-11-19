package statusview

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A ledger finish gives `status <id>` its done row only while the tracker
// does not state the issue open: after a multi-PR task's first PR, start
// opens the next PR, so status must not call the task done.
func TestFinishedRowLetsAnOpenIssueOutrankTheLedger(t *testing.T) {
	const item = "gh issue view 1 --json number,title,state,labels,createdAt,url,body,assignees"

	for _, tc := range []struct {
		name  string
		state string
		want  bool
	}{
		{name: "open", state: "open"},
		{name: "closed", state: "closed", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := testkit.NewFakes(t)
			t.Setenv("F10_STATE_DIR", t.TempDir())
			testkit.ScriptNoBranch(f, "FT-1")
			f.Absent = append(f.Absent, "gh api graphql ")
			f.Script(item, `{"number":1,"title":"one","state":"`+tc.state+`","url":"https://x/1","body":""}`)

			rd, err := NewStatusReader(testkit.ChildTarget(t, "gh"), time.Now())
			if err != nil {
				t.Fatal(err)
			}

			finish := event(
				time.Now(),
				"FT-1",
				ledger.TaskFinished,
				&ledger.TaskFinishedPayload{Args: []string{"FT-1"}},
			)
			rd.scoped = &scopedLedger{
				covered:  map[string]bool{"FT-1": true},
				events:   []ledger.Event{finish},
				finishes: true,
			}

			if got := rd.finishedRow(t.Context(), ref.Ref{ID: "FT-1", Number: "1"}) != nil; got != tc.want {
				t.Errorf("a done row = %v, want %v", got, tc.want)
			}
		})
	}
}
