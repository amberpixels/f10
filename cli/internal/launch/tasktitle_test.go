package launch_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/testkit"
)

// --all lists open and closed issues and changes only the bare titles, so
// a second run over the result changes nothing.
func TestTitleChangesAll(t *testing.T) {
	f := testkit.NewFakes(t)

	list := "gh issue list --state all --json number,title --limit 100000"
	f.Script(list, `[{"number":1,"title":"FT-1: Done"},{"number":2,"title":"Bare"},{"number":3,"title":"ft-3 Lower"}]`)

	changes, err := testkit.TitledHost().TitleChanges(t.Context(), "")
	if err != nil || len(changes) != 2 {
		t.Fatalf("titleChanges = %+v, %v", changes, err)
	}

	if changes[0].To != "FT-2: Bare" || changes[1].To != "FT-3: Lower" {
		t.Errorf("changes = %+v", changes)
	}

	f.Script(
		list,
		`[{"number":1,"title":"FT-1: Done"},{"number":2,"title":"FT-2: Bare"},{"number":3,"title":"FT-3: Lower"}]`,
	)

	if again, err := testkit.TitledHost().TitleChanges(t.Context(), ""); err != nil || len(again) != 0 {
		t.Errorf("second run = %+v, %v", again, err)
	}
}
