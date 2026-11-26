package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func testReport() Report {
	return Report{
		Scenario: "feature-small", Pass: false, CostUSD: 1.5, DurationMS: 90_000, GraderCostUSD: 0.1,
		Checks: []Check{{Name: "plan", Pass: true}, {Name: "accept", Detail: `go run . -n: want "3"`}},
	}
}

// Off a terminal the view prints plain lines: a started line, the result
// with its checks, report and steps lines, and the footer.
func TestViewPlain(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	v := newView(&buf, &styles{}, "/repo", []job{{name: "feature-small", profile: "opus:high"}})
	v.begin(0)
	v.event(event{Type: "result", CostUSD: 1.5})
	v.finish(0, testReport(), "/repo/evals/reports/x/feature-small.json", "/repo/evals/reports/x/feature-small")
	v.close("/repo/evals/reports/x")

	out := buf.String()

	for _, want := range []string{
		"started  feature-small  opus:high",
		"✗ feature-small  opus:high  FAIL  $1.50 + $0.10 grader  1m30s",
		"    ✓ plan\n",
		`    ✗ accept  go run . -n: want "3"`,
		"    report  file:///repo/evals/reports/x/feature-small/index.html",
		"    steps   just eval show evals/reports/x/feature-small.json",
		"✗ 0/1 passed  $1.50 + $0.10 grader  1m30s  file:///repo/evals/reports/x/index.html",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "\x1b") {
		t.Errorf("plain output holds an escape code: %q", out)
	}
}

// On a terminal the block shows the running job with its progress and the
// rest queued, and each repaint moves back over the last one.
func TestViewFrame(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v := &view{
		w: &buf, st: &styles{rich: true}, cur: -1, now: func() time.Time { return at },
		jobs: []job{{name: "a", profile: "opus:high"}, {name: "b", profile: "opus:medium"}},
	}

	v.begin(0)

	at = at.Add(65 * time.Second)
	v.event(event{Type: "assistant", Message: &eventMessage{
		ID: "m", Model: "claude-opus-5-5", Usage: &eventUsage{Input: 1_000_000},
		Content: []eventBlock{{Type: "tool_use", Name: "Skill", Input: []byte(`{"skill":"f10:ship"}`)}},
	}})

	frame := v.frame()
	if len(frame) != 2 || !strings.Contains(frame[0], "a  opus:high  1m5s  ~$4.00  /f10:ship  Skill  f10:ship") ||
		!strings.Contains(frame[1], "b  opus:medium  queued") {
		t.Errorf("frame %q", frame)
	}

	v.mu.Lock()
	v.paint()
	v.mu.Unlock()

	if !strings.Contains(buf.String(), "\x1b[2A\r\x1b[J") {
		t.Errorf("the repaint does not move back over the block: %q", buf.String())
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
