package statusview

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// StatusView is where a status prints and how: the flags it honours.
type StatusView struct {
	Out, Warn   io.Writer
	JSON, Plain bool
}

// statusPrinted is what a status path printed: anything, and a question
// a driven agent waits on, which the hook leaves to the model.
type statusPrinted struct {
	Printed, asks bool
}

// statusReader binds every read one status call makes, so each source is
// read once whichever paths the call takes. The ledger, the gate source and
// the drives are read on first use: the session-run and listing paths need none.
type statusReader struct {
	t       *target.Target
	now     time.Time
	main    string // where the branch config and the ledger's project live
	live    []*state.Run
	host    *hostops.Host // nil without a host CLI
	resolve func(token string) string

	eventsOnce sync.Once
	events     []ledger.Event
	gatesOnce  sync.Once
	gates      gateSource
	drivesOnce sync.Once
	active     []drivestore.Live // the drive records, read once for both the rows and the gates
}

func NewStatusReader(t *target.Target, now time.Time) (*statusReader, error) {
	live, err := state.List(state.Dir(), now, state.TTL())
	if err != nil {
		return nil, err
	}

	return &statusReader{
		t:    t,
		now:  now,
		main: cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		live: live,
		host: hostops.OptionalHostOf(t),
	}, nil
}

// Task prints what `status <token>` prints, reading the task's checkout
// first so a task with a run never waits on the host. With no run, a parent
// lists its children and a finished task prints its block (its done row
// under --json); a guessed token prints nothing, so the bare output stands.
func (rd *statusReader) Task(ctx context.Context, v StatusView, token string, guessed bool) (statusPrinted, error) {
	id, suffix := ref.SplitSuffix(token)

	r, err := rd.t.Reference(ctx, id)
	if err != nil {
		return statusPrinted{}, err
	}

	in, err := taskStatusFor(ctx, rd.t, r, suffix)
	if errors.Is(err, errNoRunToRead) && !guessed {
		rows, cerr := rd.parentRows(ctx, r)

		switch {
		case cerr == nil && len(rows) > 0:
			return rd.printRows(v, rows, r.ID)
		case cerr != nil && !errors.Is(cerr, tasksource.ErrNoParentLink):
			ui.Warnf(v.Warn, "reading the subtasks: %v", cerr)
		}

		if row := rd.finishedRow(ctx, r.Head()); row != nil {
			if v.JSON {
				return rd.printRows(v, []TaskRow{*row}, r.ID)
			}

			return statusPrinted{Printed: true}, rd.writeFinished(ctx, v, *row)
		}
	}

	if err != nil {
		return statusPrinted{}, err
	}

	runs, others := taskRuns(InRoot(rd.live, in.Root), in.ID)
	ship := rd.shipment(ctx, in, runs)

	if len(runs) == 0 {
		fb := taskFallback(in, others, ship)

		if v.JSON {
			return statusPrinted{Printed: true}, ui.JSON(v.Out, fb)
		}

		fb.Render(v.Out, ui.Width(v.Out), render.LinksFor(v.Out, v.Plain).On)

		return statusPrinted{Printed: true}, nil
	}

	gates := make([][]gate, len(runs))
	for i, r := range runs {
		gates[i] = rd.gatesOf(ctx, in.ID, in.Branch, in.Root, r)
	}

	asks := slices.ContainsFunc(runs, func(r *state.Run) bool { return r.Ask != "" })

	return statusPrinted{Printed: true, asks: asks},
		writeRuns(v, runs, gates, &ship, true, in.agent, rd.now)
}

// ledger is the project's events. A ledger that is missing or will not
// open has nothing to say: status only reads.
func (rd *statusReader) ledger(ctx context.Context) []ledger.Event {
	rd.eventsOnce.Do(func() { rd.events = readLedger(ctx, rd.main) })

	return rd.events
}

func (rd *statusReader) gateSource(ctx context.Context) gateSource {
	rd.gatesOnce.Do(func() {
		rd.gates = gateSource{
			events: rd.ledger(ctx),
			Main:   rd.main,
			Eff:    rd.t.Effective,
			review: rd.t.Effective.Review,
			store:  &lore.Store{Root: rd.t.LoreRoot()},
			host:   rd.host,
			Shared: rd.t.Layout.StorageRoot.Path(),
		}
		rd.gates.driveFlows = driveFlows(rd.drives())
	})

	return rd.gates
}

// drives is the drive records neither cancelled nor done.
func (rd *statusReader) drives() []drivestore.Live {
	rd.drivesOnce.Do(func() { rd.active = drivestore.Active(rd.t.Layout.StorageRoot.Path()) })

	return rd.active
}

// idOf turns a reference as typed into an id, read once per call.
func (rd *statusReader) idOf(ctx context.Context) func(string) string {
	if rd.resolve == nil {
		rd.resolve = rd.t.IDOf(ctx)
	}

	return rd.resolve
}

// gatesOf is a task's gates, its findings and verdicts read from its
// checkout's storage root, else the shared one.
func (rd *statusReader) gatesOf(ctx context.Context, id, branch, root string, run *state.Run) []gate {
	gs := rd.gateSource(ctx)

	return gs.gatesOf(ctx, id, branch, root, gs.StorageFor(ctx, root), run)
}

// rows is the row reads bound to this call.
func (rd *statusReader) rows(ctx context.Context) rowSource {
	return rowSource{
		Live:    rd.live,
		Events:  rd.ledger(ctx),
		main:    rd.main,
		storage: rd.t.Layout.StorageRoot,
		prefix:  rd.t.Effective.IDPrefix,
		Checkout: func(ctx context.Context, id, suffix string) (string, string) {
			r, err := rd.t.Reference(ctx, id)
			if err != nil {
				return "", ""
			}

			name, root, err := stack.TaskCheckout(ctx, rd.main, rd.t.Driver(), r, suffix)
			if err != nil {
				return "", ""
			}

			return name, root
		},
		Resolve: rd.idOf(ctx),
		host:    rd.host,
		Driven:  drivenTasks(rd.drives()),
		gates:   rd.gatesOf,
	}
}

// shipment is the task's facts block, finished before anything prints:
// a run that lost its url gets it back from the task refs, since every
// skill call reseeds its run, and the block takes the run's; with no run,
// the host is asked for the PR's state the no-run block shows.
//
//nolint:lostfield // a read: readShipment fills the fields
func (rd *statusReader) shipment(ctx context.Context, in taskStatusInput, runs []*state.Run) Shipment {
	known := ledger.LookupRefs(ctx, rd.t.RefProject(ctx), []string{in.ID})[in.ID]
	s := readShipment(ctx, in, rd.ledger(ctx), known)

	if len(runs) == 0 {
		s.AskPR(ctx, in)

		return s
	}

	for _, r := range runs {
		r.URL = cmp.Or(r.URL, known.URL)
	}

	if s.PR != "" {
		s.AskPR(ctx, in)
	}

	s.URL = cmp.Or(runs[0].URL, s.URL)

	return s
}

// launches prints the tree of what session sid started or drove that is
// still live; with nothing live it prints nothing, so the bare answer stays
// what it was.
func (rd *statusReader) launches(ctx context.Context, v StatusView, sid string) (statusPrinted, error) {
	kids := launchKids(ctx, v.Warn, rd.t, sid)
	if len(kids) == 0 {
		return statusPrinted{}, nil
	}

	rows := dropRows(rd.rows(ctx).RowsFrom(ctx, kids), func(r TaskRow) bool { return r.Stage == StageDone })
	if len(rows) == 0 {
		return statusPrinted{}, nil
	}

	return rd.printRows(v, rows, LaunchesHead)
}

func (rd *statusReader) printRows(v StatusView, rows []TaskRow, head string) (statusPrinted, error) {
	asks := slices.ContainsFunc(rows, func(r TaskRow) bool { return r.Stage == stageAsking })

	return statusPrinted{Printed: true, asks: asks}, writeTaskRows(v, rows, head)
}

// parentRows is `status <id>` for a parent: its children, closed ones
// included, as rows. ErrNoParentLink, or no rows, is not a parent.
func (rd *statusReader) parentRows(ctx context.Context, r ref.Ref) ([]TaskRow, error) {
	kids, err := stack.TrackerFor(rd.t).AllChildren(ctx, r)
	if err != nil || len(kids) == 0 {
		return nil, err
	}

	return rd.rows(ctx).RowsFrom(ctx, kids), nil
}

// finishedRow is `status <id>` for a task with no branch left: one done
// row when the ledger, the tracker or the plan archive says it finished.
func (rd *statusReader) finishedRow(ctx context.Context, r ref.Ref) *TaskRow {
	tr := stack.TrackerFor(rd.t)
	info, _ := tr.Info(ctx, r) // an unreachable tracker leaves the ledger and the archive

	lt := lookaround.FoldLedger(rd.ledger(ctx), r.ID, rd.idOf(ctx))

	if !stack.FinishedTask(info, tr.Storage, r.ID, false) &&
		!lookaround.LedgerFinishStands(lt.Finished, tr.Storage.PlanLive(r.ID)) {
		return nil
	}

	k := stack.ChildRow{ID: r.ID, Finished: true}
	if title, err := tasksource.TasksFor(rd.t).Title(ctx, r); err == nil {
		k.Title = title
		stack.RememberRefsFor(ctx, rd.t, ledger.TaskRef{ID: r.ID, Title: tasktitle.Clean(k.Title, r.ID)})
	}

	rows := rd.rows(ctx).RowsFrom(ctx, []stack.ChildRow{k})

	return &rows[0]
}

// writeFinished prints a finished task's block, read from the archive.
func (rd *statusReader) writeFinished(ctx context.Context, v StatusView, row TaskRow) error {
	known := ledger.LookupRefs(ctx, rd.t.RefProject(ctx), []string{row.ID})[row.ID]
	ship := finishedShipment(rd.t.Layout.StorageRoot, row, known)
	rows := finishedFacts(ship, row.Gates, render.LinksFor(v.Out, v.Plain).On)

	return render.WriteFacts(v.Out, rows, render.LabelWidth(rows), ui.Width(v.Out))
}

// finishedFacts is a finished task's block rows: the shipment, the PR's
// state, then the gates as the step rows a live block prints under ship.
func finishedFacts(ship Shipment, gates []gate, link bool) []render.Fact {
	rows := append(ship.Facts(link), ship.stateFacts()...)

	return append(rows, gateFacts(gates)...)
}

// hookStatusDeadline is how long the hook's status for a task may read,
// below the 5 second timeout hooks.json gives the hook.
const hookStatusDeadline = 3 * time.Second

// StatusText is what `f10 status --plain` prints for session in dir, bare
// or for token, and whether the model must take the turn: the output holds
// a question to relay, or token names no task, so it is the skill's to read.
func StatusText(ctx context.Context, dir, session, token string) (string, bool) {
	var out bytes.Buffer

	v := StatusView{Out: &out, Warn: &out, Plain: true}

	var (
		asks bool
		err  error
	)

	if token == "" {
		asks, err = BareStatus(ctx, v, dir, false, session, false)
	} else {
		// a task with no run asks the tracker and the host; past the deadline
		// the model takes the turn, inside the hook's own timeout
		ctx, cancel := context.WithTimeout(ctx, hookStatusDeadline)
		defer cancel()

		rd, ok := taskReader(ctx, dir, token)
		if !ok {
			return "", true
		}

		var p statusPrinted

		p, err = rd.Task(ctx, v, token, false)
		if ctx.Err() != nil {
			return "", true
		}

		asks = p.asks
	}

	if err != nil {
		out.WriteString(ui.Prefixed(err.Error()) + "\n")
	}

	return strings.TrimSpace(out.String()), asks
}

// taskReader is the reader for a status of token in dir; ok is false when
// token names no task there.
func taskReader(ctx context.Context, dir, token string) (*statusReader, bool) {
	t, err := target.At(ctx, dir, false, "status")
	if err != nil {
		return nil, false
	}

	id, _ := ref.SplitSuffix(token)
	if _, err := t.Reference(ctx, id); err != nil {
		return nil, false
	}

	rd, err := NewStatusReader(t, time.Now())

	return rd, err == nil
}
