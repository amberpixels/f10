package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
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
		return stageAsking, cmp.Or(r.Leaf, furthestPhase(r))
	case f.halt != nil:
		return stageHalted, f.halt.skill
	case r != nil && stoppedRun(r):
		return stageHalted, cmp.Or(r.Leaf, furthestPhase(r))
	case r == nil && f.after != "" && !f.baseDone:
		return stageWaiting, "after " + f.after
	case r == nil:
		return stageNotStarted, ""
	}

	switch r.Ship {
	case "running":
		switch r.Leaf {
		case "review":
			return stageReviewing, r.Leaf
		case "resolve":
			return stageResolving, r.Leaf
		case "judge":
			return stageJudging, r.Leaf
		default:
			return stageImplementing, cmp.Or(r.Leaf, "ship")
		}
	case "done":
		return stageShipped, cmp.Or(r.Final, "ship")
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
	halt     *ledgerHalt
}

// readLedger reads the project's events once. A ledger that is missing or
// will not open has nothing to say: status only reads.
func readLedger(ctx context.Context, main string) []ledger.Event {
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
			if strings.EqualFold(resolve(finishedArg(p, e.Task)), id) {
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
		case e.Kind == ledger.TaskHalted:
			lt.halt = &ledgerHalt{skill: payloadString(p, "skill"), reason: payloadString(p, "reason"), at: e.At}
		}
	}

	return lt
}

// finishedArg is the task a recorded `f10 finish` named: its first
// argument that is not a flag, else the session's task.
func finishedArg(p ledger.Payload, task string) string {
	args, _ := p["args"].([]any)

	for _, a := range args {
		if s, ok := a.(string); ok && s != "" && !strings.HasPrefix(s, "-") {
			return s
		}
	}

	return task
}

// claimRun reads a recorded `f10 state` call back as the run it left.
func claimRun(e ledger.Event) *state.Run {
	p := e.Payload.Val

	return &state.Run{
		Session: e.Session,
		Task:    e.Task,
		Root:    e.Checkout,
		Capture: payloadString(p, "capture"),
		Plan:    payloadString(p, "plan"),
		Ship:    payloadString(p, "ship"),
		Leaf:    payloadString(p, "leaf"),
		Final:   payloadString(p, "final"),
		Note:    payloadString(p, "note"),
		Next:    payloadString(p, "next"),
		Ask:     payloadString(p, "ask"),
		Updated: e.At,
	}
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
	checkout func(ctx context.Context, id string) (string, string)
	// resolve is a reference as typed, in the project's id format; nil
	// takes it as written
	resolve func(token string) string
	host    *host
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
	f := rowFacts{finished: k.Finished, after: k.After}
	lt := ledgerFor(src.events, k.ID, src.idOf)

	branch, root := "", ""
	if src.checkout != nil {
		branch, root = src.checkout(ctx, k.ID)
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

	return row, f
}

// finish settles the row's stage and the lines printed under it.
func (row *taskRow) finish(f rowFacts) {
	row.Stage, row.Step = stageOf(f)

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

// stageStyle colors a stage by how much it needs you: red stopped, yellow
// building, orange under review, blue planning, green done, faint idle.
func stageStyle(stage string) lipgloss.Style {
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

// renderRows prints a line per row, columns padded to the widest value and
// the title cut so the line fits limit (0: no limit), with the ask or the
// halt reason on the lines beneath.
func renderRows(w io.Writer, rows []taskRow, limit int) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "no children")

		return
	}

	ids := make([]string, len(rows))
	prs := make([]string, len(rows))

	var idW, stageW, titleW, stepW int

	for i, r := range rows {
		ids[i] = strings.Repeat("  ", r.Depth) + r.ID
		prs[i] = prColumn(r)
		idW = max(idW, lipgloss.Width(ids[i]))
		stageW = max(stageW, len(r.Stage))
		titleW = max(titleW, lipgloss.Width(r.Title))
		stepW = max(stepW, lipgloss.Width(r.Step))
	}

	if limit > 0 {
		prW := 0
		for _, p := range prs {
			prW = max(prW, lipgloss.Width(p))
		}

		// two spaces between each of the five columns
		titleW = max(10, min(titleW, limit-idW-stageW-stepW-prW-8))
	}

	faint := lipgloss.NewStyle().Faint(true)
	indent := strings.Repeat(" ", idW+2)

	for i, r := range rows {
		line := fmt.Sprintf("%s  %s  %s  %s  %s",
			pad(ids[i], idW),
			stageStyle(r.Stage).Render(pad(r.Stage, stageW)),
			pad(cutTo(r.Title, titleW), titleW),
			faint.Render(pad(r.Step, stepW)),
			prs[i])
		fmt.Fprintln(w, strings.TrimRight(line, " "))

		for _, sub := range [][2]string{{"ask", r.Ask}, {"halt", r.Note}, {"next", r.Next}} {
			if sub[1] != "" {
				fmt.Fprintf(w, "%s%s %s\n", indent, faint.Render(sub[0]+":"), sub[1])
			}
		}
	}
}

// prColumn is `#128`, with the state beside it unless the PR is open.
func prColumn(r taskRow) string {
	if r.PR == "" {
		return ""
	}

	col := "#" + path.Base(r.PR)
	if r.PRState != "" && r.PRState != pullOpen {
		col += " " + r.PRState
	}

	return col
}

func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}

// cutTo shortens s to width cells, an ellipsis marking the cut.
func cutTo(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}

	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}

	return string(runes) + "…"
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

	if !finishedTask(info, tr.archive, r.ID, false) && !ledgerFor(src.events, r.ID, src.idOf).finished {
		return nil, nil
	}

	k := childRow{ID: r.ID, Finished: true}
	if h, err := t.issues(); err == nil {
		k.Title, _ = h.issueTitle(ctx, r.Number)
	}

	rows := src.rowsFrom(ctx, []childRow{k})

	return &rows[0], nil
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

	src.checkout = func(ctx context.Context, id string) (string, string) {
		r, err := t.reference(ctx, id)
		if err != nil {
			return "", ""
		}

		name, root, err := taskCheckout(ctx, main, t.driver(), r, "")
		if err != nil {
			return "", ""
		}

		return name, root
	}

	// each finish event is resolved once per child: cache the lookups, which
	// may start git, across the concurrent gathers
	var (
		mu  sync.Mutex
		ids = map[string]string{}
	)

	src.resolve = func(token string) string {
		mu.Lock()
		defer mu.Unlock()

		if id, ok := ids[token]; ok {
			return id
		}

		id := token
		if r, err := t.reference(ctx, token); err == nil {
			id = r.ID
		}

		ids[token] = id

		return id
	}

	if h, err := t.host(); err == nil {
		src.host = &h
	}

	return src, nil
}
