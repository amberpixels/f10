package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func testReport() Report {
	return Report{
		Scenario: "feature-small", Pass: false, CostUSD: 1.5, DurationMS: 90_000, GraderCostUSD: 0.1,
		Checks: []Check{{Name: "plan", Pass: true}, {Name: "accept", Detail: `go run . -n: want "3"`}},
	}
}

func testJob() job { return job{scenario: "feature-small", profile: "opus:high", checks: 2, turns: 1} }

// Off a terminal the view prints one row per finished run, then the end
// block with the run's report, steps, failed check and kept path, then the
// footer. Nothing but the row prints before the end block.
func TestViewPlain(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	v := newView(&buf, &styles{}, "/repo", []job{testJob()})
	v.begin(0)
	v.say("kept: /tmp/f10eval-x")
	v.event(event{Type: "result", CostUSD: 1.5})

	rep := testReport()
	rep.Plumbing = &Plumbing{Calls: 12, TotalCalls: 40, Seconds: 22.5, CostUSD: 0.31, RunSeconds: 90}
	v.finish(0, rep, "/repo/evals/reports/x/feature-small.json", "/repo/evals/reports/x/feature-small")

	row := buf.String()
	if strings.Count(row, "\n") != 1 || !strings.HasPrefix(row, "✗  feature-small  opus:high  FAIL     accept ") ||
		!strings.HasSuffix(row, "$1.50     1m30s       25%\n") {
		t.Errorf("the finished run is not one row: %q", row)
	}

	v.end("/repo/evals/reports/x", nil, nil)

	out := buf.String()
	if !strings.HasPrefix(out, row+"\n✗ feature-small  opus:high\n") {
		t.Errorf("the end block does not follow the row:\n%s", out)
	}

	for _, want := range []string{
		"    report  file:///repo/evals/reports/x/feature-small/index.html\n",
		"    steps   just eval show evals/reports/x/feature-small.json\n",
		"    ✗ accept  go run . -n: want \"3\"\n",
		"    grader  $0.10\n",
		"    plumbing 25% (22s, $0.31)\n",
		"    kept: /tmp/f10eval-x\n",
		"\n✗ 0/1 passed  $1.50 + $0.10 grader  1m30s  file:///repo/evals/reports/x/index.html\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "✓ plan") {
		t.Errorf("the end block lists a passing check:\n%s", out)
	}

	if strings.Contains(out, "\x1b") {
		t.Errorf("plain output holds an escape code: %q", out)
	}
}

// Every state lays out in the same columns: the result starts at one offset,
// and the right-aligned cost and time end at one offset.
func TestViewRowsAlign(t *testing.T) {
	t.Parallel()

	jobs := []job{
		{scenario: "feature-small #1", profile: "opus:high", checks: 3},
		{scenario: "bug #10", profile: "sonnet", checks: 3},
		{scenario: "x", profile: "haiku", checks: 3},
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v := &view{st: &styles{}, jobs: jobs, cols: newColumns(jobs), cur: 1, now: func() time.Time { return at }}
	v.started = at.Add(-75 * time.Second)

	pass := Report{
		Pass: true, CostUSD: 12.25, DurationMS: 3_725_000, Plumbing: &Plumbing{Seconds: 1, RunSeconds: 4},
		Checks: []Check{{Name: "a", Pass: true}, {Name: "b", Pass: true}, {Name: "c", Pass: true}},
	}
	fail := Report{
		CostUSD: 0.4, DurationMS: 9_000, Plumbing: &Plumbing{Seconds: 1, RunSeconds: 10},
		Checks: []Check{{Name: "a", Pass: true}, {Name: "accept"}, {Name: "c", Pass: true}},
	}

	done, failed := v.doneRow(jobs[0], pass), v.doneRow(jobs[2], fail)
	running, queued := v.runningRow(), v.queuedRow(jobs[2])

	col := func(row, word string) int {
		before, _, ok := strings.Cut(row, word)
		if !ok {
			t.Fatalf("%q not in %q", word, row)
		}

		return ansi.StringWidth(before)
	}

	if a, b, c, d := col(done, "pass"), col(failed, "FAIL"), col(running, "running"), col(queued, "queued"); a != b ||
		a != c || a != d {
		t.Errorf("result columns at %d, %d, %d, %d:\n%s\n%s\n%s\n%s", a, b, c, d, done, failed, running, queued)
	}

	if !strings.Contains(done, "3/3") || !strings.Contains(failed, "accept") {
		t.Errorf("checks cells:\n%s\n%s", done, failed)
	}

	if a, b := ansi.StringWidth(done), ansi.StringWidth(failed); a != b {
		t.Errorf("finished rows %d and %d wide:\n%s\n%s", a, b, done, failed)
	}

	// the running row has no plumbing yet, so it ends where the time column does
	if a, b := col(done, "1h2m5s")+len("1h2m5s"), ansi.StringWidth(running); a != b {
		t.Errorf("the time column ends at %d finished, %d running:\n%s\n%s", a, b, done, running)
	}
}

// A failed check's name longer than the column is cut to its width, and the
// arm column shows only in arms mode.
func TestViewRowCutAndArm(t *testing.T) {
	t.Parallel()

	plain := []job{{scenario: "s", profile: "p", checks: 1}}
	v := &view{st: &styles{}, jobs: plain, cols: newColumns(plain)}

	row := v.doneRow(plain[0], Report{Checks: []Check{{Name: strings.Repeat("n", 40)}}})
	if want := strings.Repeat("n", checksFloor-1) + "…"; !strings.Contains(row, want+"  ") {
		t.Errorf("the long name is not cut to %d columns: %q", checksFloor, row)
	}

	if strings.Contains(v.header(), "arm") {
		t.Errorf("a plain set's header has an arm column: %q", v.header())
	}

	arms := []job{{arm: "native", scenario: "s", profile: "p"}, {arm: "f10", scenario: "s", profile: "p"}}
	v = &view{st: &styles{}, jobs: arms, cols: newColumns(arms)}

	if h := v.header(); !strings.HasPrefix(h, "   arm     scenario") {
		t.Errorf("arms header %q", h)
	}

	if q := v.queuedRow(arms[1]); !strings.HasPrefix(q, "·  f10     s ") {
		t.Errorf("arms row %q", q)
	}
}

// On a terminal the block is the running row, its detail frame (the turn,
// the last four tool calls, the cost) and the queued rows, and each repaint
// moves back over the last one.
func TestViewFrame(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jobs := []job{{scenario: "a", profile: "opus:high", turns: 2}, {scenario: "b", profile: "opus:medium"}}
	v := &view{
		w: &buf, st: &styles{rich: true}, cur: -1, now: func() time.Time { return at },
		jobs: jobs, cols: newColumns(jobs), entries: make([]*entry, 2),
	}

	v.begin(0)
	v.turn("/f10:ship FT-1\nmore")

	for i := range 5 {
		at = at.Add(13 * time.Second)
		v.event(event{Type: "assistant", Message: &eventMessage{
			ID: "m", Model: "claude-opus-5-5", Usage: &eventUsage{Input: 1_000_000},
			Content: []eventBlock{{Type: "tool_use", Name: "Read", Input: fmt.Appendf(nil, `{"file_path":"f%d"}`, i)}},
		}})
	}

	frame := v.frame(0, 0)
	want := []string{
		"turn 1/2  /f10:ship FT-1", "26s  Read  f1", "39s  Read  f2", "52s  Read  f3", "1m5s  Read  f4", "cost  ~$4.00",
		"b ",
	}

	if len(frame) != 1+len(want) || !strings.Contains(frame[0], "running") || !strings.Contains(frame[0], "~$4.00") {
		t.Fatalf("frame %q", frame)
	}

	for i, w := range want {
		if !strings.Contains(frame[i+1], w) {
			t.Errorf("frame line %d %q, want %q", i+1, frame[i+1], w)
		}
	}

	if !strings.Contains(buf.String(), "scenario") {
		t.Errorf("the first run prints no header: %q", buf.String())
	}

	v.mu.Lock()
	v.paint()
	buf.Reset()
	v.paint()
	v.mu.Unlock()

	if !strings.Contains(buf.String(), fmt.Sprintf("\x1b[%dA\r\x1b[J", len(frame))) {
		t.Errorf("the repaint does not move back over the block: %q", buf.String())
	}
}

// The end block lists every finished run in order, then the lines said
// between runs, and a tail prints between it and the footer.
func TestViewEndBlockOrder(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	jobs := []job{
		{arm: "f10", scenario: "a", profile: "p", checks: 1},
		{arm: "raw", scenario: "b", profile: "p", checks: 1},
	}
	v := newView(&buf, &styles{}, "/repo", jobs)

	v.begin(0)
	v.finish(0, Report{Pass: true, Checks: []Check{{Name: "plan", Pass: true}}}, "/repo/r/a.json", "")
	v.begin(1)
	v.say("page: disk full")
	v.finish(1, Report{Error: "claude: exit 1"}, "/repo/r/b.json", "/repo/r/b")
	v.say("index page: no space")
	v.end("/repo/r", func([]Report) { fmt.Fprintln(&buf, "TABLE") }, nil)

	out := buf.String()
	order := []string{
		"✓  f10  a ", "✗  raw  b ", "\n✓ f10 · a  p\n    steps   just eval show r/a.json\n",
		"✗ raw · b  p\n    report  file:///repo/r/b/index.html\n    steps   just eval show r/b.json\n" +
			"    error  claude: exit 1\n    page: disk full\n",
		"index page: no space\n", "TABLE\n", "1/2 passed",
	}
	at := 0

	for _, w := range order {
		i := strings.Index(out[at:], w)
		if i < 0 {
			t.Fatalf("output misses %q after offset %d:\n%s", w, at, out)
		}

		at += i + len(w)
	}

	if !strings.Contains(out, "✗  raw  b         p        error    -  ") {
		t.Errorf("an errored run's row: %q", out)
	}
}

func TestRelToRoot(t *testing.T) {
	t.Parallel()

	if got := relToRoot("/repo", "/repo/evals/reports/x/a.json"); got != "evals/reports/x/a.json" {
		t.Errorf("inside: %q", got)
	}

	if got := relToRoot("/repo", "/tmp/set/a.json"); got != "/tmp/set/a.json" {
		t.Errorf("outside: %q", got)
	}
}

// A block taller than the screen could not be moved back over, so the queued
// rows stop short of the height, counting the detail frame, and end with a
// count.
func TestViewFrameCapsQueued(t *testing.T) {
	t.Parallel()

	jobs := make([]job, 50)
	for i := range jobs {
		jobs[i] = job{scenario: "s", profile: "p"}
	}

	v := &view{st: &styles{}, jobs: jobs, cols: newColumns(jobs), cur: 0, now: time.Now}

	frame := v.frame(10, 0)
	// the running row, its cost line, five queued and the count: 8 lines
	if len(frame) != 8 || frame[len(frame)-1] != "+44 queued" {
		t.Errorf("frame %d lines, last %q, want 8 ending in +44 queued", len(frame), frame[len(frame)-1])
	}

	if got := len(v.frame(0, 0)); got != 51 {
		t.Errorf("an unknown height caps nothing: %d lines", got)
	}
}

func TestFileURLEncodes(t *testing.T) {
	t.Parallel()

	if got := fileURL("/Users/x/My Evals/#1"); got != "file:///Users/x/My%20Evals/%231" {
		t.Errorf("fileURL %q", got)
	}
}

type fakeTerminal struct{ bytes.Buffer }

func (*fakeTerminal) Terminal() bool { return true }

// newStyles is plain for a pipe and under NO_COLOR, and colored only on a
// terminal without it.
//
//nolint:paralleltest // t.Setenv
func TestNewStyles(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	if newStyles(&bytes.Buffer{}).rich {
		t.Error("a pipe got rich styles")
	}

	if !newStyles(&fakeTerminal{}).rich {
		t.Error("a terminal without NO_COLOR got plain styles")
	}

	t.Setenv("NO_COLOR", "1")

	st := newStyles(&fakeTerminal{})
	if st.rich || strings.Contains(st.mark(false), "\x1b") {
		t.Error("NO_COLOR on a terminal still colors")
	}
}
