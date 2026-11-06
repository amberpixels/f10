package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/tasktree"
)

// Stages, as `status <parent>` names them, one per row.
const (
	stageDone         = "done"
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

// A taskRow is one task as `status <parent>` lists it, and the shape its
// --json prints. A finished task with no branch left prints one alone.
type taskRow struct {
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

	full bool // a finished task alone: every gate on a line of its own
}

// rowFacts is what a row's stage is decided from: the task's run, live in
// its worktree or as the ledger last recorded it, and the durable facts.
type rowFacts struct {
	run      *state.Run
	finished bool // finish ran, the tracker closed it, or its plan is archived
	merged   bool
	halt     *ledgerHalt // a halt drive recorded after the run's last claim
	after    string
	baseDone bool
	queued   bool // its drive's cap holds it back
	driven   bool // a running drive lists it
}

type ledgerHalt struct {
	skill, reason string
	at            time.Time
}

// stageOf is the one mapping from a task's facts to its stage and step.
func stageOf(f rowFacts) (string, string) {
	r := f.run

	switch {
	case f.finished || f.merged:
		return stageDone, "finish"
	case r != nil && r.Ask != "":
		return stageAsking, cmp.Or(r.LeafShown(), furthestPhase(r))
	case f.halt != nil:
		return stageHalted, f.halt.skill
	case r != nil && r.Wait != "" && stoppedRun(r):
		return stageWaiting, "on " + r.Wait
	case r != nil && stoppedRun(r):
		return stageHalted, cmp.Or(r.LeafShown(), furthestPhase(r))
	case r == nil && f.queued:
		return stageQueued, ""
	case r == nil && f.after != "" && !f.baseDone && f.driven:
		return stageWaiting, "after " + f.after
	case r == nil && f.after != "" && !f.baseDone:
		// only a drive holds a task for its base; nothing holds this one yet
		return stageNotStarted, "after " + f.after
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
		return stagePlanning, furthestPhase(r)
	case r.Plan != "pending" && r.Plan != "":
		return stagePlanned, "plan"
	}

	return stageNotStarted, ""
}

// furthestPhase is the last phase a run has left pending.
func furthestPhase(r *state.Run) string {
	phase := state.Phases[0]

	for _, p := range state.Phases {
		if s := r.Status(p); s != "pending" && s != "" {
			phase = p
		}
	}

	return phase
}

// ledgerTask is what the ledger holds about one task.
type ledgerTask struct {
	claim    *state.Run
	finished bool
	pr       string
	pushed   bool // a push on the task: its PR may exist though no pr_opened says so
	halt     *ledgerHalt
}

// readLedger reads the project's events once. A ledger that is missing or
// will not open has nothing to say: status only reads.
func readLedger(ctx context.Context, main string) []ledger.Event {
	_ = ledger.Sync(ctx) // a failed replay still reads what the database holds

	l, err := ledger.OpenRead(ctx, ledger.Path())
	if err != nil {
		return nil
	}

	defer l.Close()

	events, err := l.List(ctx, ledger.Filter{Project: ledger.Where(ctx, main).Project})
	if err != nil {
		return nil
	}

	return events
}

// ledgerFor folds the task's events, oldest first, into its last claim,
// whether finish ran on it, its PR and the halt drive saw after the claim.
// resolve turns finish's argument as typed (`117`, a url) into an id.
func ledgerFor(events []ledger.Event, id string, resolve func(string) string) ledgerTask {
	var lt ledgerTask

	for _, e := range events {
		p := e.Payload.Val

		switch {
		case e.Kind == ledger.TaskFinished:
			// finish runs from the main checkout, whose session may be on
			// another task: the id it was given decides
			if strings.EqualFold(resolve(e.FinishedTask()), id) {
				lt.finished = true
			}
		case !onTask(e, id):
		case strings.HasPrefix(e.Kind, "state_"):
			lt.claim = claimRun(e)
			if lt.halt != nil && !lt.halt.at.After(e.At) {
				lt.halt = nil
			}
		case e.Kind == ledger.PROpened:
			lt.pr = cmp.Or(payloadString(p, "url"), lt.pr)
		case e.Kind == ledger.Pushed:
			lt.pushed = true
		case e.Kind == ledger.TaskHalted:
			lt.halt = &ledgerHalt{skill: payloadString(p, "skill"), reason: payloadString(p, "reason"), at: e.At}
		}
	}

	return lt
}

// claimRun reads a recorded `f10 state` call back as the run it left.
func claimRun(e ledger.Event) *state.Run {
	p := e.Payload.Val

	return &state.Run{
		Session:  e.Session,
		Task:     e.Task,
		Root:     e.Checkout,
		Capture:  payloadString(p, "capture"),
		Plan:     payloadString(p, "plan"),
		Ship:     payloadString(p, "ship"),
		Leaf:     payloadString(p, "leaf"),
		Final:    payloadString(p, "final"),
		Pipeline: payloadStrings(p, "pipeline"),
		Labels:   payloadStrings(p, "labels"),
		At:       payloadInt(p, "at"),
		Note:     payloadString(p, "note"),
		Next:     payloadString(p, "next"),
		Ask:      payloadString(p, "ask"),
		Updated:  e.At,
	}
}

// payloadStrings is a list the payload holds as JSON, its strings only.
func payloadStrings(p ledger.Payload, key string) []string {
	raw, _ := p[key].([]any)

	var out []string

	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

// payloadInt is a number the payload holds; JSON gives it as a float.
func payloadInt(p ledger.Payload, key string) int {
	switch n := p[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}

	return 0
}

func payloadString(p ledger.Payload, key string) string {
	s, _ := p[key].(string)

	return s
}

// rowSource is what gathering rows reads from, bound once per status call.
type rowSource struct {
	live   []*state.Run
	events []ledger.Event
	// checkout is the task's branch and the checkout holding it, "" for none
	checkout func(ctx context.Context, id, suffix string) (string, string)
	// resolve is a reference as typed, in the project's id format; nil
	// takes it as written
	resolve func(token string) string
	host    *host
	// driven holds every task id a running drive lists, bundle riders too
	driven map[string]bool
	// gates reads a task's gates; nil reads none
	gates func(ctx context.Context, id, branch, root string, run *state.Run) []gate
}

// idOf is token as an id. An empty token, a finish run with no argument
// from a session with no task, names nothing: resolving it would read the
// reader's own branch.
func (src rowSource) idOf(token string) string {
	if token == "" || src.resolve == nil {
		return token
	}

	return src.resolve(token)
}

// rowsFrom builds a row per child, the reads for each running beside the
// others', then decides the stages in tree order so a base's stage is
// known before its dependents'.
func (src rowSource) rowsFrom(ctx context.Context, kids []childRow) []taskRow {
	facts := make([]rowFacts, len(kids))
	rows := make([]taskRow, len(kids))

	var wg sync.WaitGroup

	sem := make(chan struct{}, 4) // each row shells out to git and the host

	for i, k := range kids {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			rows[i], facts[i] = src.gather(ctx, k)
		})
	}

	wg.Wait()

	done := map[string]bool{}

	for i := range rows {
		facts[i].baseDone = done[facts[i].after]
		rows[i].finish(facts[i])
		done[rows[i].ID] = rows[i].Stage == stageDone
	}

	return rows
}

// gather reads one task's sources: the live run in its checkout, else the
// ledger's last claim, then the PR by branch or by the url the ledger kept.
func (src rowSource) gather(ctx context.Context, k childRow) (taskRow, rowFacts) {
	row := taskRow{ID: k.ID, Title: k.Title, URL: k.URL, Depth: k.Depth, After: k.After}
	f := rowFacts{
		finished: k.Finished,
		after:    k.After,
		queued:   k.queued,
		driven:   src.driven[strings.ToUpper(k.ID)],
	}
	lt := ledgerFor(src.events, k.ID, src.idOf)

	branch, root := "", ""
	if src.checkout != nil {
		branch, root = src.checkout(ctx, k.ID, k.suffix)
	}

	if root != "" {
		if own, _ := taskRuns(inRoot(src.live, root), k.ID); len(own) > 0 {
			f.run = own[0] // state.List sorts newest first
		}
	}

	if f.run == nil {
		f.run = lt.claim
	}

	f.finished = f.finished || lt.finished

	if lt.halt != nil && (f.run == nil || lt.halt.at.After(f.run.Updated)) {
		f.halt = lt.halt
	}

	// gh and glab take a branch or a request's url alike
	if at := cmp.Or(branch, lt.pr); at != "" && src.host != nil {
		if p, err := src.host.pull(ctx, at); err == nil {
			row.PR, row.PRState = p.url(), p.status()
		}
	}

	if row.PR == "" {
		row.PR = lt.pr
	}

	f.merged = row.PRState == "merged"

	if src.gates != nil {
		row.Gates = src.gates(ctx, k.ID, branch, root, f.run)
	}

	return row, f
}

// finish settles the row's stage and the lines printed under it.
func (row *taskRow) finish(f rowFacts) {
	row.Stage, row.Step = stageOf(f)

	// a shipped task whose finish drive holds says so where the step goes
	if row.Stage == stageShipped {
		for _, g := range row.Gates {
			if g.Step == "finish" && g.Via == viaDrive {
				row.Step = "finish: " + g.text()
			}
		}
	}

	switch row.Stage {
	case stageAsking:
		row.Ask = f.run.Ask
	case stageHalted:
		if f.halt != nil {
			row.Note = f.halt.reason
		} else {
			row.Note, row.Next = f.run.Note, f.run.Next
		}
	}
}

// rowStageStyle colors a stage by how much it needs you.
func rowStageStyle(stage string) lipgloss.Style {
	s := lipgloss.NewStyle()

	switch stage {
	case stageDone:
		return s.Foreground(lipgloss.Color("2"))
	case stageShipped:
		return s.Faint(true).Foreground(lipgloss.Color("2"))
	case stageAsking, stageHalted:
		return s.Bold(true).Foreground(lipgloss.Color("1"))
	case stageImplementing:
		return s.Foreground(lipgloss.Color("3"))
	case stageReviewing, stageResolving:
		return s.Foreground(lipgloss.Color("208"))
	case stagePlanning, stageJudging:
		return s.Foreground(lipgloss.Color("4"))
	case stagePlanned:
		return s.Faint(true).Foreground(lipgloss.Color("4"))
	default:
		return s.Faint(true)
	}
}

// rowView is how task rows print; plain is cut to a width and carries no
// urls, since a model reads it and runs `f10 task open --link` for one.
type rowView struct {
	parent string // the parent's id, heading its children; "" for a task alone
	width  int    // 0: no limit
	term   bool
	plain  bool
}

// stageOrder is the stages as a parent's heading counts them.
var stageOrder = []string{
	stageDone, stageShipped, stageAsking, stageHalted, stageImplementing, stageReviewing,
	stageResolving, stageJudging, stagePlanning, stagePlanned, stageWaiting, stageQueued, stageNotStarted,
}

// renderRows prints the rows as a task tree under the parent's heading.
func renderRows(w io.Writer, rows []taskRow, v rowView) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no children")

		return err
	}

	tree := tasktree.Tree{Align: true, Width: v.width}
	if v.parent != "" {
		tree.Head = v.parent + "  " + stageCounts(rows)
	}

	links := linksOn(v.term)

	faint := lipgloss.NewStyle().Faint(true)
	if v.term {
		tree.Label = func(s string) string { return faint.Render(s) }
	}

	// plain output keeps its rows narrow: the gates go on a line beneath
	withGates := !v.plain && slices.ContainsFunc(rows, func(r taskRow) bool { return r.gateColumn() != "" })
	nodes := make([]tasktree.Node, len(rows))

	for i, r := range rows {
		cells := []tasktree.Cell{
			{Text: stateGlyph(r.Stage) + " " + r.ID},
			{Text: r.Stage},
			{Text: r.Step},
		}

		if withGates {
			cells = append(cells, tasktree.Cell{Text: r.gateColumn()})
		}

		cells = append(cells, tasktree.Cell{Text: prColumn(r)})

		links.Known("", r.ID, r.URL)

		if v.term {
			glyph, id := stateGlyph(r.Stage), links.ID("", r.ID)
			stage, pr := rowStageStyle(r.Stage), r.PR

			cells[0].Render = func(string) string { return glyph + " " + id }
			cells[1].Render = func(s string) string { return stage.Render(s) }
			cells[2].Render = func(s string) string { return faint.Render(s) }
			cells[len(cells)-1].Render = func(s string) string { return hyperlink(pr, s) }
		}

		notes := make([]tasktree.Note, 0, 3)
		if gc := r.gateColumn(); v.plain && gc != "" {
			notes = append(notes, tasktree.Note{Label: "gates", Text: gc})
		}

		for _, sub := range r.subLines() {
			notes = append(notes, tasktree.Note{Label: sub[0], Text: sub[1], Whole: sub[0] == "ask"})
		}

		nodes[i] = tasktree.Node{Depth: r.Depth, Cells: cells, Title: r.Title, Notes: notes}
	}

	return tree.Write(w, nodes)
}

// stateGlyph marks a row's stage at a glance: done, stopped, waiting on an
// answer, or anything else.
func stateGlyph(stage string) string {
	switch stage {
	case stageDone, stageShipped:
		return "✓"
	case stageHalted:
		return "✗"
	case stageAsking:
		return "?"
	}

	return "·"
}

// stageCounts is `4 done · 2 halted`, the stages in stageOrder.
func stageCounts(rows []taskRow) string {
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

// subLines is what prints under a row: the ask, the halt reason and what
// unblocks it, then a finished task's gates.
func (row *taskRow) subLines() [][2]string {
	var out [][2]string

	for _, sub := range [][2]string{{"ask", row.Ask}, {"halt", row.Note}, {"next", row.Next}} {
		if sub[1] != "" {
			out = append(out, sub)
		}
	}

	if row.full {
		for _, l := range gateLines(row.Gates) {
			out = append(out, [2]string{strings.TrimSpace(l[0]), l[1]})
		}
	}

	return out
}

// gateColumn is the row's compact gates; a finished task alone prints
// every gate on a line instead.
func (row *taskRow) gateColumn() string {
	if row.full || len(row.Gates) == 0 {
		return ""
	}

	return compactGates(row.Gates)
}

// prColumn is `#128`, with the state beside it unless the PR is open, an
// open or merged PR's icon before it so the number reads as a PR.
func prColumn(r taskRow) string {
	if r.PR == "" {
		return ""
	}

	col := pullNumber(r.PR)

	switch r.PRState {
	case "", pullOpen:
		return statusGlyph(glyphOpenPR) + " " + col
	case pullMerged:
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

// pullNumber is a request's url as `#128`.
func pullNumber(url string) string {
	return "#" + path.Base(url)
}

// parentRows is `status <id>` for a parent: its children, closed ones
// included, as rows. errNoParentLink, or no rows, is not a parent.
func parentRows(ctx context.Context, t *target, r ref.Ref) ([]taskRow, error) {
	kids, err := t.tracker().allChildren(ctx, r)
	if err != nil || len(kids) == 0 {
		return nil, err
	}

	src, err := statusSource(ctx, t)
	if err != nil {
		return nil, err
	}

	return src.rowsFrom(ctx, kids), nil
}

// finishedRow is `status <id>` for a task with no branch left: one done
// row when the ledger, the tracker or the plan archive says it finished.
func finishedRow(ctx context.Context, t *target, r ref.Ref) (*taskRow, error) {
	src, err := statusSource(ctx, t)
	if err != nil {
		return nil, err
	}

	tr := t.tracker()
	info, _ := tr.info(ctx, r) // an unreachable tracker leaves the ledger and the archive

	lt := ledgerFor(src.events, r.ID, src.idOf)

	// a ledger finish counts only while no live plan says the task came back
	if !finishedTask(info, tr.plans, r.ID, false) && (!lt.finished || exists(filepath.Join(tr.plans, r.ID+".md"))) {
		return nil, nil
	}

	k := childRow{ID: r.ID, Finished: true}
	if h, err := t.issues(""); err == nil {
		k.Title, _ = h.issueTitle(ctx, r.Number)
		t.rememberRefs(ctx, ledger.TaskRef{ID: r.ID, Title: cleanTitle(r.ID, k.Title)})
	}

	rows := src.rowsFrom(ctx, []childRow{k})
	rows[0].full = true

	return &rows[0], nil
}

// drivenTasks is every task a drive lists while its driver runs, by id,
// each bundle member on its own. A stopped drive holds nothing until it
// resumes, as its queue does not.
func drivenTasks(root string) map[string]bool {
	recs, err := drives.List(root)
	if err != nil {
		return nil
	}

	out := map[string]bool{}

	for _, r := range recs {
		if r.Cancelled != nil || r.Done() {
			continue
		}

		if _, running := drives.Probe(root, drives.DriveKey(r.Number)); !running {
			continue
		}

		for _, t := range r.Tasks {
			for member := range strings.SplitSeq(t.ID, ref.Separator) {
				id, _ := ref.SplitSuffix(member)
				out[strings.ToUpper(id)] = true
			}
		}
	}

	return out
}

// statusSource binds the row reads to this target: live runs, the ledger,
// the checkout lookup and the host.
func statusSource(ctx context.Context, t *target) (rowSource, error) {
	live, err := state.List(state.Dir(), time.Now(), state.TTL())
	if err != nil {
		return rowSource{}, err
	}

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	src := rowSource{live: live, events: readLedger(ctx, main)}

	src.checkout = func(ctx context.Context, id, suffix string) (string, string) {
		r, err := t.reference(ctx, id)
		if err != nil {
			return "", ""
		}

		name, root, err := taskCheckout(ctx, main, t.driver(), r, suffix)
		if err != nil {
			return "", ""
		}

		return name, root
	}

	src.resolve = t.idOf(ctx)

	if h, err := t.host(); err == nil {
		src.host = &h
	}

	gs := newGateSource(t, src.events, src.host)
	src.driven = drivenTasks(gs.shared)
	src.gates = func(ctx context.Context, id, branch, root string, run *state.Run) []gate {
		return gs.gatesOf(ctx, id, branch, root, gs.storageFor(ctx, root, run), run)
	}

	return src, nil
}
