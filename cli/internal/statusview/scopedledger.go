package statusview

import (
	"context"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// scopedLedger is the ledger rows one status call has read: every session
// of the tasks it showed, and the project's finish rows, which name their
// task in finish's arguments, so only a read of all of them finds one.
type scopedLedger struct {
	path, project string
	covered       map[string]bool
	events        []ledger.Event
	finishes      bool
}

func newScopedLedger(path, project string) *scopedLedger {
	return &scopedLedger{path: path, project: project, covered: map[string]bool{}}
}

// For is the rows read so far, widened first to the sessions of any id not
// yet covered. Each widening builds a new slice, so a caller still walking
// an earlier one is unaffected.
func (s *scopedLedger) For(ctx context.Context, ids ...string) []ledger.Event {
	var missing []string

	for _, id := range ids {
		key := strings.ToUpper(id)
		if id != "" && !s.covered[key] {
			s.covered[key] = true
			missing = append(missing, id)
		}
	}

	var read []ledger.Event

	if !s.finishes {
		s.finishes = true
		finishes, _ := ledger.ReadEvents(ctx, s.path, ledger.Filter{Project: s.project, Kind: ledger.TaskFinished})
		read = append(read, finishes...)
	}

	if len(missing) > 0 {
		scoped, _ := ledger.ReadScope(ctx, s.path, ledger.TaskScope(s.project, missing...))
		read = append(read, scoped...)
	}

	if len(read) > 0 {
		s.events = merge(s.events, read)
	}

	return s.events
}

// merge is a and b in id order, each row once.
func merge(a, b []ledger.Event) []ledger.Event {
	out := slices.Concat(a, b)
	slices.SortStableFunc(out, func(x, y ledger.Event) int { return strings.Compare(x.ID, y.ID) })

	return slices.CompactFunc(out, func(x, y ledger.Event) bool { return x.ID == y.ID })
}

// bundleLead is the lead of the bundle branch ships when id rides in it,
// else "": a bundle's rows carry its lead's id.
func bundleLead(id, branch string) string {
	i := strings.LastIndexByte(id, '-')
	if i < 0 || branch == "" {
		return ""
	}

	members := ref.Members(id[:i], branch)
	if len(members) < 2 || strings.EqualFold(members[0], id) {
		return ""
	}

	return members[0]
}
