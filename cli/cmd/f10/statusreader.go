package main

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

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// statusView is where a status prints and how: the flags it honours.
type statusView struct {
	w, warn     io.Writer
	json, plain bool
}

// statusPrinted is what a status path printed: anything, and a question
// a driven agent waits on, which the hook leaves to the model.
type statusPrinted struct {
	printed, asks bool
}

// statusReader binds every read one status call makes, so each source is
// read once whichever paths the call takes. The ledger, the gate source and
// the drives are read on first use: the session-run and listing paths need none.
type statusReader struct {
	t       *target
	now     time.Time
	main    string // where the branch config and the ledger's project live
	live    []*state.Run
	host    *host // nil without a host CLI
	resolve func(token string) string

	eventsOnce sync.Once
	events     []ledger.Event
	gatesOnce  sync.Once
	gates      gateSource
	drivesOnce sync.Once
	active     []drives.Live // the drive records, read once for both the rows and the gates
}

func newStatusReader(t *target, now time.Time) (*statusReader, error) {
	live, err := state.List(state.Dir(), now, state.TTL())
	if err != nil {
		return nil, err
	}

	return &statusReader{
		t: t, now: now, main: cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot), live: live, host: t.optionalHost(),
	}, nil
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
			main:   rd.main,
			eff:    rd.t.eff,
			review: rd.t.eff.Review,
			store:  &lore.Store{Root: loreRootFor(rd.t)},
			host:   rd.host,
			shared: rd.t.lay.StorageRoot.Path(),
		}
		rd.gates.driveFlows = driveFlows(rd.drives())
	})

	return rd.gates
}

// drives is the drive records neither cancelled nor done.
func (rd *statusReader) drives() []drives.Live {
	rd.drivesOnce.Do(func() { rd.active = drives.Active(rd.t.lay.StorageRoot.Path()) })

	return rd.active
}

// idOf turns a reference as typed into an id, read once per call.
func (rd *statusReader) idOf(ctx context.Context) func(string) string {
	if rd.resolve == nil {
		rd.resolve = rd.t.idOf(ctx)
	}

	return rd.resolve
}

// gatesOf is a task's gates, its findings and verdicts read from its
// checkout's storage root, else the shared one.
func (rd *statusReader) gatesOf(ctx context.Context, id, branch, root string, run *state.Run) []gate {
	gs := rd.gateSource(ctx)

	return gs.gatesOf(ctx, id, branch, root, gs.storageFor(ctx, root), run)
}

// rows is the row reads bound to this call.
func (rd *statusReader) rows(ctx context.Context) rowSource {
	return rowSource{
		live:    rd.live,
		events:  rd.ledger(ctx),
		main:    rd.main,
		storage: rd.t.lay.StorageRoot,
		prefix:  rd.t.eff.IDPrefix,
		checkout: func(ctx context.Context, id, suffix string) (string, string) {
			r, err := rd.t.reference(ctx, id)
			if err != nil {
				return "", ""
			}

			name, root, err := taskCheckout(ctx, rd.main, rd.t.driver(), r, suffix)
			if err != nil {
				return "", ""
			}

			return name, root
		},
		resolve: rd.idOf(ctx),
		host:    rd.host,
		driven:  drivenTasks(rd.drives()),
		gates:   rd.gatesOf,
	}
}

// task prints what `status <token>` prints, reading the task's checkout
// first so a task with a run never waits on the host. With no run, a parent
// lists its children and a finished task its done row; a guessed token
// prints nothing, so the bare output stands.
func (rd *statusReader) task(ctx context.Context, v statusView, token string, guessed bool) (statusPrinted, error) {
	id, suffix := ref.SplitSuffix(token)

	r, err := rd.t.reference(ctx, id)
	if err != nil {
		return statusPrinted{}, err
	}

	in, err := taskStatusFor(ctx, rd.t, r, suffix)
	if errors.Is(err, errNoRunToRead) && !guessed {
		rows, cerr := rd.parentRows(ctx, r)

		switch {
		case cerr == nil && len(rows) > 0:
			return rd.printRows(v, rows, r.ID)
		case cerr != nil && !errors.Is(cerr, errNoParentLink):
			ui.Warnf(v.warn, "reading the subtasks: %v", cerr)
		}

		if row := rd.finishedRow(ctx, r.Head()); row != nil {
			return rd.printRows(v, []taskRow{*row}, r.ID)
		}
	}

	if err != nil {
		return statusPrinted{}, err
	}

	runs, others := taskRuns(inRoot(rd.live, in.root), in.id)
	ship := rd.shipment(ctx, in, runs)

	if len(runs) == 0 {
		fb := taskFallback(in, others, ship)

		if v.json {
			return statusPrinted{printed: true}, ui.JSON(v.w, fb)
		}

		fb.render(v.w, ui.Width(v.w), linksFor(v.w, v.plain).on)

		return statusPrinted{printed: true}, nil
	}

	gates := make([][]gate, len(runs))
	for i, r := range runs {
		gates[i] = rd.gatesOf(ctx, in.id, in.branch, in.root, r)
	}

	asks := slices.ContainsFunc(runs, func(r *state.Run) bool { return r.Ask != "" })

	return statusPrinted{printed: true, asks: asks},
		writeRuns(v, runs, gates, &ship, true, in.agent, rd.now)
}

// shipment is the task's facts block, finished before anything prints:
// a run that lost its url gets it back from the task refs, since every
// skill call reseeds its run, and the block takes the run's; with no run,
// the host is asked for the PR's state the no-run block shows.
//
//nolint:lostfield // a read: readShipment fills the fields
func (rd *statusReader) shipment(ctx context.Context, in taskStatusInput, runs []*state.Run) shipment {
	known := ledger.LookupRefs(ctx, rd.t.refProject(ctx), []string{in.id})[in.id]
	s := readShipment(ctx, in, rd.ledger(ctx), known)

	if len(runs) == 0 {
		s.askPR(ctx, in)

		return s
	}

	for _, r := range runs {
		r.URL = cmp.Or(r.URL, known.URL)
	}

	s.URL = cmp.Or(runs[0].URL, s.URL)

	return s
}

// launches prints the tree of what session sid started or drove that is
// still live; with nothing live it prints nothing, so the bare answer stays
// what it was.
func (rd *statusReader) launches(ctx context.Context, v statusView, sid string) (statusPrinted, error) {
	kids := launchKids(ctx, v.warn, rd.t, sid)
	if len(kids) == 0 {
		return statusPrinted{}, nil
	}

	rows := dropRows(rd.rows(ctx).rowsFrom(ctx, kids), func(r taskRow) bool { return r.Stage == stageDone })
	if len(rows) == 0 {
		return statusPrinted{}, nil
	}

	return rd.printRows(v, rows, launchesHead)
}

func (rd *statusReader) printRows(v statusView, rows []taskRow, head string) (statusPrinted, error) {
	asks := slices.ContainsFunc(rows, func(r taskRow) bool { return r.Stage == stageAsking })

	return statusPrinted{printed: true, asks: asks}, writeTaskRows(v, rows, head)
}

// statusText is what a bare `f10 status` prints for session in dir, with no
// color and no links, and whether it printed a question to relay.
func statusText(ctx context.Context, dir, session string) (string, bool) {
	var out bytes.Buffer

	asks, err := bareStatus(ctx, statusView{w: &out, warn: &out}, dir, false, session, false)
	if err != nil {
		out.WriteString("f10: " + err.Error() + "\n")
	}

	return strings.TrimSpace(out.String()), asks
}
