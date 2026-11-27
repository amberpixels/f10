package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"github.com/amberpixels/f10/cli/internal/render"
)

// A job is one run of the selection, as its row shows it.
type job struct {
	arm      string // "" outside arms mode
	scenario string // with #n under -repeat
	profile  string
	checks   int // the checks the run will grade
	turns    int
}

// name is the job as the end block heads its entry.
func (j job) name() string {
	if j.arm == "" {
		return j.scenario
	}

	return j.arm + " · " + j.scenario
}

// An entry is what the end block prints for one finished job.
type entry struct {
	rep    Report
	path   string
	folder string
	said   []string
}

// A view owns stdout for a whole eval. Every run is one row of fixed columns.
// Finished rows print into the scrollback. On a terminal, a block under them
// is repainted in place: the running row, its detail frame and the queued
// rows. Urls, commands and check details wait for the end block, so the rows
// stay contiguous. The stream's goroutine and the ticker both reach the view,
// so every method locks.
type view struct {
	mu   sync.Mutex
	w    io.Writer
	st   *styles
	root string
	now  func() time.Time
	size func(io.Writer) (int, int) // read on every paint, so a resize is followed

	jobs    []job
	cols    columns
	headed  bool
	cur     int // the running job, -1 between runs
	started time.Time
	prog    progress
	turnN   int
	prompt  string
	recent  []recentCall // the running job's last tool calls, oldest first
	painted int          // lines the block took on screen

	entries []*entry // by job, nil until it finishes
	said    []string // lines said with no job running

	passed   int
	cost     float64
	grader   float64
	duration time.Duration

	stop chan struct{}
	done chan struct{}
}

// A recentCall is one line of the detail frame: when in the run it was made.
type recentCall struct {
	at   time.Duration
	line string
	sub  bool
}

const (
	tick        = 100 * time.Millisecond
	recentTools = 4
)

func newView(w io.Writer, st *styles, root string, jobs []job) *view {
	v := &view{
		w: w, st: st, root: root, jobs: jobs, cols: newColumns(jobs), cur: -1, now: time.Now, size: termSize,
		entries: make([]*entry, len(jobs)),
	}

	if st.rich {
		v.stop, v.done = make(chan struct{}), make(chan struct{})
		go v.ticker()
	}

	return v
}

func (v *view) ticker() {
	defer close(v.done)

	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		select {
		case <-v.stop:
			return
		case <-t.C:
			v.mu.Lock()
			v.paint()
			v.mu.Unlock()
		}
	}
}

// begin marks job i running. The first one prints the header on a terminal.
func (v *view) begin(i int) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.cur, v.started, v.prog = i, v.now(), progress{}
	v.turnN, v.prompt, v.recent = 0, "", nil

	if v.st.rich && !v.headed {
		v.headed = true
		v.above(v.st.dim.Render(v.header()))

		return
	}

	v.paint()
}

// turn starts a turn of the running job with its prompt.
func (v *view) turn(prompt string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.turnN++
	v.prompt = prompt
	v.prog.beginTurn(prompt)
}

// event folds one stream event into the running job's progress.
func (v *view) event(e event) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.prog.apply(e)

	for _, b := range e.tools() {
		v.recent = append(v.recent, recentCall{at: v.now().Sub(v.started), line: v.prog.toolLine(b), sub: e.subagent()})
	}

	if n := len(v.recent); n > recentTools {
		v.recent = v.recent[n-recentTools:]
	}
}

// say keeps a line for the end block: under the running job's entry, else
// after every entry.
func (v *view) say(line string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.cur >= 0 {
		if v.entries[v.cur] == nil {
			v.entries[v.cur] = &entry{}
		}

		v.entries[v.cur].said = append(v.entries[v.cur].said, line)

		return
	}

	v.said = append(v.said, line)
}

// finish prints job i's row above the block and keeps its entry.
func (v *view) finish(i int, rep Report, path, folder string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if rep.Pass {
		v.passed++
	}

	v.cost += rep.CostUSD
	v.grader += rep.GraderCostUSD
	v.duration += time.Duration(rep.DurationMS) * time.Millisecond
	v.cur = -1

	e := v.entries[i]
	if e == nil {
		e = &entry{}
		v.entries[i] = e
	}

	e.rep, e.path, e.folder = rep, path, folder

	v.above(v.doneRow(v.jobs[i], rep))
}

// close stops the block and prints the end block.
func (v *view) close() {
	if v.stop != nil {
		close(v.stop)
		<-v.done
		v.stop = nil
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	v.clearBlock()

	if lines := v.endBlock(); len(lines) > 0 {
		fmt.Fprintln(v.w)

		for _, l := range lines {
			fmt.Fprintln(v.w, l)
		}
	}
}

// end prints the end block, then tail when set, then the footer.
func (v *view) end(out string, tail func([]Report), reps []Report) {
	v.close()

	if tail != nil {
		tail(reps)
	}

	v.footer(out)
}

// footer prints passes, cost, time and the set's url.
func (v *view) footer(out string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	cost := v.st.cost.Render(fmt.Sprintf("$%.2f", v.cost))
	if v.grader > 0 {
		cost += fmt.Sprintf(" + $%.2f grader", v.grader)
	}

	mark := v.st.mark(v.passed == len(v.jobs))
	fmt.Fprintf(v.w, "\n%s %d/%d passed  %s  %s  %s\n", mark, v.passed, len(v.jobs), cost,
		v.st.timer.Render(v.duration.Round(time.Second).String()), v.link(fileURL(filepath.Join(out, pageFile))))
}

// endBlock is an entry per finished job, then the lines said between runs.
func (v *view) endBlock() []string {
	var lines []string

	for i, e := range v.entries {
		if e == nil {
			continue
		}

		lines = append(lines, v.entryLines(v.jobs[i], e)...)
	}

	return append(lines, v.said...)
}

func (v *view) entryLines(j job, e *entry) []string {
	rep := e.rep
	lines := []string{v.st.mark(rep.Pass) + " " + v.st.title.Render(j.name()) + "  " + v.st.dim.Render(j.profile)}

	if e.folder != "" {
		lines = append(lines, "    "+v.st.dim.Render("report")+"  "+v.link(fileURL(filepath.Join(e.folder, pageFile))))
	}

	if e.path != "" {
		lines = append(lines, "    "+v.st.dim.Render("steps")+"   just eval show "+relToRoot(v.root, e.path))
	}

	for _, c := range rep.Checks {
		if !c.Pass {
			line := "    " + v.st.mark(false) + " " + c.Name
			if c.Detail != "" {
				line += "  " + c.Detail
			}

			lines = append(lines, line)
		}
	}

	if rep.Error != "" {
		lines = append(lines, "    "+v.st.fail.Render("error")+"  "+rep.Error)
	}

	if rep.RunDirError != "" {
		lines = append(lines, "    "+v.st.fail.Render("run folder")+"  "+rep.RunDirError)
	}

	if rep.GraderCostUSD > 0 {
		lines = append(
			lines,
			"    "+v.st.dim.Render("grader")+"  "+v.st.cost.Render(fmt.Sprintf("$%.2f", rep.GraderCostUSD)),
		)
	}

	if s := plumbingShare(rep); s != "" {
		lines = append(lines, "    "+v.st.dim.Render(s))
	}

	for _, l := range e.said {
		lines = append(lines, "    "+l)
	}

	return lines
}

// link is url as an OSC 8 hyperlink on a terminal, else the url alone.
func (v *view) link(target string) string {
	if !v.st.rich {
		return target
	}

	return render.Hyperlink(target, target)
}

// above prints lines where the block was, then paints the block under them.
// On a terminal they are cut to its width, because a wrapped row would break
// the columns it lines up with.
func (v *view) above(lines ...string) {
	v.clearBlock()

	width := 0
	if v.st.rich && v.size != nil {
		width, _ = v.size(v.w)
	}

	for _, l := range lines {
		if width > 0 {
			l = ansi.Truncate(l, width-1, "…")
		}

		fmt.Fprintln(v.w, l)
	}

	v.paint()
}

func (v *view) clearBlock() {
	if v.painted > 0 {
		fmt.Fprintf(v.w, "\x1b[%dA\r\x1b[J", v.painted)
		v.painted = 0
	}
}

// paint redraws the block: the running row and its frame, then the queued rows.
func (v *view) paint() {
	if !v.st.rich {
		return
	}

	width, height := 0, 0
	if v.size != nil {
		width, height = v.size(v.w)
	}

	frame := v.frame(height, width)

	var b strings.Builder

	if v.painted > 0 {
		fmt.Fprintf(&b, "\x1b[%dA\r\x1b[J", v.painted)
	}

	for _, line := range frame {
		if width > 0 {
			// a wrapped line would break the cursor-up count
			line = ansi.Truncate(line, width-1, "…")
		}

		b.WriteString(line + "\n")
	}

	v.painted = len(frame)
	_, _ = io.WriteString(v.w, b.String())
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// frame is the block's lines: the running row, its detail frame, then every
// later job queued.
//
// A block taller than the screen cannot be moved back over, so with a known
// height the queued rows stop short of it and end with a count.
func (v *view) frame(height, width int) []string {
	if v.cur < 0 {
		return nil
	}

	frame := append([]string{v.runningRow()}, v.detail(width)...)

	queued := v.jobs[v.cur+1:]
	more := 0

	if room := height - 2 - len(frame); height > 0 && len(queued) > max(room, 0) {
		keep := max(room-1, 0)
		queued, more = queued[:keep], len(queued)-keep
	}

	for _, q := range queued {
		frame = append(frame, v.queuedRow(q))
	}

	if more > 0 {
		frame = append(frame, v.st.dim.Render(fmt.Sprintf("+%d queued", more)))
	}

	return frame
}

// detail is the running job's frame under its row: the turn and its prompt,
// the last tool calls with when they were made, and the cost so far.
func (v *view) detail(width int) []string {
	const indent = "    "

	var lines []string

	if v.turnN > 0 {
		head := fmt.Sprintf("turn %d/%d", v.turnN, max(v.jobs[v.cur].turns, v.turnN))
		if v.prog.Step != "" {
			head += "  " + v.prog.Step
		}

		prompt, _, _ := strings.Cut(strings.TrimSpace(v.prompt), "\n")
		line := indent + v.st.dim.Render(head) + "  " + prompt

		if width > 0 {
			line = ansi.Truncate(line, width-1, "…")
		}

		lines = append(lines, line)
	}

	for _, c := range v.recent {
		tool := c.line
		if c.sub {
			tool = "sub " + tool
		}

		lines = append(lines, indent+v.st.timer.Render(fmt.Sprintf("%6s", c.at.Round(time.Second)))+"  "+tool)
	}

	return append(lines, indent+v.st.dim.Render("cost")+"  "+v.st.cost.Render(v.liveCost()))
}

// liveCost is the running job's cost so far, ~ marking an estimate in it.
func (v *view) liveCost() string {
	cost, est := v.prog.CostUSD()
	if est {
		return fmt.Sprintf("~$%.2f", cost)
	}

	return fmt.Sprintf("$%.2f", cost)
}

// columns are the widths of a row's variable cells, settled from the whole job
// list before the first run, so no column moves as rows finish. arm is 0
// outside arms mode, which drops the column.
type columns struct {
	arm, scenario, profile, checks int
}

// The fixed cells' widths fit their longest value: "running", "~$123.45",
// "1h59m59s" and the "plumbing" header.
const (
	resultWidth   = 7
	costWidth     = 8
	timeWidth     = 8
	plumbingWidth = 8
	checksFloor   = 20 // room for a failed check's name or two
)

func newColumns(jobs []job) columns {
	c := columns{scenario: len("scenario"), profile: len("profile"), checks: max(len("checks"), checksFloor)}

	for _, j := range jobs {
		if j.arm != "" {
			c.arm = max(c.arm, len("arm"), ansi.StringWidth(j.arm))
		}

		c.scenario = max(c.scenario, ansi.StringWidth(j.scenario))
		c.profile = max(c.profile, ansi.StringWidth(j.profile))
		c.checks = max(c.checks, len(fmt.Sprintf("%d/%d", j.checks, j.checks)))
	}

	return c
}

// cells are one row's values past its name columns, each already styled.
type cells struct {
	glyph, result, checks, cost, time, plumbing string
}

// row lays a job and its cells out in the columns: text left-aligned and cut
// to width, numbers right-aligned so their digits line up.
func (v *view) row(j job, c cells, title bool) string {
	name := j.scenario
	if title {
		name = v.st.title.Render(name)
	}

	parts := []string{c.glyph}

	if v.cols.arm > 0 {
		parts = append(parts, pad(j.arm, v.cols.arm, false))
	}

	parts = append(parts,
		pad(name, v.cols.scenario, false), pad(v.st.dim.Render(j.profile), v.cols.profile, false),
		pad(c.result, resultWidth, false), pad(c.checks, v.cols.checks, false),
		pad(c.cost, costWidth, true), pad(c.time, timeWidth, true), pad(c.plumbing, plumbingWidth, true))

	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

func (v *view) header() string {
	j := job{arm: "arm", scenario: "scenario", profile: "profile"}

	return v.row(
		j,
		cells{glyph: " ", result: "result", checks: "checks", cost: "cost", time: "time", plumbing: "plumbing"},
		false,
	)
}

func (v *view) queuedRow(j job) string {
	return v.row(j, cells{glyph: v.st.dim.Render("·"), result: v.st.dim.Render("queued")}, false)
}

func (v *view) runningRow() string {
	elapsed := v.now().Sub(v.started)

	return v.row(v.jobs[v.cur], cells{
		glyph: v.st.spin.Render(spinner[int(elapsed/tick)%len(spinner)]), result: v.st.spin.Render("running"),
		cost: v.st.cost.Render(v.liveCost()), time: v.st.timer.Render(elapsed.Round(time.Second).String()),
	}, true)
}

func (v *view) doneRow(j job, rep Report) string {
	c := cells{
		glyph: v.st.mark(rep.Pass), result: v.st.ok.Render("pass"), checks: checksCell(rep, v.cols.checks),
		cost: v.st.cost.Render(fmt.Sprintf("$%.2f", rep.CostUSD)),
		time: v.st.timer.Render((time.Duration(rep.DurationMS) * time.Millisecond).Round(time.Second).String()),
	}

	switch {
	case rep.Error != "":
		c.result = v.st.fail.Render("error")
	case !rep.Pass:
		c.result = v.st.fail.Render("FAIL")
	}

	if p := rep.Plumbing; p != nil {
		c.plumbing = v.st.dim.Render(fmt.Sprintf("%.0f%%", p.Share()))
	}

	return v.row(j, c, true)
}

// checksCell is k/N on a pass, else the failed checks' names cut to width.
func checksCell(rep Report, width int) string {
	var failed []string

	for _, c := range rep.Checks {
		if !c.Pass {
			failed = append(failed, c.Name)
		}
	}

	switch {
	case len(rep.Checks) == 0:
		return "-"
	case len(failed) == 0:
		return fmt.Sprintf("%d/%d", len(rep.Checks), len(rep.Checks))
	}

	return ansi.Truncate(strings.Join(failed, ", "), width, "…")
}

// pad cuts s to width display columns and pads it there, by display width
// because styled cells and the · glyph are wider in bytes than on screen.
func pad(s string, width int, right bool) string {
	s = ansi.Truncate(s, width, "…")
	fill := strings.Repeat(" ", width-ansi.StringWidth(s))

	if right {
		return fill + s
	}

	return s + fill
}

// termSize is w's terminal width and height, 0 for either when w is not one.
func termSize(w io.Writer) (int, int) {
	f, ok := w.(*os.File)
	if !ok {
		return 0, 0
	}

	width, height, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0
	}

	return width, height
}

// fileURL is a path's file:// url, its path percent-encoded so a space
// or a # in the checkout's path still opens.
func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// relToRoot is path as typed at the repo root, where `just eval` runs from,
// else the path itself.
func relToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}

	return rel
}

// plumbingShare is a report's plumbing as "plumbing 23% (41s, $0.31)". It is
// empty when the report has none.
func plumbingShare(rep Report) string {
	p := rep.Plumbing
	if p == nil {
		return ""
	}

	return fmt.Sprintf("plumbing %.0f%% (%.0fs, $%.2f)", p.Share(), p.Seconds, p.CostUSD)
}
