package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The status noun reads what the badge reads and says the rest: which step
// a run stopped on, why, and what unblocks it. A task's run is read from the
// checkout holding its branch, so a session can relay a driven agent's ask.
func statusCommand() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "where this session's f10 run is, what it started or drove, a task's run in its own worktree, or every live run in this repo",
		ArgsUsage: "[task-id]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "every live run on this machine, not only this repo's"},
			&cli.BoolFlag{Name: "json", Usage: "emit the same runs as JSON"},
			&cli.BoolFlag{
				Name: "plain",
				Usage: "print a parent's, a finished task's or the launched-here tree as plain text, without urls, " +
					"cut to $COLUMNS (else 100), for a skill to relay",
			},
		},
		Action: runStatus,
	}
}

func runStatus(ctx context.Context, cmd *cli.Command) error {
	dir := state.Dir()
	now := time.Now()
	all := cmd.Bool("all")
	token := strings.TrimSpace(cmd.Args().First())
	sid := state.Session()

	if token != "" {
		if all {
			return errors.New("status takes a task id or --all, not both: the id already picks a checkout")
		}

		t, err := targetFor(ctx, cmd)
		if err != nil {
			return err
		}

		_, err = taskStatus(ctx, cmd, t, token, false)

		return err
	}

	// the session run's task before the checkout's, the reverse of ref.Lookup,
	// since the run is what status reports; in a checkout naming no task the
	// launches come first, as the session run's task would hide them
	if !all {
		if t, err := targetFor(ctx, cmd); err == nil && !t.scoped {
			here := checkoutTaskID(ctx, t)
			if here == "" && sid != "" {
				if done, err := launchStatus(ctx, cmd, t, sid); done {
					return err
				}
			}

			if id := cmp.Or(sessionTaskID(sid), here); id != "" {
				if done, err := taskStatus(ctx, cmd, t, id, true); done {
					return err
				}
			}
		}
	}

	var (
		runs     []*state.Run
		showRoot bool
	)

	switch {
	case sid != "" && !all:
		r, err := state.Load(state.Path(dir, sid))
		if errors.Is(err, os.ErrNotExist) {
			_, err = fmt.Fprintln(cmd.Writer, "no f10 run in this session")

			return err
		}

		if err != nil {
			return err
		}

		runs = []*state.Run{r}
	default:
		live, err := state.List(dir, now, state.TTL())
		if err != nil {
			return err
		}

		showRoot = true
		runs = live

		if !all {
			root, err := checkoutRoot(ctx, cmd)
			if err != nil {
				return err
			}

			runs = inRoot(live, root)

			if len(runs) == 0 {
				_, err = fmt.Fprintf(cmd.Writer, "no live f10 run in %s (--all lists every session)\n", root)

				return err
			}
		}

		if len(runs) == 0 {
			_, err = fmt.Fprintln(cmd.Writer, "no live f10 run on this machine")

			return err
		}
	}

	return writeRuns(cmd, runs, make([][]gate, len(runs)), nil, showRoot, "", now)
}

// sessionTaskID is the task the session's run names, "" for none.
func sessionTaskID(sid string) string {
	if sid == "" {
		return ""
	}

	if r, err := state.Load(state.Path(state.Dir(), sid)); err == nil {
		return r.Task
	}

	return ""
}

// checkoutTaskID is the task the branch names, else the one a linked
// worktree's directory names on a detached HEAD, "" for none.
func checkoutTaskID(ctx context.Context, t *target) string {
	if r, err := (ref.Lookup{Prefix: t.eff.IDPrefix, Dir: t.dir, NoSession: true}).Find(ctx, ""); err == nil {
		return r.Label()
	}

	linked := t.lay.MainRoot != "" && canonPath(t.lay.MainRoot) != canonPath(t.lay.CheckoutRoot)
	if !linked || gitx.Out(ctx, t.dir, "rev-parse", "--abbrev-ref", "HEAD") != "HEAD" {
		return ""
	}

	return ref.Match(t.eff.IDPrefix, filepath.Base(t.lay.CheckoutRoot))
}

// taskStatus prints what `status <token>` prints, reporting whether it
// printed. A guessed token whose task has no checkout here asks the host
// nothing and prints nothing, so the bare output stands.
func taskStatus(ctx context.Context, cmd *cli.Command, t *target, token string, guessed bool) (bool, error) {
	id, suffix := ref.SplitSuffix(token)

	r, err := t.reference(ctx, id)
	if err != nil {
		return false, err
	}

	route := taskRoute{
		status:   func() (taskStatusInput, error) { return taskStatusFor(ctx, t, r, suffix) },
		children: func() ([]taskRow, error) { return parentRows(ctx, t, r) },
		finished: func() (*taskRow, error) { return finishedRow(ctx, t, r.Head()) },
	}

	if guessed {
		route.children = func() ([]taskRow, error) { return nil, nil }
		route.finished = func() (*taskRow, error) { return nil, nil }
	}

	in, rows, err := routeTask(cmd.ErrWriter, route)

	switch {
	case err != nil:
		return false, err
	case in == nil:
		return true, writeTaskRows(cmd, rows, r.ID)
	}

	now := time.Now()

	live, err := state.List(state.Dir(), now, state.TTL())
	if err != nil {
		return false, err
	}

	runs, others := taskRuns(inRoot(live, in.root), in.id)

	events := readLedger(ctx, cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot))
	known := ledger.LookupRefs(ctx, t.refProject(ctx), []string{in.id})[in.id]
	ship := readShipment(ctx, *in, events, known)

	if len(runs) == 0 {
		fb := taskFallback(ctx, *in, others, ship)

		if cmd.Bool("json") {
			return true, encodeJSON(cmd.Writer, fb)
		}

		fb.render(cmd.Writer, termWidth(cmd.Writer), linksFor(cmd.Writer, cmd.Bool("plain")).on)

		return true, nil
	}

	// every skill call reseeds its run, which drops the url fetch recorded
	for _, r := range runs {
		r.URL = cmp.Or(r.URL, known.URL)
	}

	ship.url = cmp.Or(runs[0].URL, ship.url)
	ship.link = linksFor(cmd.Writer, cmd.Bool("plain")).on

	return true, writeRuns(cmd, runs, runGates(ctx, t, *in, events, runs), &ship, true, in.agent, now)
}

// writeRuns prints runs, each with its gates, or encodes them under --json.
// head is the task's shipment, printed once above the runs in place of each
// run's task and url rows; nil for runs listed without a task. agent is the
// profile the task's branch records, "" for the kind's default.
func writeRuns(cmd *cli.Command, runs []*state.Run, gates [][]gate, head *shipment, showRoot bool, agent string,
	now time.Time,
) error {
	if cmd.Bool("json") {
		out := make([]runJSON, len(runs))
		for i, r := range runs {
			out[i] = runJSON{Run: r, Agent: agent, Gates: gates[i]}
			if head != nil {
				out[i].Verdicts, out[i].Reviews = head.verdicts, head.reviews
			}
		}

		return encodeJSON(cmd.Writer, out)
	}

	limit := termWidth(cmd.Writer)

	if head != nil {
		if err := writeReport(cmd.Writer, head.facts(), nil); err != nil {
			return err
		}
	}

	for i, r := range runs {
		if i > 0 || head != nil {
			fmt.Fprintln(cmd.Writer)
		}

		if head != nil {
			renderRunRows(cmd.Writer, r, gates[i], showRoot, agent, now, limit)
		} else {
			renderRun(cmd.Writer, r, gates[i], showRoot, agent, now, limit)
		}
	}

	return nil
}

// shipment is the facts block a ship report opens with, reprinted from what
// f10 holds locally; only a pushed task with no PR recorded asks the host.
type shipment struct {
	task, title, url, branch, pr, commit, plan, handoff string

	prState string // the PR's state, when the host was asked
	pulled  bool   // the host was asked for the PR, so the fallback asks no more

	verdicts []verdictPath
	reviews  []reviewPath

	link bool // the file paths link on a terminal; off for --plain
}

// verdictPath is a judge verdict file the facts block links, by its subject.
type verdictPath struct {
	Subject string `json:"subject"` // task, plan or change
	Path    string `json:"path"`
}

// reviewPath is a findings file the facts block links, by its round.
type reviewPath struct {
	Round string `json:"round"`
	Path  string `json:"path"`
}

// facts is the block's rows in the ship report's order, empty ones left out.
func (s shipment) facts() []fact {
	rows := []fact{
		{label: "task", value: s.task},
		{label: "title", value: s.title},
		{label: "url", value: s.url, url: true},
		{label: "branch", value: s.branch},
		{label: "pr", value: s.pr, url: true},
		{label: "commit", value: s.commit},
		{label: "plan", value: s.plan, path: s.link},
		{label: "handoff", value: s.handoff, path: s.link},
	}

	// a subject judged twice takes each file's number, so every label names one file
	seen := map[string]int{}
	for _, v := range s.verdicts {
		seen[v.Subject]++
	}

	for _, v := range s.verdicts {
		label := "judge " + v.Subject
		if seen[v.Subject] > 1 {
			label += " " + strings.TrimSuffix(filepath.Base(v.Path), ".md")
		}

		rows = append(rows, fact{label: label, value: v.Path, path: s.link})
	}

	for _, r := range s.reviews {
		rows = append(rows, fact{label: "review " + r.Round, value: r.Path, path: s.link})
	}

	return slices.DeleteFunc(rows, func(f fact) bool { return f.value == "" })
}

// readShipment fills the shipment from the ledger's task ref and PR, git and
// the storage root. The commit row waits for a commit of the task's own:
// before one, HEAD is the base's.
func readShipment(ctx context.Context, in taskStatusInput, events []ledger.Event, known ledger.TaskRef) shipment {
	lt := ledgerFor(events, in.id, func(token string) string { return token })

	s := shipment{
		task:   in.id,
		title:  known.Title,
		url:    known.URL,
		branch: in.branch,
		pr:     lt.pr,
	}

	// a PR opened by a script around the host CLI leaves no pr_opened; only a
	// pushed task asks the host, so a status before the PR stays local
	if s.pr == "" && lt.pushed && in.host != nil && in.branch != "" {
		s.pulled = true
		if p, err := in.host.pull(ctx, in.branch); err == nil {
			s.pr, s.prState = p.url(), p.status()
		}
	}

	if in.storage != "" {
		s.verdicts, s.reviews = shipFiles(in.storage, in.id)
	}

	if in.main != "" && in.branch != "" {
		if def, err := localDefault(ctx, in.main); err == nil && baseHasCode(ctx, in.main, in.branch, def) {
			s.commit = gitx.Out(ctx, in.main, "rev-parse", "--short", "refs/heads/"+in.branch)
		}
	}

	if exists(in.plan) {
		s.plan = in.plan
	}

	if exists(in.handoff) {
		s.handoff = in.handoff
	}

	return s
}

// shipFiles is the task's verdict and findings files. Verdicts from before
// the open series are an earlier PR's, dropped as the judge row drops them.
func shipFiles(storage, id string) ([]verdictPath, []reviewPath) {
	reviews := filepath.Join(storage, "reviews", id)
	since := seriesStart(reviews)

	var (
		verdicts []verdictPath
		rounds   []reviewPath
	)

	for _, v := range judgeFiles(storage, id) {
		if !v.at.Before(since) {
			verdicts = append(verdicts, verdictPath{Subject: v.judged, Path: v.path})
		}
	}

	for _, path := range numbered(reviews) {
		rounds = append(rounds, reviewPath{Round: strings.TrimSuffix(filepath.Base(path), ".md"), Path: path})
	}

	return verdicts, rounds
}

// taskStatusInput is what `status <id>` knows about a task before reading
// any run.
type taskStatusInput struct {
	id      string
	branch  string
	root    string // the checkout holding branch
	main    string // the main checkout, where the branch's refs and config live
	plan    string // where the task's plan would be
	handoff string // where the task's handoff would be
	storage string // the storage root holding the task's verdicts and findings
	host    *host  // nil without a host CLI: no PR row
	agent   string // the profile the branch records, as typed; "" for the kind's default
}

// taskRoute is what `status <id>` can report on a task, read lazily.
type taskRoute struct {
	status   func() (taskStatusInput, error)
	children func() ([]taskRow, error)
	finished func() (*taskRow, error)
}

// routeTask reads the task's own checkout first, so a task with a run never
// waits on the host. With no run to read, a parent reports its children
// and a finished task its done row; anything else keeps the error.
func routeTask(warn io.Writer, rt taskRoute) (*taskStatusInput, []taskRow, error) {
	in, err := rt.status()
	if !errors.Is(err, errNoRunToRead) {
		if err != nil {
			return nil, nil, err
		}

		return &in, nil, nil
	}

	rows, cerr := rt.children()

	switch {
	case cerr == nil && len(rows) > 0:
		return nil, rows, nil
	case cerr != nil && !errors.Is(cerr, errNoParentLink):
		fmt.Fprintf(warn, "f10: reading the subtasks: %v\n", cerr)
	}

	row, ferr := rt.finished()
	if ferr != nil {
		return nil, nil, ferr
	}

	if row == nil {
		return nil, nil, err
	}

	return nil, []taskRow{*row}, nil
}

// errNoRunToRead is a task with no branch, or a branch checked out
// nowhere: no run to read, though a finished one still reports.
var errNoRunToRead = errors.New("no run to read")

// taskStatusFor resolves the task to its checkout.
func taskStatusFor(ctx context.Context, t *target, r ref.Ref, suffix string) (taskStatusInput, error) {
	// a bundle's run, plan and branch are its lead's
	r = r.Head()

	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)

	name, path, err := taskCheckout(ctx, main, t.driver(), r, suffix)

	switch {
	case err != nil:
		return taskStatusInput{}, err
	case name == "":
		return taskStatusInput{}, fmt.Errorf("%s has no branch here: %w", r.ID, errNoRunToRead)
	case path == "":
		return taskStatusInput{}, fmt.Errorf("branch %s is checked out nowhere: %w", name, errNoRunToRead)
	}

	in := taskStatusInput{
		id:      r.ID,
		branch:  name,
		root:    path,
		main:    main,
		plan:    taskPlan(path, t.sharedPlansDir(), r.ID),
		handoff: taskHandoff(main, t.sharedPlansDir(), r.ID),
		storage: filepath.Dir(taskPlansDir(main, t.sharedPlansDir())),
	}

	if h, err := t.host(); err == nil {
		h = h.at(path)
		in.host = &h
	}

	// a branch with no record, or one unreadable, shows no agent row
	if p, err := recordedProfile(ctx, main, name); err == nil && !p.IsDefault() {
		in.agent = p.Short()
	}

	return in, nil
}

// taskRuns splits the checkout's runs into the task's own and the rest,
// since every skill call reseeds its run and the agent may be on another
// task by now; an empty task is the task's only in a phase skill's run that
// fetch has not named yet, since a catchup's run names no task either.
func taskRuns(runs []*state.Run, id string) ([]*state.Run, []*state.Run) {
	var own, others []*state.Run

	for _, r := range runs {
		fresh := r.Task == "" && state.OpensPhase(r.Skill) && (r.Capture == "pending" || r.Capture == "skipped")

		if strings.EqualFold(r.Task, id) || fresh {
			own = append(own, r)
		} else {
			others = append(others, r)
		}
	}

	return own, others
}

// fallback is what `status <id>` reports for a task no live run is about:
// the runs in its checkout, which are on something else, then what is durable.
type fallback struct {
	Task    string       `json:"task"`
	Root    string       `json:"root"`
	Others  []*state.Run `json:"others"`
	Plan    string       `json:"plan,omitempty"`
	PR      string       `json:"pr,omitempty"`
	PRState string       `json:"pr_state,omitempty"`

	Verdicts []verdictPath `json:"verdicts,omitempty"`
	Reviews  []reviewPath  `json:"reviews,omitempty"`

	ship shipment // the facts block render opens with; the JSON keeps its keys
}

// taskFallback gathers the fallback around the task's shipment. Status only
// reads, so a host that cannot answer drops the PR rather than failing the report.
func taskFallback(ctx context.Context, in taskStatusInput, others []*state.Run, ship shipment) fallback {
	fb := fallback{
		Task: in.id, Root: in.root, Others: others, PR: ship.pr,
		Verdicts: ship.verdicts, Reviews: ship.reviews, ship: ship,
	}
	if fb.Others == nil {
		fb.Others = []*state.Run{} // an empty list, not null, for a JSON reader
	}

	if exists(in.plan) {
		fb.Plan = in.plan
	}

	switch {
	case ship.pulled:
		fb.PRState = ship.prState
	case in.host != nil:
		if p, err := in.host.pull(ctx, in.branch); err == nil && p.url() != "" {
			fb.PR, fb.PRState = p.url(), p.status()
		}
	}

	return fb
}

func (fb fallback) render(w io.Writer, limit int, link bool) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	s := fb.ship
	s.task, s.plan, s.pr, s.link = fb.Task, fb.Plan, cmp.Or(fb.PR, s.pr), link
	_ = writeReport(w, s.facts(), nil)

	fmt.Fprintln(w)
	headerRow(w, "run", "none for "+fb.Task+" in "+fb.Root, limit, faint)

	for _, r := range fb.Others {
		headerRow(w, "agent", valueOr(r.Task)+" · "+currentPhase(r), limit, plain)
	}

	if fb.PRState != "" {
		headerRow(w, "pr state", fb.PRState, limit, plain)
	}
}

// writeTaskRows prints task rows as a tree, headed by parent unless the
// one row is a finished task alone, or encodes them under --json.
func writeTaskRows(cmd *cli.Command, rows []taskRow, parent string) error {
	if cmd.Bool("json") {
		return encodeJSON(cmd.Writer, rows)
	}

	v := rowView{parent: parent, plain: cmd.Bool("plain")}
	if len(rows) == 1 && rows[0].full {
		v.parent = ""
	}

	switch {
	case v.plain:
		v.width = plainWidth()
	case isTerminal(cmd.Writer):
		v.term, v.width = true, termWidth(cmd.Writer)
	}

	return renderRows(cmd.Writer, rows, v)
}

// plainWidth is the width plain output is cut to: $COLUMNS, else 100, as
// a skill relays it into a pane whose width the binary cannot read.
func plainWidth() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}

	return 100
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding status: %w", err)
	}

	return nil
}

// currentPhase is the furthest phase a run has reached, with its status:
// `capture running`, `ship blocked at judge`.
func currentPhase(r *state.Run) string {
	phase := state.Phases[0]

	for _, p := range state.Phases {
		if r.Status(p) != "pending" {
			phase = p
		}
	}

	return phase + " " + r.PhaseText(phase)
}

// checkoutRoot is the repo a plain terminal asks about: -C's target or the
// cwd, in physical form, the way the seed hook recorded root.
func checkoutRoot(ctx context.Context, cmd *cli.Command) (string, error) {
	dir, _, err := rootDir(cmd)
	if err != nil {
		return "", err
	}

	return layout.Locate(ctx, dir).CheckoutRoot, nil
}

// inRoot keeps the runs recorded against root. The hook stores git's
// toplevel and the layout package stores the physical path, so both sides are
// compared in physical form.
func inRoot(runs []*state.Run, root string) []*state.Run {
	var kept []*state.Run

	for _, r := range runs {
		if r.Root != "" && canonPath(r.Root) == canonPath(root) {
			kept = append(kept, r)
		}
	}

	return kept
}

func canonPath(p string) string {
	if c, err := filepath.EvalSymlinks(p); err == nil {
		return c
	}

	return p
}

// runJSON is a run as --json prints it, a task's gates beside the state's
// own keys.
type runJSON struct {
	*state.Run

	Agent string `json:"agent,omitempty"`
	Gates []gate `json:"gates,omitempty"`

	// the task's files, repeated per run as the agent is
	Verdicts []verdictPath `json:"verdicts,omitempty"`
	Reviews  []reviewPath  `json:"reviews,omitempty"`
}

// runGates is each of the task's runs' gates, from the ledger's events.
func runGates(ctx context.Context, t *target, in taskStatusInput, events []ledger.Event, runs []*state.Run) [][]gate {
	src := newGateSource(t, events, in.host)
	out := make([][]gate, len(runs))

	for i, r := range runs {
		out[i] = src.gatesOf(ctx, in.id, in.branch, in.root, layout.Locate(ctx, r.Root).StorageRoot, r)
	}

	return out
}

// newGateSource binds the gate reads to this target.
func newGateSource(t *target, events []ledger.Event, h *host) gateSource {
	src := gateSource{
		events: events,
		main:   cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot),
		flows:  t.eff.Flows,
		labels: t.eff.FlowLabels,
		review: t.eff.Review,
		store:  &lore.Store{Root: loreRootFor(t)},
		host:   h,
	}

	src.shared = t.sharedRoot()
	src.driveFlows = driveFlows(src.shared)

	return src
}

// renderRun prints one run in the order `f10 state show` prints the same
// facts, the task's gates under ship when it has them. The root row appears
// only when listing: for one session it is the directory you are in.
func renderRun(w io.Writer, r *state.Run, gates []gate, showRoot bool, agent string, now time.Time, limit int) {
	plain := lipgloss.NewStyle()

	headerRow(w, "task", valueOr(r.Task), limit, plain)
	headerRow(w, "url", valueOr(r.URL), limit, plain)
	renderRunRows(w, r, gates, showRoot, agent, now, limit)
}

// renderRunRows is a run's rows after its task and url: the step rows a
// shipment block heads.
func renderRunRows(w io.Writer, r *state.Run, gates []gate, showRoot bool, agent string, now time.Time, limit int) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)

	if agent != "" {
		headerRow(w, "agent", agent, limit, faint)
	}

	if showRoot {
		headerRow(w, "root", valueOr(r.Root), limit, faint)
	}

	for _, p := range state.Phases {
		headerRow(w, p, r.PhaseText(p), limit, statusStyle(r.Status(p)))
	}

	for _, g := range gates {
		headerRow(w, "  "+g.shown(), g.text(), limit, statusStyle(g.style()))
	}

	if r.Note != "" {
		headerRow(w, "note", r.Note, limit, plain)
	}

	if r.Next != "" {
		headerRow(w, "next", r.Next, limit, plain)
	}

	if r.Ask != "" {
		headerRow(w, "ask", r.Ask, limit, plain)
	}

	if r.Final != "" {
		headerRow(w, "final", r.Final, limit, faint)
	}

	age := ago(now, r.Updated)
	if !r.Live(now, state.TTL()) {
		age += " (stale)"
	}

	headerRow(w, "updated", age, limit, faint)
}

func valueOr(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// statusStyle is the badge's palette: shape first, color second, so the
// same status reads the same in the status line and here.
func statusStyle(status string) lipgloss.Style {
	s := lipgloss.NewStyle()

	switch status {
	case "done":
		return s.Foreground(lipgloss.Color("2"))
	case "running":
		return s.Bold(true).Foreground(lipgloss.Color("6"))
	case "failed":
		return s.Bold(true).Foreground(lipgloss.Color("1"))
	case "partial":
		return s.Bold(true).Foreground(lipgloss.Color("3"))
	case "blocked":
		return s.Bold(true).Foreground(lipgloss.Color("5"))
	case "prior":
		return s.Faint(true).Foreground(lipgloss.Color("2"))
	default:
		return s.Faint(true)
	}
}

// ago is a duration a human reads at a glance: seconds under a minute,
// then the largest whole unit that fits.
func ago(now, then time.Time) string {
	if then.IsZero() {
		return "unknown"
	}

	d := now.Sub(then).Round(time.Second)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
