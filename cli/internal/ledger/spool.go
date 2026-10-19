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

// staleClaim is how old a claimed spool must be before another opener takes
// it over: its replayer died mid-way.
const staleClaim = time.Minute

func spoolPath(dbPath string) string { return filepath.Join(filepath.Dir(dbPath), spoolName) }

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

// spool appends events as JSON lines in one write, so parallel spoolers'
// lines never interleave.
func spool(dbPath string, events []Event) {
	var buf bytes.Buffer

	for _, e := range events {
		line, err := json.Marshal(e)
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
	claims := []string{main}

	if stale, err := filepath.Glob(main + ".*"); err == nil {
		for _, s := range stale {
			if fi, err := os.Stat(s); err == nil && time.Since(fi.ModTime()) > staleClaim {
				claims = append(claims, s)
			}
		}
	}

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

		l.replayFile(ctx, claimed)
	}
}

func (l *Ledger) replayFile(ctx context.Context, claimed string) {
	f, err := os.Open(claimed)
	if err != nil {
		return
	}

	var left bytes.Buffer

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.ID == "" {
			continue // a torn line carries nothing recoverable
		}

		if left.Len() > 0 || l.insert(ctx, e) != nil {
			left.WriteString(line)
			left.WriteByte('\n')
		}
	}

	_ = f.Close()

	if appendFile(spoolPath(l.path), left.Bytes()) == nil {
		_ = os.Remove(claimed)
	}
}
