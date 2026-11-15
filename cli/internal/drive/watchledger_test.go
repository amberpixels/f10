package drive

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// A told report becomes a watch_reported row in the project; the host's
// report is the project's own, so its row names no task.
func TestLedgerReportsRecordsTheReport(t *testing.T) {
	main := t.TempDir()
	record := LedgerReports(t.Context(), main)

	record(watchReport{Task: "FT-1", Kind: watchStopped, Text: "ship done", Next: "f10 finish FT-1", At: watchNow})
	record(watchReport{Task: watchHostKey, Kind: watchUnanswered, Text: "gh: HTTP 401", At: watchNow})

	tail := ledger.OpenTail(ledger.Path())
	t.Cleanup(func() { _ = tail.Close() })

	page, err := tail.After(t.Context(), "", ledger.Narrow{Kinds: []string{ledger.WatchReported}})
	if err != nil {
		t.Fatal(err)
	}

	var rows []ledger.Placed

	for _, r := range page.Rows {
		if r.Project == main {
			rows = append(rows, r)
		}
	}

	if len(rows) != 2 || rows[0].Task != "FT-1" || rows[1].Task != "" || !rows[0].At.Equal(watchNow) {
		t.Fatalf("rows = %+v, want FT-1's stop and the project's host report", rows)
	}

	p, err := ledger.Decode[*ledger.WatchReportedPayload](rows[0].Event)
	if err != nil || p.GetReport() != watchStopped || p.GetNext() != "f10 finish FT-1" {
		t.Errorf("payload = %v, %v", p, err)
	}
}
