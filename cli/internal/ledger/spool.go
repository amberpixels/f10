package ledger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

const spoolName = "ledger.spool"

// gatedWait is how long a gated row with no run start in the database is
// kept for a later replay before it counts as outside any run.
const gatedWait = 10 * time.Minute

// staleClaim is how old a claimed spool must be before another opener takes
// it over: its replayer died mid-way.
const staleClaim = time.Minute

func spoolPath(dbPath string) string { return filepath.Join(filepath.Dir(dbPath), spoolName) }

// pending reports a spool or a claim beside the database: anything a
// replay would insert.
func pending(dbPath string) bool {
	main := spoolPath(dbPath)
	if _, err := os.Stat(main); err == nil {
		return true
	}

	claims, _ := filepath.Glob(main + ".*")

	return len(claims) > 0
}

// spooled is a spool line. Gated marks a row kept only for a session the
// database knows started a run: replay checks what the hook could not
// without opening it.
type spooled struct {
	Event

	Gated bool `json:"gated,omitempty"`
}

// fill mints what an event needs before it is stored or spooled, so a
// replayed event keeps its id and a replay retried never duplicates a row.
func fill(dbPath string, events []Event) {
	machine := MachineID(filepath.Dir(dbPath))

	for i := range events {
		e := &events[i]
		if e.ID == "" {
			e.ID = ulid.Make().String()
		}

		if e.Machine == "" {
			e.Machine = machine
		}

		if e.At.IsZero() {
			e.At = time.Now()
		}

		e.At = e.At.UTC()

		if e.Payload.Val == nil {
			e.Payload.Val = Payload{}
		}
	}
}

// spool appends events as JSON lines in one O_APPEND write, so parallel
// spoolers' lines never interleave.
func spool(dbPath string, events []Event) { spoolAs(dbPath, events, false) }

func spoolAs(dbPath string, events []Event, gated bool) {
	var buf bytes.Buffer

	for _, e := range events {
		line, err := json.Marshal(spooled{Event: e, Gated: gated})
		if err != nil {
			continue
		}

		buf.Write(line)
		buf.WriteByte('\n')
	}

	_ = appendFile(spoolPath(dbPath), buf.Bytes())
}

func appendFile(path string, data []byte) error {
	if len(data) == 0 {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()

		return fmt.Errorf("ledger: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	return nil
}

// replay claims the spool by renaming it, so a racing opener finds nothing,
// and inserts its events. Claims a dead replayer left behind are taken over
// once stale. What fails to insert goes back to the spool.
func (l *Ledger) replay(ctx context.Context) {
	main := spoolPath(l.path)

	// stale claims first: their rows predate the main spool's, and a gated
	// row needs its run's start replayed before it
	var claims []string

	if stale, err := filepath.Glob(main + ".*"); err == nil {
		for _, s := range stale {
			if fi, err := os.Stat(s); err == nil && time.Since(fi.ModTime()) > staleClaim {
				claims = append(claims, s)
			}
		}
	}

	claims = append(claims, main)
	runs := map[string]bool{}

	for i, src := range claims {
		// one name per source, so a claim kept after a failed replay is never
		// renamed over
		claimed := fmt.Sprintf("%s.%d.%d", main, os.Getpid(), i)

		// rename keeps the mtime, so stamp it first: on a claim it means
		// claimed at, and a live claim never reads as stale
		now := time.Now()
		_ = os.Chtimes(src, now, now)

		if err := os.Rename(src, claimed); err != nil {
			continue // nothing there, or another opener took it
		}

		l.replayFile(ctx, claimed, runs)
	}
}

// replayFile inserts a claimed file's rows; runs caches, per session,
// whether a run started, for the gated rows.
func (l *Ledger) replayFile(ctx context.Context, claimed string, runs map[string]bool) {
	f, err := os.Open(claimed)
	if err != nil {
		return
	}

	var (
		left bytes.Buffer
		held []spooled
		raw  []string
	)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var e spooled
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.ID == "" {
			continue // a torn line carries nothing recoverable
		}

		// a commit or a PR outside any f10 run is dropped, but only once it is
		// old: its run's start may sit behind it in leftovers or in another
		// process's live claim
		if left.Len() == 0 && e.Gated && !l.runStarted(ctx, e.Session, runs) {
			held, raw = append(held, e), append(raw, line)

			continue
		}

		if left.Len() > 0 || l.insert(ctx, e.Event) != nil {
			left.WriteString(line)
			left.WriteByte('\n')

			continue
		}

		if e.Kind == RunStarted {
			runs[e.Session] = true
		}
	}

	_ = f.Close()

	// a start later in the same file lets the rows held above it in
	for i, e := range held {
		switch {
		case runs[e.Session] && left.Len() == 0 && l.insert(ctx, e.Event) == nil:
		case runs[e.Session] || time.Since(e.At) < gatedWait:
			left.WriteString(raw[i])
			left.WriteByte('\n')
		}
	}

	if appendFile(spoolPath(l.path), left.Bytes()) == nil {
		_ = os.Remove(claimed)
	}
}

// runStarted reports whether the database holds a run start for session.
// A failed count keeps the row: a spooled event can be ignored later, a
// dropped one cannot be recovered.
func (l *Ledger) runStarted(ctx context.Context, session string, runs map[string]bool) bool {
	if session == "" {
		return false
	}

	if ok, seen := runs[session]; seen {
		return ok
	}

	n, err := l.Count(ctx, Filter{Session: session, Kind: RunStarted})
	if err != nil {
		return true
	}

	runs[session] = n > 0

	return n > 0
}
