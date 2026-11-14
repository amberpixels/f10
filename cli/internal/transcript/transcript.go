// Package transcript reads token usage out of a Claude Code session
// transcript: one JSON object per line, appended as the session runs. Only
// counts, model ids and entry ids leave it, never message text.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// Tokens is one API usage, or a sum of them.
type Tokens struct {
	Input      int
	Output     int
	CacheWrite int
	CacheRead  int
}

// Zero reports no tokens at all.
func (t Tokens) Zero() bool { return t == Tokens{} }

func (t Tokens) plus(u Tokens) Tokens {
	return Tokens{t.Input + u.Input, t.Output + u.Output, t.CacheWrite + u.CacheWrite, t.CacheRead + u.CacheRead}
}

// Usage is what a read counted: the session's own messages, by model, and
// its subagents' (sidechain) messages apart.
type Usage struct {
	Tokens

	ByModel   map[string]Tokens
	Subagents Tokens
	// Model is the last model the session's own messages ran on.
	Model string
	// Through is the uuid of the last entry counted in the main file.
	Through string
}

// Zero reports a read that counted nothing.
func (u Usage) Zero() bool { return u.Tokens.Zero() && u.Subagents.Zero() }

// Position is how far each file has been read: the byte offset after the
// last complete line, and the message id counted last, since one API message
// spans several lines that each repeat its usage.
type Position struct {
	Offsets map[string]int64  `json:"offsets"`
	LastIDs map[string]string `json:"last_ids,omitempty"`
}

// Start is the position at the current end of the main file and of every
// subagent file, so a read from it counts only what is written later.
func Start(path string) (Position, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Position{}, fmt.Errorf("transcript: %w", err)
	}

	pos := Position{Offsets: map[string]int64{path: fi.Size()}}

	subs, _ := filepath.Glob(filepath.Join(SubagentDir(path), "*.jsonl"))
	for _, sub := range subs {
		if fi, err := os.Stat(sub); err == nil {
			pos.Offsets[sub] = fi.Size()
		}
	}

	return pos, nil
}

// SubagentDir is where Claude Code writes a session's subagent transcripts.
func SubagentDir(path string) string {
	return filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
}

// Read counts the usage written to the transcript at path, and to its
// subagent files, since from. A missing main file is an error; an
// undecodable line is skipped, and a partial last line is left for the
// next read.
func Read(path string, from Position) (Usage, Position, error) {
	next := Position{Offsets: map[string]int64{}, LastIDs: map[string]string{}}
	maps.Copy(next.Offsets, from.Offsets)
	maps.Copy(next.LastIDs, from.LastIDs)

	var u Usage

	if err := readFile(path, &next, &u, false); err != nil {
		return Usage{}, from, err
	}

	subs, _ := filepath.Glob(filepath.Join(SubagentDir(path), "*.jsonl"))
	for _, sub := range subs {
		_ = readFile(sub, &next, &u, true)
	}

	return u, next, nil
}

// entry is the part of a transcript line Read needs.
type entry struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	Sidechain bool   `json:"isSidechain"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Input      int `json:"input_tokens"`
			Output     int `json:"output_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// assistantMark prefilters lines, so only assistant lines are decoded.
var assistantMark = []byte(`"assistant"`)

func readFile(path string, pos *Position, u *Usage, sidechain bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("transcript: %w", err)
	}
	defer f.Close()

	off := pos.Offsets[path]
	if fi, err := f.Stat(); err == nil && fi.Size() < off {
		off = 0 // rewritten: the last id still guards against a recount
	}

	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return fmt.Errorf("transcript: %w", err)
	}

	r := bufio.NewReaderSize(f, 64<<10)
	seen := map[string]bool{}

	if id := pos.LastIDs[path]; id != "" {
		seen[id] = true
	}

	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break // a line without its newline is still being written
		}

		if err != nil {
			return fmt.Errorf("transcript: %w", err)
		}

		off += int64(len(line))

		if !bytes.Contains(line, assistantMark) {
			continue
		}

		var e entry
		if json.Unmarshal(line, &e) != nil || e.Type != "assistant" {
			continue
		}

		if id := e.Message.ID; id != "" {
			if seen[id] {
				continue
			}

			seen[id] = true
			pos.LastIDs[path] = id
		}

		count(u, &e, sidechain)
	}

	pos.Offsets[path] = off

	return nil
}

func count(u *Usage, e *entry, sidechain bool) {
	t := Tokens{
		Input:      e.Message.Usage.Input,
		Output:     e.Message.Usage.Output,
		CacheWrite: e.Message.Usage.CacheWrite,
		CacheRead:  e.Message.Usage.CacheRead,
	}

	if sidechain || e.Sidechain {
		u.Subagents = u.Subagents.plus(t)

		return
	}

	u.Tokens = u.plus(t)
	u.Through = e.UUID

	if !realModel(e.Message.Model) {
		return
	}

	u.Model = e.Message.Model

	if u.ByModel == nil {
		u.ByModel = map[string]Tokens{}
	}

	u.ByModel[e.Message.Model] = u.ByModel[e.Message.Model].plus(t)
}

// tailLimit bounds LastModel's backward reads: a session whose last
// assistant line is further back than this reports no model.
const tailLimit = 4 << 20

// LastModel is the model of the transcript's last main-thread assistant
// line, "" when none is within its last few MiB.
func LastModel(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return ""
	}

	for n := int64(64 << 10); ; n *= 4 {
		start := max(fi.Size()-n, 0)

		buf := make([]byte, fi.Size()-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return ""
		}

		if start > 0 { // the first line is cut
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			}
		}

		if m := lastModelIn(buf); m != "" {
			return m
		}

		if start == 0 || n >= tailLimit {
			return ""
		}
	}
}

func lastModelIn(buf []byte) string {
	for len(buf) > 0 {
		i := bytes.LastIndexByte(bytes.TrimRight(buf, "\n"), '\n')
		line := buf[i+1:]
		buf = buf[:max(i, 0)]

		if !bytes.Contains(line, assistantMark) {
			continue
		}

		var e entry
		if json.Unmarshal(line, &e) == nil && e.Type == "assistant" && !e.Sidechain && realModel(e.Message.Model) {
			return e.Message.Model
		}
	}

	return ""
}

// realModel leaves out the placeholder Claude Code writes on a message no
// model produced, such as an API error.
func realModel(m string) bool { return m != "" && m != "<synthetic>" }
