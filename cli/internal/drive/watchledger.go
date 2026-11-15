package drive

import (
	"context"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// LedgerReports records each report a pass tells as a watch_reported row,
// so API clients see what the watch log shows. The project is read once,
// on the first report; the pull and host reports are the project's own, so
// their rows name no task.
func LedgerReports(ctx context.Context, main string) func(watchReport) {
	var place *ledger.Place

	return func(r watchReport) {
		e, err := ledger.NewEvent(ledger.WatchReported, &ledger.WatchReportedPayload{
			Report: r.Kind, Text: r.Text, Next: r.Next, Ask: r.Ask,
		})
		if err != nil {
			return
		}

		if place == nil {
			p := ledger.Where(ctx, main)
			place = &p
		}

		e.Project, e.ProjectLocal, e.Source, e.At = place.Project, place.ProjectLocal, ledger.Observed, r.At
		if r.Task != watchPullKey && r.Task != watchHostKey {
			e.Task = r.Task
		}

		ledger.Record(e)
	}
}
