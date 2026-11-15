package asks

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// An answered ask's row carries the ask's labels, so a stream narrowed to a
// project can match the answer as it matches the raise.
func TestAnsweredRowCarriesTheLabels(t *testing.T) {
	dir := t.TempDir()

	a, _, err := Raise(
		dir, Ask{Task: "FT-7", Root: "/src/l/.f10", Main: "/src/l", Text: "1. Go? [yes | no]"}, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Claim(dir, a.Key(), ByAsks, "yes", false, time.Now()); err != nil {
		t.Fatal(err)
	}

	tail := ledger.OpenTail(ledger.Path())
	t.Cleanup(func() { _ = tail.Close() })

	page, err := tail.After(t.Context(), "", ledger.Narrow{Task: "FT-7", Kinds: []string{ledger.AskAnswered}})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("answered rows = %+v, %v; want one", page.Rows, err)
	}

	p, err := ledger.Decode[*ledger.AskAnsweredPayload](page.Rows[0].Event)
	if err != nil || p.GetLabels()["main"] != "/src/l" || p.GetLabels()["task"] != "FT-7" {
		t.Errorf("labels = %v, %v; want the ask's", p.GetLabels(), err)
	}
}
