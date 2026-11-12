package statusview

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/tasktree"
)

// Stages, as `status <parent>` names them, one per row.
const (
	StageDone         = "done"
	stageShipped      = "shipped"
	stageAsking       = "asking"
	stageHalted       = "halted"
	stageImplementing = "implementing"
	stageReviewing    = "reviewing"
	stageResolving    = "resolving"
	stageJudging      = "judging"
	stagePlanning     = "planning"
	stagePlanned      = "planned"
	stageWaiting      = "waiting"
	stageQueued       = "queued"
	stageNotStarted   = "not started"
)

// A TaskRow is one task as `status <parent>` lists it, and the shape its
// --json prints. A finished task with no branch left prints one alone.
type TaskRow struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Depth   int    `json:"depth"`
	After   string `json:"after,omitempty"`
	Stage   string `json:"stage"`
	Step    string `json:"step,omitempty"`
	PR      string `json:"pr,omitempty"`
	PRState string `json:"pr_state,omitempty"`
	Note    string `json:"note,omitempty"`
	Next    string `json:"next,omitempty"`
	Ask     string `json:"ask,omitempty"`
	Gates   []gate `json:"gates,omitempty"`
}

// Finish settles the row's stage and the lines printed under it.
func (row *TaskRow) Finish(f lookaround.TaskFacts, baseDone bool) {
	row.Stage, row.Step = stageOf(f, baseDone)

	// a shipped task whose finish drive holds says so where the step goes
	if row.Stage == stageShipped {
		for _, g := range row.Gates {
			if g.Step == "finish" && g.Via == viaDrive {
				row.Step = "finish: " + g.Text()
			}
		}
	}

	switch row.Stage {
	case stageAsking:
		row.Ask = f.Run.Ask
	case stageHalted:
		if f.Halt != nil {
			row.Note = f.Halt.Reason
		} else {
			row.Note, row.Next = f.Run.Note, f.Run.Next
		}
	}
}

// GateColumn is the row's compact gates.
func (row *TaskRow) GateColumn() string {
	if len(row.Gates) == 0 {
		return ""
	}

	return compactGates(row.Gates)
}

// cells is the row's stage, step, gates and PR, and the lines under it.
func (row *TaskRow) cells(v RowView, withGates bool, faint lipgloss.Style) ([]tasktree.Cell, []tasktree.Note) {
	cells := []tasktree.Cell{{Text: row.Stage}, {Text: row.Step}}

	if withGates {
		cells = append(cells, tasktree.Cell{Text: row.GateColumn()})
	}

	cells = append(cells, tasktree.Cell{Text: prColumn(*row)})

	if v.Term {
		stage, pr := StageStyle(row.Stage), row.PR

		cells[0].Render = func(s string) string { return stage.Render(s) }
		cells[1].Render = func(s string) string { return faint.Render(s) }
		cells[len(cells)-1].Render = func(s string) string { return render.Hyperlink(pr, s) }
	}

	// plain output prints no link, so a relayed tree keeps its urls as text
	notes := make([]tasktree.Note, 0, 5)
	if v.Plain {
		for _, u := range [][2]string{{"url", row.URL}, {"pr", row.PR}} {
			if u[1] != "" {
				notes = append(notes, tasktree.Note{Label: u[0], Text: u[1], Whole: true})
			}
		}
	}

	if gc := row.GateColumn(); v.Plain && gc != "" {
		notes = append(notes, tasktree.Note{Label: "gates", Text: gc})
	}

	for _, sub := range row.subLines() {
		notes = append(notes, tasktree.Note{Label: sub[0], Text: sub[1], Whole: sub[0] == "ask"})
	}

	return cells, notes
}

// subLines is what prints under a row: the ask, the halt reason and what
// unblocks it.
func (row *TaskRow) subLines() [][2]string {
	var out [][2]string

	for _, sub := range [][2]string{{"ask", row.Ask}, {"halt", row.Note}, {"next", row.Next}} {
		if sub[1] != "" {
			out = append(out, sub)
		}
	}

	return out
}

// stageOf is the one mapping from a task's facts to its stage and step;
// baseDone says the base the task waits on is done.
func stageOf(f lookaround.TaskFacts, baseDone bool) (string, string) {
	r := f.Run

	switch {
	case f.Finished || f.PR != nil && f.PR.Status == forge.Merged:
		return StageDone, "finish"
	case r != nil && r.Ask != "":
		return stageAsking, cmp.Or(r.LeafShown(), r.Furthest())
	case f.Halt != nil:
		return stageHalted, f.Halt.Skill
	case r != nil && r.Wait != "" && drivectl.StoppedRun(r):
		return stageWaiting, "on " + r.Wait
	case r != nil && drivectl.StoppedRun(r):
		return stageHalted, cmp.Or(r.LeafShown(), r.Furthest())
	case f.DrivePhase == drivestore.PhaseParked:
		return stageWaiting, "on " + cmp.Or(f.Base, "its base")
	case f.DrivePhase == drivestore.PhaseWaiting:
		return stageWaiting, "to finish after " + cmp.Or(f.Base, "its base")
	case r == nil && f.Queued:
		return stageQueued, ""
	case r == nil && f.Base != "" && !baseDone && f.Driven:
		return stageWaiting, "after " + f.Base
	case r == nil && f.Base != "" && !baseDone:
		// only a drive holds a task for its base; nothing holds this one yet
		return stageNotStarted, "after " + f.Base
	case r == nil:
		return stageNotStarted, ""
	}

	switch r.Ship {
	case "running":
		switch r.Leaf {
		case "review":
			return stageReviewing, r.LeafShown()
		case "resolve":
			return stageResolving, r.LeafShown()
		case "judge":
			return stageJudging, r.LeafShown()
		default:
			return stageImplementing, cmp.Or(r.LeafShown(), "ship")
		}
	case "done":
		return stageShipped, cmp.Or(r.FinalShown(), "ship")
	}

	switch {
	case r.Capture == "running" || r.Plan == "running":
		return stagePlanning, r.Furthest()
	case r.Plan != state.Upcoming && r.Plan != "":
		return stagePlanned, "plan"
	}

	return stageNotStarted, ""
}

// readLedger reads the project's events once. A ledger that is missing or
// will not open has nothing to say: status only reads.
func readLedger(ctx context.Context, main string) []ledger.Event {
	events, _ := ledger.ReadEvents(ctx, ledger.Path(), ledger.Filter{Project: ledger.Where(ctx, main).Project})

	return events
}

// rowSource is what gathering rows reads from, bound once per status call.
type rowSource struct {
	Live    []*state.Run
	Events  []ledger.Event
	main    string       // where the branch records live
	storage storage.Root // where a live plan says a finished task came back
	prefix  string       // the task id prefix requests are matched by
	// Checkout is the task's branch and the checkout holding it, "" for none
	Checkout func(ctx context.Context, id, suffix string) (string, string)
	// Resolve is a reference as typed, in the project's id format; nil
	// takes it as written
	Resolve func(token string) string
	host    *hostops.Host
	// Driven holds every task id a running drive lists, bundle riders too
	Driven map[string]bool
	// gates reads a task's gates; nil reads none
	gates func(ctx context.Context, id, branch, root string, run *state.Run) []gate
	// Requests is every child's requests by upper-cased id, read once
	Requests map[string][]lookaround.PR
}

// RowsFrom builds a row per child from one host read of every child's
// requests, the local reads for each running beside the others', then
// decides the stages with each base's before its dependents'.
func (src rowSource) RowsFrom(ctx context.Context, kids []stack.ChildRow) []TaskRow {
	facts := make([]lookaround.TaskFacts, len(kids))
	rows := make([]TaskRow, len(kids))
	src.Requests = src.readRequests(ctx, kids)

	var wg sync.WaitGroup

	sem := make(chan struct{}, 4) // each row shells out to git

	for i, k := range kids {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			rows[i], facts[i] = src.Gather(ctx, k)
		})
	}

	wg.Wait()

	ids := make([]string, len(kids))
	for i, k := range kids {
		ids[i] = k.ID
	}

	lookaround.SettleBases(ids, func(i int) string { return facts[i].Base }, func(i, b int) {
		rows[i].Finish(facts[i], b >= 0 && rows[b].Stage == StageDone)
	})

	return rows
}

// Gather reads one task's facts: the live run in its checkout, else the
// ledger's last claim, the requests naming it (its branch's where it has
// one), and the base its branch records, else the tracker's.
func (src rowSource) Gather(ctx context.Context, k stack.ChildRow) (TaskRow, lookaround.TaskFacts) {
	row := TaskRow{ID: k.ID, Title: k.Title, URL: k.URL, Depth: k.Depth, After: k.After}
	f := lookaround.TaskFacts{
		Queued:     k.Queued,
		DrivePhase: k.Phase,
		Driven:     src.Driven[strings.ToUpper(k.ID)],
	}

	if k.Finished {
		f.Finished, f.FinishedFrom = true, lookaround.FinishedByTracker
	}

	branch, root := "", ""
	if src.Checkout != nil {
		branch, root = src.Checkout(ctx, k.ID, k.Suffix)
	}

	if root != "" {
		if own, _ := taskRuns(InRoot(src.Live, root), k.ID); len(own) > 0 {
			f.Run = own[0] // state.List sorts newest first
		}
	}

	f.AddLedger(lookaround.FoldLedger(src.Events, k.ID, src.idOf), src.storage.PlanLive(k.ID))

	// another row may share the id's requests, so they are sorted as a copy;
	// a task with a branch reads that branch's requests only, since an older
	// merged request on another head says nothing about the work in it
	prs := slices.Clone(src.Requests[strings.ToUpper(k.ID)])
	if branch != "" {
		prs = slices.DeleteFunc(prs, func(p lookaround.PR) bool { return p.Head != branch })
	}

	f.SetPRs(prs)

	// the search is capped and lags a new request, so a branch the search
	// missed is read exactly; a request no id names, by the url the ledger kept
	for _, at := range []string{branch, f.PRURL} {
		if f.PR != nil || at == "" || src.host == nil {
			continue
		}

		if p, err := src.host.PR(ctx, at); err == nil {
			f.SetPRs([]lookaround.PR{lookaround.NewPR(p)})
		}
	}

	if f.PR != nil {
		row.PR, row.PRState = f.PR.URL, f.PR.Status
	} else {
		row.PR = f.PRURL
	}

	var branchAfter string
	if branch != "" {
		branchAfter = branchmeta.Out(ctx, src.main, branch, branchmeta.After)
	}

	f.Base, f.BaseFrom = lookaround.BaseOf(branchAfter, f.PR, k.After)

	if src.gates != nil {
		row.Gates = src.gates(ctx, k.ID, branch, root, f.Run)
	}

	return row, f
}

// idOf is token as an id. An empty token, a finish run with no argument
// from a session with no task, names nothing: resolving it would read the
// reader's own branch.
func (src rowSource) idOf(token string) string {
	if token == "" || src.Resolve == nil {
		return token
	}

	return src.Resolve(token)
}

// readRequests is every child's requests by upper-cased id, one host query
// for all of them. A failed read leaves each row to the url the ledger kept.
func (src rowSource) readRequests(ctx context.Context, kids []stack.ChildRow) map[string][]lookaround.PR {
	if src.host == nil || len(kids) == 0 {
		return nil
	}

	ids := make([]string, 0, len(kids))
	for _, k := range kids {
		if !slices.ContainsFunc(ids, func(id string) bool { return strings.EqualFold(id, k.ID) }) {
			ids = append(ids, k.ID)
		}
	}

	found, err := lookaround.TaskRequests(ctx, &src.host.Host, src.prefix, ids)
	if err != nil {
		return nil
	}

	out := make(map[string][]lookaround.PR, len(found))
	for id, prs := range found {
		out[strings.ToUpper(id)] = prs
	}

	return out
}

// RowView is how task rows print; plain is cut to a width and carries no
// urls, since a model reads it and runs `f10 task open --link` for one.
type RowView struct {
	Parent string // the parent's id, heading its children; "" for a task alone
	Width  int    // 0: no limit
	Term   bool
	Plain  bool
}

// stageOrder is the stages as a parent's heading counts them.
var stageOrder = []string{
	StageDone, stageShipped, stageAsking, stageHalted, stageImplementing, stageReviewing,
	stageResolving, stageJudging, stagePlanning, stagePlanned, stageWaiting, stageQueued, stageNotStarted,
}

// RenderRows prints the rows as a task tree under the parent's heading.
func RenderRows(w io.Writer, rows []TaskRow, v RowView) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no children")

		return err
	}

	tree := tasktree.Tree{Align: true, Width: v.Width}
	if v.Parent != "" {
		tree.Head = v.Parent + "  " + stageCounts(rows)
	}

	links := render.LinksOn(v.Term)

	faint := lipgloss.NewStyle().Faint(true)
	if v.Term {
		tree.Label = func(s string) string { return faint.Render(s) }
	}

	// plain output keeps its rows narrow: the gates go on a line beneath
	withGates := !v.Plain && slices.ContainsFunc(rows, func(r TaskRow) bool { return r.GateColumn() != "" })

	tr := make([]tasksource.TreeRow, len(rows))
	for i, r := range rows {
		tr[i] = tasksource.TreeRow{ID: r.ID, Title: r.Title, URL: r.URL, Depth: r.Depth}
		links.Known("", r.ID, r.URL)
	}

	nodes := tasksource.SubtaskNodes(tr, tasksource.TreeOpts{
		TitleBelow: true,
		ID: func(i int) tasktree.Cell {
			c := tasktree.Cell{Text: stateGlyph(rows[i].Stage) + " " + rows[i].ID}
			if v.Term {
				glyph, id := stateGlyph(rows[i].Stage), links.ID("", rows[i].ID)
				c.Render = func(string) string { return glyph + " " + id }
			}

			return c
		},
		Extra: func(i int) ([]tasktree.Cell, []tasktree.Note) { return rows[i].cells(v, withGates, faint) },
	})

	return tree.Write(w, nodes)
}

// stateGlyph marks a row's stage at a glance: done, stopped, waiting on an
// answer, or anything else.
func stateGlyph(stage string) string {
	switch stage {
	case StageDone, stageShipped:
		return "✓"
	case stageHalted:
		return "✗"
	case stageAsking:
		return "?"
	}

	return "·"
}

// stageCounts is `4 done · 2 halted`, the stages in stageOrder.
func stageCounts(rows []TaskRow) string {
	n := map[string]int{}
	for _, r := range rows {
		n[r.Stage]++
	}

	var parts []string

	for _, st := range stageOrder {
		if n[st] > 0 {
			parts = append(parts, strconv.Itoa(n[st])+" "+st)
		}
	}

	return strings.Join(parts, " · ")
}

// prColumn is `#128`, with the state beside it unless the PR is open, an
// open or merged PR's icon before it so the number reads as a PR.
func prColumn(r TaskRow) string {
	if r.PR == "" {
		return ""
	}

	col := forge.PullNumber(r.PR)

	switch r.PRState {
	case "", forge.Open:
		return statusGlyph(glyphOpenPR) + " " + col
	case forge.Merged:
		return statusGlyph(glyphMergedPR) + " " + col + " " + r.PRState
	}

	return col + " " + r.PRState
}

// The status tree's icons, by position in F10_STATUS_GLYPHS.
const (
	glyphOpenPR = iota
	glyphMergedPR
	glyphReview
	glyphJudge
)

// statusGlyph is a Nerd Font md-* icon, the only range that keeps one cell
// in Claude Code's output; F10_STATUS_GLYPHS replaces all four for a
// terminal without the font.
func statusGlyph(i int) string {
	glyphs := []string{"\U000F04C2", "\U000F062D", "\U000F0694", "\U000F029B"}
	if set := strings.Fields(os.Getenv("F10_STATUS_GLYPHS")); len(set) == len(glyphs) {
		glyphs = set
	}

	return glyphs[i]
}

// drivenTasks is every task a drive lists while its driver runs, by id,
// each bundle member on its own. A stopped drive holds nothing until it
// resumes, as its queue does not.
func drivenTasks(active []drivestore.Live) map[string]bool {
	out := map[string]bool{}

	for _, d := range active {
		if !d.Running {
			continue
		}

		for _, t := range d.Tasks {
			for _, id := range ref.TypedMembers(t.ID) {
				out[id] = true
			}
		}
	}

	return out
}
