package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// show prints the canned run's turns, skills and tool calls in order, an
// errored call marked, from the run folder beside the report.
func TestShow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	folder := filepath.Join(dir, "feature-small")

	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatal(err)
	}

	body := cannedStream(t)
	n := strings.Count(string(body), "\n")

	var times strings.Builder
	for i := range n {
		times.WriteString(strconv.Itoa(i*1000) + "\n")
	}

	for name, content := range map[string]string{
		transcriptFile:         string(body),
		timesFile:              times.String(),
		"inputs/scenario.json": `{"prompts":["/f10:plan EV-1 --driven","/f10:ship EV-1"]}`,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(folder, name)), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(folder, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rep, _ := json.Marshal(
		Report{Scenario: "feature-small", Pass: true, CostUSD: 0.75, DurationMS: 90_000, RunDir: "feature-small"},
	)
	if err := os.WriteFile(filepath.Join(dir, "feature-small.json"), rep, 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := showMain(&buf, "/", []string{filepath.Join(dir, "feature-small.json")}); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	order := []string{
		"✓ feature-small  $0.75  1m30s",
		"turn 1",
		"  /f10:plan",
		"0:01  ~$0.05  Skill  f10:plan",
		"Bash  f10-state.sh set plan running; f10 task read EV-1",
		"Read  main.go  ✗",
		`sub Grep  flag\.Bool`,
		"  end  $0.50  1m0s  Plan saved.",
		"turn 2",
		"  /f10:ship",
		"Edit  main.go",
		"  end  $0.25  30s  Shipped.",
	}

	// the prompt names /f10:plan and so does the Skill call: one heading
	if n := strings.Count(out, "/f10:plan\n"); n != 1 {
		t.Errorf("%d /f10:plan headings, want 1:\n%s", n, out)
	}

	at := 0
	for _, want := range order {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("output misses %q after byte %d:\n%s", want, at, out)
		}

		at += i + len(want)
	}
}

func TestShowWithoutRunDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(path, []byte(`{"scenario":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := showMain(&bytes.Buffer{}, "/", []string{path})
	if err == nil || !strings.Contains(err.Error(), "predates run folders") {
		t.Errorf("err %v", err)
	}
}
