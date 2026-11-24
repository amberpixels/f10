package drivestore

import (
	"regexp"
	"slices"
	"strings"
)

// Live is a drive neither cancelled nor done, and whether its driver runs:
// holds the drive's lease. A stopped drive still lists its tasks.
type Live struct {
	*Record

	Running bool
}

// Active is every live drive under root, newest first; an unreadable
// drives dir lists none.
func Active(root string) []Live {
	recs, err := List(root)
	if err != nil {
		return nil
	}

	var out []Live

	for _, r := range recs {
		if l, ok := live(root, r); ok {
			out = append(out, l)
		}
	}

	return out
}

// ActiveOne is drive n when it is live, false when it is done, cancelled
// or has no record.
func ActiveOne(root string, n int) (Live, bool) {
	r, err := Read(root, n)
	if err != nil {
		return Live{}, false
	}

	return live(root, r)
}

func live(root string, r *Record) (Live, bool) {
	if r.Cancelled != nil || r.Done() {
		return Live{}, false
	}

	_, running := Probe(root, DriveKey(r.Number))

	return Live{Record: r, Running: running}, true
}

// Holding is the newest live record holding task id: as a task, as any
// member of a bundle, or with a -suffix. A batch is addressed by it, so no
// command needs the record's number. The newest record holding the task
// decides: once f10 stop ended it, an older live one is not picked up in its
// place, because the user was told the stop is for good.
func Holding(root, id string) (Live, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Live{}, false
	}

	recs, err := List(root)
	if err != nil {
		return Live{}, false
	}

	for _, r := range recs {
		if !r.Holds(id) {
			continue
		}

		if r.Cancelled != nil {
			return Live{}, false
		}

		if l, ok := live(root, r); ok {
			return l, true
		}
	}

	return Live{}, false
}

// wholeID is a task id, as against a tracker prefix cut short: it ends in a
// number that starts the id or follows a separator, so FT and R3 are none.
var wholeID = regexp.MustCompile(`(^|[-#])\d+$`)

// holds reports typed, a task as a record keeps it, naming id. A typed
// -suffix is matched only under a whole id, so a prefix alone names nothing.
func holds(typed, id string) bool {
	whole := wholeID.MatchString(id)

	for m := range strings.SplitSeq(typed, "+") {
		if strings.EqualFold(m, id) {
			return true
		}

		if whole && len(m) > len(id) && strings.EqualFold(m[:len(id)+1], id+"-") {
			return true
		}
	}

	return false
}

// Stopped is every live record no process runs and a task id still names,
// newest first: what a bare resume picks from. A record whose every task a
// newer batch holds is left out, because no task id reaches it.
func Stopped(root string) []Live {
	return slices.DeleteFunc(Active(root), func(l Live) bool { return l.Running || l.Name(root) == "" })
}

// Name is the task id that names this record to a command: its first task
// that Holding resolves to it, "" when a newer batch holds them all.
func (r *Record) Name(root string) string {
	for _, t := range r.Tasks {
		for m := range strings.SplitSeq(t.ID, "+") {
			if l, ok := Holding(root, m); ok && l.Number == r.Number {
				return m
			}
		}
	}

	return ""
}

// Lead is the record's first task as typed, "" for none.
func (r *Record) Lead() string {
	if len(r.Tasks) == 0 {
		return ""
	}

	lead, _, _ := strings.Cut(r.Tasks[0].ID, "+")

	return lead
}

// Holds reports the record holding task id: as a task, as any member of a
// bundle, or with a -suffix.
func (r *Record) Holds(id string) bool {
	return slices.ContainsFunc(r.Tasks, func(t Task) bool { return holds(t.ID, id) })
}
