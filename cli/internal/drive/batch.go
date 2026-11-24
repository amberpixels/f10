package drive

import (
	"context"
	"strconv"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
)

// BatchOf is the newest live batch holding the task a token names: the
// token as typed, then the id it resolves to and its bundle's members.
// Every command that names a batch by a task resolves it here.
func BatchOf(ctx context.Context, t *target.Target, token string) (drivestore.Live, bool) {
	root := t.Layout.StorageRoot.Path()

	if l, ok := drivestore.Holding(root, token); ok {
		return l, true
	}

	r, err := t.Reference(ctx, token)
	if err != nil {
		return drivestore.Live{}, false
	}

	for _, id := range append([]string{r.ID}, r.Members()...) {
		if l, ok := drivestore.Holding(root, id); ok {
			return l, true
		}
	}

	return drivestore.Live{}, false
}

// leadOf is batch n's first task, the id its commands take, "" when the
// record is not open or not readable.
func leadOf(root string, n int) string {
	rec, err := drivestore.Read(root, n)
	if err != nil {
		return ""
	}

	return rec.Lead()
}

// resumeHint is the command that goes on with batch n.
func resumeHint(root string, n int) string {
	if lead := leadOf(root, n); lead != "" {
		return "f10 resume " + lead
	}

	return "f10 resume"
}

// logsHint is the command that follows batch n's log. Before the record is
// readable it names the number, which the command still takes.
func logsHint(root string, n int) string {
	if lead := leadOf(root, n); lead != "" {
		return "f10 daemon logs " + lead + " -f"
	}

	return "f10 daemon logs " + strconv.Itoa(n) + " -f"
}

// startedLogsHint is logsHint for a batch the daemon has just taken, whose
// record may not be open yet: it then names the first task as typed.
func startedLogsHint(root string, n int, a drivectl.DriveArgs) string {
	if leadOf(root, n) == "" && len(a.Tasks) > 0 {
		return "f10 daemon logs " + ref.TypedLead(a.Tasks[0]) + " -f"
	}

	return logsHint(root, n)
}
