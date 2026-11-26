package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func cannedStream(t *testing.T) []byte {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", "stream", "feature-small.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	return body
}

// The reader keeps every line, an unknown event and a non-JSON one
// included, and takes the turn's result from the result event.
func TestReadStream(t *testing.T) {
	t.Parallel()

	body := cannedStream(t)
	dir := t.TempDir()

	tr, err := openTranscript(dir)
	if err != nil {
		t.Fatal(err)
	}

	var (
		prog  progress
		skill []string
		steps []string
		tools []string
	)

	res, got, err := readStream(strings.NewReader(string(body)), tr, func(e event) {
		prog.apply(e)
		skill, steps, tools = append(skill, prog.Skill), append(steps, prog.Step), append(tools, prog.Tool)
	})
	if err != nil || !got {
		t.Fatalf("read: got %v, err %v", got, err)
	}

	if err := tr.close(); err != nil {
		t.Fatal(err)
	}

	// two turns in one stream: the last result is the one returned
	if res.SessionID != "s1" || res.CostUSD != 0.25 || res.Result != "Shipped." {
		t.Errorf("result %+v", res)
	}

	if !slices.Contains(skill, "/f10:plan") || skill[len(skill)-1] != "/f10:ship" {
		t.Errorf("skills %q", skill)
	}

	if !slices.Contains(steps, "plan") {
		t.Errorf("steps %q, want the badge's phase", steps)
	}

	if !slices.Contains(tools, "Read  main.go") || !slices.Contains(tools, `Grep  flag\.Bool`) {
		t.Errorf("tools %q", tools)
	}

	if prog.Tools != 6 {
		t.Errorf("%d tool calls, want 6", prog.Tools)
	}

	if cost, est := prog.CostUSD(); cost != 0.75 || est {
		t.Errorf("cost %v estimated %v, want the two results' exact sum", cost, est)
	}

	kept, _ := os.ReadFile(filepath.Join(dir, transcriptFile))
	if !bytes.Equal(kept, body) {
		t.Error("the transcript is not the stream line for line")
	}

	times, _ := os.ReadFile(filepath.Join(dir, timesFile))
	if n, want := strings.Count(string(times), "\n"), strings.Count(string(body), "\n"); n != want {
		t.Errorf("%d times for %d lines", n, want)
	}
}

func TestReadStreamWithoutResult(t *testing.T) {
	t.Parallel()

	_, got, err := readStream(strings.NewReader(`{"type":"system"}`+"\n"), nil, nil)
	if got || err != nil {
		t.Errorf("got %v err %v, want no result and no read error", got, err)
	}
}

// The running estimate counts each message once, though claude prints a
// message once per content block, and a result replaces it with the exact
// figure.
func TestProgressEstimate(t *testing.T) {
	t.Parallel()

	u := &eventUsage{Input: 1_000_000}
	msg := event{Type: "assistant", Message: &eventMessage{ID: "m", Model: "claude-opus-5-5", Usage: u}}

	var p progress

	p.apply(msg)
	p.apply(msg)

	if cost, est := p.CostUSD(); cost != 4 || !est {
		t.Errorf("estimate %v %v, want $4 for a million opus input tokens, once", cost, est)
	}

	p.apply(event{Type: "result", CostUSD: 3.5})

	if cost, est := p.CostUSD(); cost != 3.5 || est {
		t.Errorf("after the result %v %v, want the exact figure", cost, est)
	}
}

func TestStepOf(t *testing.T) {
	t.Parallel()

	for cmd, want := range map[string]string{
		"f10-state.sh set ship running implement; f10-load-project.sh implement": "implement",
		"f10-state.sh set plan running":                                          "plan",
		"f10-state.sh set ship running 'pr[pr-polish]'":                          "pr[pr-polish]",
		"f10-load-project.sh fetch investigate plan":                             "fetch investigate plan",
		"go test ./...": "",
	} {
		if got := stepOf(cmd); got != want {
			t.Errorf("stepOf(%q) = %q, want %q", cmd, got, want)
		}
	}
}
