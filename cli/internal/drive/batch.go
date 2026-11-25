package drive

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
)

// Every command that names a batch by a task resolves it in this file, so
// stop, resume and daemon logs agree on which batch a task id means.

// namesOf are the ids a token may be held under: as typed, then the id it
// resolves to and its bundle's members.
func namesOf(ctx context.Context, t *target.Target, token string) []string {
	names := []string{token}

	if r, err := t.Reference(ctx, token); err == nil {
		names = append(names, r.ID, r.Head().ID)
		names = append(names, r.Members()...)
	}

	return slices.Compact(names)
}

// BatchOf is the newest live batch holding the task a token names.
func BatchOf(ctx context.Context, t *target.Target, token string) (drivestore.Live, bool) {
	root := t.Layout.StorageRoot.Path()

	for _, id := range namesOf(ctx, t, token) {
		if l, ok := drivestore.Holding(root, id); ok {
			return l, true
		}
	}

	return drivestore.Live{}, false
}

// RecordOf is the newest record holding the task a token names, whatever
// its standing: a finished batch still has a log, and a stopped one a reason.
func RecordOf(ctx context.Context, t *target.Target, token string) (*drivestore.Record, bool) {
	return recordHolding(t.Layout.StorageRoot.Path(), namesOf(ctx, t, token))
}

func recordHolding(root string, names []string) (*drivestore.Record, bool) {
	recs, err := drivestore.List(root)
	if err != nil {
		return nil, false
	}

	for _, rec := range recs {
		if slices.ContainsFunc(names, rec.Holds) {
			return rec, true
		}
	}

	return nil, false
}

// A NoBatchError is a task id no batch answers to, for the command asking.
type NoBatchError struct{ Token string }

func (e NoBatchError) Error() string {
	return fmt.Sprintf("no batch holds %s: f10 status --batches lists every batch", e.Token)
}

// LogBatch is the number of the batch whose log a task names: the newest
// record holding it, live or not, because a finished batch still has a log
// and an older live batch is not the one the task last ran in.
func LogBatch(ctx context.Context, t *target.Target, token string) (int, error) {
	return logBatch(t.Layout.StorageRoot.Path(), token, namesOf(ctx, t, token))
}

func logBatch(root, token string, names []string) (int, error) {
	if rec, ok := recordHolding(root, names); ok {
		return rec.Number, nil
	}

	return 0, NoBatchError{Token: token}
}

// StopBatch is the number of the batch f10 stop ends for a task: the
// running one holding its lease, else the newest live one whose record
// holds it. The lease is asked first, because a batch the daemon has just
// taken runs before its record is readable.
func StopBatch(ctx context.Context, t *target.Target, token string) (int, error) {
	token = strings.TrimSpace(token)
	root := t.Layout.StorageRoot.Path()

	if r, err := t.Reference(ctx, token); err == nil {
		if holder, held := drivestore.Probe(root, r.Head().ID); held && holder.Drive > 0 {
			return holder.Drive, nil
		}
	}

	if l, ok := BatchOf(ctx, t, token); ok {
		return l.Number, nil
	}

	return 0, NoBatchError{Token: token}
}

// ErrNoneStopped is a bare resume in a project with no stopped batch.
var ErrNoneStopped = errors.New("no stopped batch in this project: f10 status --batches lists every batch")

// A SeveralStoppedError is a bare resume that cannot pick: each batch is
// named by its lead, which resumes it.
type SeveralStoppedError struct{ Stopped []drivestore.Live }

func (e SeveralStoppedError) Error() string {
	return strconv.Itoa(len(e.Stopped)) + " batches are stopped: name one by a task id"
}

// A NotResumableError is a task no live batch holds. Ended is set when f10
// stop ended the newest batch holding it, which no resume picks up.
type NotResumableError struct {
	Token string
	Ended *drivestore.Record
}

func (e NotResumableError) Error() string {
	if e.Ended != nil {
		return fmt.Sprintf("batch %d was stopped for good: run its f10 start line again to start a new one",
			e.Ended.Number)
	}

	return fmt.Sprintf("no batch to resume holds %s: f10 status --batches lists every batch", e.Token)
}

// Resumable is the batch a resume names: the live one holding the task, or
// with no token the project's one stopped batch.
func Resumable(ctx context.Context, t *target.Target, token string) (*drivestore.Record, error) {
	if token == "" {
		stopped := drivestore.Stopped(t.Layout.StorageRoot.Path())

		switch len(stopped) {
		case 0:
			return nil, ErrNoneStopped
		case 1:
			return stopped[0].Record, nil
		}

		return nil, SeveralStoppedError{Stopped: stopped}
	}

	if l, ok := BatchOf(ctx, t, token); ok {
		return l.Record, nil
	}

	if rec, ok := RecordOf(ctx, t, token); ok && rec.Cancelled != nil {
		return nil, NotResumableError{Token: token, Ended: rec}
	}

	return nil, NotResumableError{Token: token}
}

// CommandID is the task id that names rec to stop, resume and daemon logs:
// its first task that resolves back to it, else its lead. Every hint prints
// it, so a hint never names a task a newer batch has taken. It reads every
// record, so it is called where a line is printed, never in the poll loop.
func CommandID(root string, rec *drivestore.Record) string {
	return cmp.Or(rec.Name(root), rec.Lead())
}

// leadOf is CommandID for batch n, "" when the record is not open or not
// readable.
func leadOf(root string, n int) string {
	rec, err := drivestore.Read(root, n)
	if err != nil {
		return ""
	}

	return CommandID(root, rec)
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
