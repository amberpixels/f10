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

// A job is one run of the selection, named as the view prints it.
type job struct {
	name    string // the scenario, with #n under -repeat
	profile string
}

// A view owns stdout for a whole eval. Finished runs print their result lines.
// On a terminal, a block under them is repainted in place with the running
// and the queued runs, and elsewhere only plain lines print. The stream's
// goroutine and the ticker both reach it, so every method locks.
type view struct {
	mu   sync.Mutex
	w    io.Writer
	st   *styles
	root string
	now  func() time.Time
	size func(io.Writer) (int, int) // read on every paint, so a resize is followed

	jobs    []job
	cur     int // the running job, -1 between runs
	started time.Time
	prog    progress
	painted int // lines the block took on screen

	passed   int
	cost     float64
	grader   float64
	duration time.Duration

	stop chan struct{}
	done chan struct{}
}

const tick = 100 * time.Millisecond

func newView(w io.Writer, st *styles, root string, jobs []job) *view {
	v := &view{w: w, st: st, root: root, jobs: jobs, cur: -1, now: time.Now, size: termSize}

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

// begin marks job i running.
func (v *view) begin(i int) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.cur, v.started, v.prog = i, v.now(), progress{}

	if !v.st.rich {
		fmt.Fprintf(v.w, "started  %s  %s\n", v.jobs[i].name, v.jobs[i].profile)
	}

	v.paint()
}

// turn starts a turn of the running job with its prompt.
func (v *view) turn(prompt string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.prog.beginTurn(prompt)
}

// event folds one stream event into the running job's progress.
func (v *view) event(e event) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.prog.apply(e)
}

// say prints a line above the block.
func (v *view) say(line string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.above(line)
}

// finish prints job i's result lines above the block.
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

	v.above(v.resultLines(v.jobs[i], rep, path, folder)...)
}

// close stops the block and prints the footer: passes, cost, time and the
// set's url.
func (v *view) close(out string) {
	if v.stop != nil {
		close(v.stop)
		<-v.done
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	v.clearBlock()

	cost := v.st.cost.Render(fmt.Sprintf("$%.2f", v.cost))
	if v.grader > 0 {
		cost += fmt.Sprintf(" + $%.2f grader", v.grader)
	}

	mark := v.st.mark(v.passed == len(v.jobs))
	fmt.Fprintf(v.w, "\n%s %d/%d passed  %s  %s  %s\n", mark, v.passed, len(v.jobs), cost,
		v.st.timer.Render(v.duration.Round(time.Second).String()), v.link(fileURL(out)))
}

func (v *view) resultLines(j job, rep Report, path, folder string) []string {
	status := v.st.ok.Render("pass")
	if !rep.Pass {
		status = v.st.fail.Render("FAIL")
	}

	cost := v.st.cost.Render(fmt.Sprintf("$%.2f", rep.CostUSD))
	if rep.GraderCostUSD > 0 {
		cost += fmt.Sprintf(" + $%.2f grader", rep.GraderCostUSD)
	}

	lines := []string{fmt.Sprintf("%s %s  %s  %s  %s  %s", v.st.mark(rep.Pass), v.st.title.Render(j.name),
		v.st.dim.Render(j.profile), status, cost,
		v.st.timer.Render((time.Duration(rep.DurationMS) * time.Millisecond).Round(time.Second).String()))}

	if rep.Error != "" {
		lines = append(lines, "    "+v.st.fail.Render("error")+"  "+rep.Error)
	}

	for _, c := range rep.Checks {
		line := "    " + v.st.mark(c.Pass) + " " + c.Name
		if !c.Pass && c.Detail != "" {
			line += "  " + c.Detail
		}

		lines = append(lines, line)
	}

	if folder != "" {
		lines = append(lines, "    "+v.st.dim.Render("report")+"  "+v.link(fileURL(folder)))
	}

	if rep.RunDirError != "" {
		lines = append(lines, "    "+v.st.fail.Render("run folder")+"  "+rep.RunDirError)
	}

	lines = append(lines, "    "+v.st.dim.Render("steps")+"   just eval show "+relToRoot(v.root, path))

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
func (v *view) above(lines ...string) {
	v.clearBlock()

	for _, l := range lines {
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

// paint redraws the block: the running job, then the queued ones.
func (v *view) paint() {
	if !v.st.rich {
		return
	}

	width, height := 0, 0
	if v.size != nil {
		width, height = v.size(v.w)
	}

	frame := v.frame(height)

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

// frame is the block's lines: the running job, then every later one queued.
//
// A block taller than the screen cannot be moved back over, so with a known
// height the queued lines stop short of it and end with a count.
func (v *view) frame(height int) []string {
	if v.cur < 0 {
		return nil
	}

	elapsed := v.now().Sub(v.started)
	j := v.jobs[v.cur]

	cost, est := v.prog.CostUSD()
	money := fmt.Sprintf("$%.2f", cost)

	if est {
		money = "~" + money
	}

	glyph := spinner[int(elapsed/tick)%len(spinner)]
	parts := []string{
		v.st.spin.Render(glyph) + " " + v.st.title.Render(j.name), v.st.dim.Render(j.profile),
		v.st.timer.Render(elapsed.Round(time.Second).String()), v.st.cost.Render(money),
	}

	for _, part := range []string{v.prog.Skill, v.prog.Step} {
		if part != "" {
			parts = append(parts, part)
		}
	}

	if v.prog.Tool != "" {
		tool := v.prog.Tool
		if v.prog.Sub {
			tool = "sub " + tool
		}

		parts = append(parts, v.st.dim.Render(tool))
	}

	frame := []string{strings.Join(parts, "  ")}

	queued := v.jobs[v.cur+1:]
	more := 0

	if room := height - 3; height > 0 && len(queued) > max(room, 0) {
		keep := max(room-1, 0)
		queued, more = queued[:keep], len(queued)-keep
	}

	for _, q := range queued {
		frame = append(frame, fmt.Sprintf("%s %s  %s  %s", v.st.dim.Render("·"), q.name,
			v.st.dim.Render(q.profile), v.st.dim.Render("queued")))
	}

	if more > 0 {
		frame = append(frame, v.st.dim.Render(fmt.Sprintf("+%d queued", more)))
	}

	return frame
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

// fileURL is a directory's file:// url, its path percent-encoded so a space
// or a # in the checkout's path still opens.
func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path) + "/"}).String()
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
