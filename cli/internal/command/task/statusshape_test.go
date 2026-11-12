package task_test

import (
	"bytes"
	"testing"

	"github.com/urfave/cli/v3"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// `task report --children` draws the parent, then its open subtasks nested
// by After:, each with its title and url.
func TestTaskReportChildrenTree(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")

	f.Script("gh api --paginate repos/{owner}/{repo}/issues/71/sub_issues", `[
		{"number":72,"title":"base","state":"open","html_url":"https://x/72","body":"b","labels":[]},
		{"number":73,"title":"later","state":"open","html_url":"https://x/73","body":"x\nAfter: #72\n","labels":[]},
		{"number":74,"title":"done","state":"closed","html_url":"https://x/74","body":"","labels":[]}
	]`)
	testkit.ScriptNoBranch(f, "FT-72", "FT-73")

	var out bytes.Buffer

	cmd := &cli.Command{Writer: &out, ErrWriter: &out}
	r := ref.Ref{ID: "FT-71", Number: "71"}

	if err := taskcmd.WriteParentTree(t.Context(), cmd, tg, "71", r,
		ledger.TaskRef{ID: "FT-71", Title: "Rate limits", URL: "https://x/71"}); err != nil {
		t.Fatal(err)
	}

	want := "FT-71        Rate limits  ↗ https://x/71\n" +
		"└─ FT-72     base         ↗ https://x/72\n" +
		"   └─ FT-73  later        ↗ https://x/73\n"
	if out.String() != want {
		t.Errorf("tree =\n%s\nwant\n%s", out.String(), want)
	}
}
