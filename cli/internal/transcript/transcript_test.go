package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture copies testdata's session and its subagent file into a temp dir,
// returning the main file's path and its lines.
func fixture(t *testing.T) (string, []string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	raw, err := os.ReadFile(filepath.Join("testdata", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	sub, err := os.ReadFile(filepath.Join("testdata", "session", "subagents", "agent-a1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(SubagentDir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(SubagentDir(path), "agent-a1.jsonl"), sub, 0o600); err != nil {
		t.Fatal(err)
	}

	lines := strings.SplitAfter(string(raw), "\n")

	return path, lines[:len(lines)-1] // the text ends in a newline
}

// naive sums a file the simple way, each message id once, as the check
// Read's incremental sums must agree with.
func naive(t *testing.T, path string) (Tokens, Tokens) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var main, side Tokens

	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)

	for sc.Scan() {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "assistant" || seen[e.Message.ID] {
			continue
		}

		seen[e.Message.ID] = true
		tk := Tokens{
			e.Message.Usage.Input,
			e.Message.Usage.Output,
			e.Message.Usage.CacheWrite,
			e.Message.Usage.CacheRead,
		}

		if e.Sidechain {
			side = side.plus(tk)
		} else {
			main = main.plus(tk)
		}
	}

	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	return main, side
}

func TestReadsInCutsSumToTheTranscriptsTotals(t *testing.T) {
	path, lines := fixture(t)

	var (
		sum  Usage
		pos  Position
		cuts = []int{2, 6, len(lines)} // the first cut splits one message's lines
	)

	written := 0
	for _, cut := range cuts {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		_, _ = f.WriteString(strings.Join(lines[written:cut], ""))
		_ = f.Close()
		written = cut

		u, next, err := Read(path, pos)
		if err != nil {
			t.Fatal(err)
		}

		sum.Tokens = sum.plus(u.Tokens)
		sum.Subagents = sum.Subagents.plus(u.Subagents)
		pos = next
	}

	main, side := naive(t, path)
	subMain, subSide := naive(t, filepath.Join(SubagentDir(path), "agent-a1.jsonl"))
	side = side.plus(subMain).plus(subSide)

	if sum.Tokens != main {
		t.Errorf("main thread summed %+v, transcript holds %+v", sum.Tokens, main)
	}

	if sum.Subagents != side {
		t.Errorf("subagents summed %+v, transcripts hold %+v", sum.Subagents, side)
	}

	want := Tokens{Input: 11, Output: 660, CacheWrite: 1200, CacheRead: 7200}
	if sum.Tokens != want {
		t.Errorf("main thread %+v, want %+v", sum.Tokens, want)
	}
}

func TestReadSplitsByModelAndSkipsSynthetic(t *testing.T) {
	path, _ := fixture(t)
	if err := os.WriteFile(path, mustRead(t, filepath.Join("testdata", "session.jsonl")), 0o600); err != nil {
		t.Fatal(err)
	}

	u, _, err := Read(path, Position{})
	if err != nil {
		t.Fatal(err)
	}

	if u.Model != "claude-opus-5-5" || u.Through != "a7" {
		t.Errorf("model %q through %q", u.Model, u.Through)
	}

	if len(u.ByModel) != 2 || u.ByModel["claude-sonnet-5-5"].Output != 150 ||
		u.ByModel["claude-opus-5-5"].Output != 510 {
		t.Errorf("by model %+v", u.ByModel)
	}
}

func TestReadLeavesAPartialLineForLater(t *testing.T) {
	path, lines := fixture(t)
	last := lines[len(lines)-1]

	if err := os.WriteFile(
		path,
		[]byte(strings.Join(lines[:len(lines)-1], "")+strings.TrimSuffix(last, "\n")),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	u1, pos, err := Read(path, Position{})
	if err != nil {
		t.Fatal(err)
	}

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("\n")
	_ = f.Close()

	u2, _, err := Read(path, pos)
	if err != nil {
		t.Fatal(err)
	}

	if u1.Output+u2.Output != 660 || u2.Output != 10 {
		t.Errorf("first read %d, second %d output tokens", u1.Output, u2.Output)
	}
}

func TestReadOfAMissingOrCorruptFile(t *testing.T) {
	dir := t.TempDir()

	if _, _, err := Read(filepath.Join(dir, "none.jsonl"), Position{}); err == nil {
		t.Error("a missing transcript read without an error")
	}

	bad := filepath.Join(dir, "bad.jsonl")
	_ = os.WriteFile(bad, []byte("{\"type\":\"assistant\"\nnot json\n\x00\x01\n"), 0o600)

	u, _, err := Read(bad, Position{})
	if err != nil || !u.Zero() {
		t.Errorf("a corrupt transcript read %+v, %v", u, err)
	}

	if m := LastModel(bad); m != "" {
		t.Errorf("a corrupt transcript's model %q", m)
	}
}

func TestLastModelSkipsSidechains(t *testing.T) {
	path, lines := fixture(t)
	_ = os.WriteFile(path, []byte(strings.Join(lines[:6], "")), 0o600) // ends on a sidechain line

	if m := LastModel(path); m != "claude-sonnet-5-5" {
		t.Errorf("last model %q", m)
	}
}

// bigTranscript writes a transcript of about size bytes of distinct turns.
func bigTranscript(t testing.TB, path string, size int) {
	t.Helper()

	var b bytes.Buffer

	pad := strings.Repeat("y", 4000)
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, `{"type":"user","uuid":"u%d","message":{"content":%q}}`+"\n", i, pad)
		fmt.Fprintf(
			&b,
			`{"type":"assistant","uuid":"a%d","message":{"id":"m%d","model":"m","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n",
			i,
			i,
		)
	}

	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

const turn = `{"type":"assistant","uuid":"last","message":{"id":"m-last","model":"m","usage":{"input_tokens":5,"output_tokens":7}}}` + "\n"

// A second read starts at the saved offset: the file's earlier bytes,
// rewritten with turns the first read never saw, are not read again.
func TestASecondReadReadsOnlyTheNewTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	bigTranscript(t, path, 20<<20)

	_, pos, err := Read(path, Position{})
	if err != nil {
		t.Fatal(err)
	}

	raw := mustRead(t, path)
	raw = bytes.ReplaceAll(raw, []byte(`"id":"m`), []byte(`"id":"x`))
	raw = append(raw, turn...)
	_ = os.WriteFile(path, raw, 0o600)

	start := time.Now()

	u, _, err := Read(path, pos)
	if err != nil {
		t.Fatal(err)
	}

	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("second read took %v", took)
	}

	if u.Tokens != (Tokens{Input: 5, Output: 7}) || u.Through != "last" {
		t.Errorf("second read counted %+v through %q", u.Tokens, u.Through)
	}
}

func BenchmarkSecondRead(b *testing.B) {
	path := filepath.Join(b.TempDir(), "s.jsonl")
	bigTranscript(b, path, 20<<20)

	pos, err := Start(path)
	if err != nil {
		b.Fatal(err)
	}

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(turn)
	_ = f.Close()

	for b.Loop() {
		if _, _, err := Read(path, pos); err != nil {
			b.Fatal(err)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}
