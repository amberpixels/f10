package lookaround

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
)

// TaskFacts is what one command knows about one task, gathered once. Both
// stage vocabularies, `f10 status`'s and the board's, map it to a stage and
// read nothing else.
type TaskFacts struct {
	Run *state.Run // live in its worktree, else the ledger's last claim
	// Halt is a halt a drive recorded after the run's last word
	Halt         *Halt
	Finished     bool
	FinishedFrom string // FinishedByLedger or FinishedByTracker
	PR           *PR    // the request the stage reads: open, else the newest merged
	PRs          []PR   // every request naming the task, open first
	PRURL        string // a request only the ledger names
	Branch       *Branch
	Worktree     *Worktree
	Plan         bool   // a live plan file
	Lead         string // the bundle's lead, for a rider sharing its branch and run
	Base         string // the base task's id
	BaseFrom     string // FromLocal (the branch), FromTrace (the state block) or FromGuessed (the task body)
	Driven       bool   // a running drive lists it
	Queued       bool   // its drive's cap holds it back
	DrivePhase   string // where its running drive says it stands, a drivestore.Phase name
	Findings     *Findings
	Verdict      string
}

// Where a task's finish was read: a finish run, or the tracker's list,
// which counts a task whose plan is archived as closed.
const (
	FinishedByLedger  = "ledger"
	FinishedByTracker = "tracker"
)

// A Halt is a drive stopping a task: the skill it stopped and why.
type Halt struct {
	Skill, Reason string
	At            time.Time
}

// LedgerTask is what the ledger holds about one task.
type LedgerTask struct {
	Claim    *state.Run
	Finished bool
	PR       string
	Pushed   bool
	Halt     *Halt
}

// FoldLedger folds the task's events, oldest first, into its last claim,
// whether finish ran on it, its request and the halt a drive saw after the
// claim. resolve turns finish's argument as typed (`117`, a url) into an id.
func FoldLedger(events []ledger.Event, id string, resolve func(string) string) LedgerTask {
	var lt LedgerTask

	for _, e := range events {
		p := e.Payload.Val

		switch {
		case e.Kind == ledger.TaskFinished:
			// finish runs from the main checkout, whose session may be on
			// another task: the id it was given decides
			if strings.EqualFold(resolve(e.FinishedTask()), id) {
				lt.Finished = true
			}
		case !e.OnTask(id):
		case strings.HasPrefix(e.Kind, "state_"):
			lt.Claim = ClaimRun(e)
			if lt.Halt != nil && !lt.Halt.At.After(e.At) {
				lt.Halt = nil
			}
		case e.Kind == ledger.PROpened:
			lt.PR = cmp.Or(p.String("url"), lt.PR)
		case e.Kind == ledger.Pushed:
			lt.Pushed = true
		case e.Kind == ledger.TaskHalted:
			lt.Halt = &Halt{Skill: e.Skill(), Reason: e.Reason(), At: e.At}
		}
	}

	return lt
}

// ClaimRun reads a recorded `f10 state` call back as the run it left, minus
// its wait and done: those are read from a live run only.
func ClaimRun(e ledger.Event) *state.Run {
	r := ledger.DecodeClaim(e).Run
	r.Wait, r.Done = "", ""

	return &r
}

// LedgerFinishStands says whether a finish the ledger recorded still holds:
// a live plan means the task was re-planned after it, so it is pending work
// again. Status, the board and the local scan all read a finish by it.
func LedgerFinishStands(finished, planLive bool) bool { return finished && !planLive }

// AddLedger takes what the ledger adds to the record: the claim where no live
// run is, a finish while it stands, the request url, and a halt newer than
// the run.
func (tf *TaskFacts) AddLedger(lt LedgerTask, planLive bool) {
	if tf.Run == nil {
		tf.Run = lt.Claim
	}

	if LedgerFinishStands(lt.Finished, planLive) && !tf.Finished {
		tf.Finished, tf.FinishedFrom = true, FinishedByLedger
	}

	if lt.Halt != nil && (tf.Run == nil || lt.Halt.At.After(tf.Run.Updated)) {
		tf.Halt = lt.Halt
	}

	tf.PRURL = lt.PR
}

// SetPRs keeps the task's requests open first, then merged and closed
// newest first, and reads the first as the one the stage reads.
func (tf *TaskFacts) SetPRs(prs []PR) {
	tf.PRs = prs
	slices.SortStableFunc(tf.PRs, func(a, c PR) int {
		if d := prRank(a) - prRank(c); d != 0 {
			return d
		}

		return cmp.Or(c.MergedAt.Compare(a.MergedAt), c.ClosedAt.Compare(a.ClosedAt))
	})

	tf.PR = nil
	if len(tf.PRs) > 0 {
		tf.PR = &tf.PRs[0]
	}
}

func prRank(p PR) int {
	switch p.Status {
	case forge.Open:
		return 0
	case forge.Merged:
		return 1
	default:
		return 2
	}
}

// TaskRequests is every request naming one of ids, by id, read in one host
// query: the requests of a few tasks, whatever their age. A request counts
// for an id its branch or title names, so a search hit on a longer id does not.
func TaskRequests(ctx context.Context, h *forge.Host, prefix string, ids []string) (map[string][]PR, error) {
	found, err := h.EarlierRequests(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]PR, len(ids))

	for _, id := range ids {
		for _, p := range found[id] {
			pr := NewPR(p)
			if slices.ContainsFunc(membersOf(prefix, pr), func(m string) bool { return strings.EqualFold(m, id) }) {
				out[id] = append(out[id], pr)
			}
		}
	}

	return out, nil
}

// SettleBases visits every key once, each after its base, so a mapper reads
// the base's stage before its dependent's. visit gets the base's index, -1
// for no base, a base not among keys, or the key itself; in a cycle the
// base is passed unsettled.
func SettleBases(keys []string, baseOf func(i int) string, visit func(i, base int)) {
	index := make(map[string]int, len(keys))
	for i, k := range keys {
		index[strings.ToUpper(k)] = i
	}

	done := make([]bool, len(keys))
	onPath := make([]bool, len(keys))

	var settle func(i int)

	settle = func(i int) {
		if done[i] || onPath[i] {
			return
		}

		onPath[i] = true

		b, ok := index[strings.ToUpper(baseOf(i))]
		if !ok || b == i {
			b = -1
		} else {
			settle(b)
		}

		visit(i, b)

		onPath[i], done[i] = false, true
	}

	for i := range keys {
		settle(i)
	}
}
