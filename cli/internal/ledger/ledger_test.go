package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tempPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "f10.db")
	t.Setenv(PathEnv, path)

	return path
}

func openT(t *testing.T, path string) *Ledger {
	t.Helper()

	l, err := Open(context.Background(), path, BusyAsync, true)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	return l
}

func list(t *testing.T, path string, f Filter) []Event {
	t.Helper()

	l, err := OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	defer l.Close()

	events, err := l.List(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}

	return events
}

func TestAppendReadsBack(t *testing.T) {
	path := tempPath(t)
	l := openT(t, path)
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.FixedZone("x", 3600))

	l.Append(
		context.Background(),
		Event{
			At:      at,
			Session: "s1",
			Task:    "FT-1",
			Kind:    StepEntered,
			Source:  Observed,
			Payload: col(Payload{"steps": []any{"implement"}}),
		},
		Event{Session: "s2", Kind: RunStarted, Source: Observed},
	)

	got := list(t, path, Filter{})
	if len(got) != 2 {
		t.Fatalf("got %d events", len(got))
	}

	e := got[0]
	if e.ID == "" || e.Machine == "" || e.Kind != StepEntered || e.Task != "FT-1" || !e.At.Equal(at) {
		t.Errorf("first event %+v", e)
	}

	if steps, _ := e.Payload.Val["steps"].([]any); len(steps) != 1 || steps[0] != "implement" {
		t.Errorf("payload %v", e.Payload.Val)
	}

	if s2 := list(t, path, Filter{Session: "s2"}); len(s2) != 1 || s2[0].Kind != RunStarted {
		t.Errorf("filtered by session: %+v", s2)
	}

	if machine := MachineID(filepath.Dir(path)); got[1].Machine != machine {
		t.Errorf("machine %q, file holds %q", got[1].Machine, machine)
	}
}

// An id already stored is left as it was: rows are never rewritten.
func TestAKnownIDIsNotRewritten(t *testing.T) {
	path := tempPath(t)
	l := openT(t, path)

	l.Append(context.Background(), Event{ID: "01A", Kind: RunStarted, Source: Observed})
	l.Append(context.Background(), Event{ID: "01A", Kind: Pushed, Source: Claimed})

	got := list(t, path, Filter{})
	if len(got) != 1 || got[0].Kind != RunStarted || got[0].Source != Observed {
		t.Errorf("got %+v", got)
	}
}

// Two writers, each with its own connection pool, as two `f10 drive` agents
// would be: every event lands.
func TestParallelWritersLoseNothing(t *testing.T) {
	path := tempPath(t)
	openT(t, path) // migrate once, as any earlier run would have

	const writers, each = 2, 200

	var wg sync.WaitGroup

	for w := range writers {
		wg.Go(func() {
			for i := range each {
				Record(context.Background(), BusyAsync, Event{
					Session: fmt.Sprintf("w%d", w), Kind: Committed, Source: Observed,
					Payload: col(Payload{"i": i}),
				})
			}
		})
	}

	wg.Wait()

	got := list(t, path, Filter{})
	if spooled, _ := os.ReadFile(spoolPath(path)); len(spooled) > 0 {
		openT(t, path) // a spooled event is not lost: the next open replays it
		got = list(t, path, Filter{})
	}

	if len(got) != writers*each {
		t.Errorf("got %d events, want %d", len(got), writers*each)
	}
}

func TestUnwritableDatabaseSpoolsAndTheNextOpenReplays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f10.db")
	t.Setenv(PathEnv, path)

	// a directory where the database should be: every open fails
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatal(err)
	}

	Record(context.Background(), BusyAsync,
		Event{Session: "s1", Kind: RunStarted, Source: Observed},
		Event{Session: "s1", Kind: StateSet, Source: Claimed})

	raw, err := os.ReadFile(spoolPath(path))
	if err != nil || len(raw) == 0 {
		t.Fatalf("nothing spooled: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	openT(t, path).Close()

	got := list(t, path, Filter{Session: "s1"})
	if len(got) != 2 || got[0].Kind != RunStarted || got[1].Kind != StateSet {
		t.Errorf("replayed %+v", got)
	}

	if _, err := os.Stat(spoolPath(path)); !os.IsNotExist(err) {
		t.Errorf("spool left behind: %v", err)
	}

	// the same lines again, as a replay interrupted half way would leave
	if err := os.WriteFile(spoolPath(path), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	openT(t, path).Close()

	if got := list(t, path, Filter{}); len(got) != 2 {
		t.Errorf("a second replay duplicated rows: %d", len(got))
	}
}

func TestDumpOfNoDatabaseIsNotExist(t *testing.T) {
	if _, err := OpenRead(context.Background(), filepath.Join(t.TempDir(), "f10.db")); !os.IsNotExist(unwrapAll(err)) {
		t.Errorf("err = %v", err)
	}
}

func unwrapAll(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok || u.Unwrap() == nil {
			return err
		}

		err = u.Unwrap()
	}
}

func TestMachineIDIsStable(t *testing.T) {
	dir := t.TempDir()

	a, b := MachineID(dir), MachineID(dir)
	if a == "" || a != b {
		t.Errorf("ids %q, %q", a, b)
	}
}

func TestNormalizeRemote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"git@github.com:a/b.git", "github.com/a/b"},
		{"https://github.com/a/b", "github.com/a/b"},
		{"https://github.com/a/b.git", "github.com/a/b"},
		{"https://user@GitHub.com/a/b/", "github.com/a/b"},
		{"ssh://git@github.com/a/b.git", "github.com/a/b"},
		{"ssh://git@gitlab.example.com:2222/g/sub/r.git", "gitlab.example.com/g/sub/r"},
		{"git@gitlab.com:/g/r.git", "gitlab.com/g/r"},
		{"/srv/git/r.git", "/srv/git/r.git"},
		{"file:///srv/git/r.git", "/srv/git/r.git"},
		{"", ""},
	} {
		if got := NormalizeRemote(tc.in); got != tc.want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A claim another opener holds is left alone while fresh, taken over once
// stale; a claim stamps the time it was made, so a spool that sat for a
// while is not stale the moment it is claimed.
func TestReplayTakesOverOnlyStaleClaims(t *testing.T) {
	path := tempPath(t)

	line := func(id string) []byte {
		events := []Event{{ID: id, Kind: RunStarted, Source: Observed}}
		fill(path, events)

		raw, err := json.Marshal(events[0])
		if err != nil {
			t.Fatal(err)
		}

		return append(raw, '\n')
	}

	old := time.Now().Add(-time.Hour)
	live, dead := spoolPath(path)+".111.0", spoolPath(path)+".222.0"

	for name, id := range map[string]string{live: "01LIVE", dead: "01DEAD"} {
		if err := os.WriteFile(name, line(id), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Chtimes(dead, old, old); err != nil {
		t.Fatal(err)
	}

	openT(t, path).Close()

	got := list(t, path, Filter{})
	if len(got) != 1 || got[0].ID != "01DEAD" {
		t.Errorf("replayed %+v", got)
	}

	if _, err := os.Stat(live); err != nil {
		t.Errorf("a live claim was taken: %v", err)
	}

	// the main spool, last appended an hour ago, claimed by this opener
	if err := os.WriteFile(spoolPath(path), line("01MAIN"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(spoolPath(path), old, old); err != nil {
		t.Fatal(err)
	}

	l := openT(t, path)
	claims, _ := filepath.Glob(spoolPath(path) + ".*")

	for _, c := range claims {
		if fi, err := os.Stat(c); err == nil && c != live && time.Since(fi.ModTime()) > staleClaim {
			t.Errorf("claim %s reads as stale", c)
		}
	}

	_ = l.Close()
}

// A synchronous hook opens without replaying: the spool waits for the next
// async writer.
func TestOpenWithoutReplayLeavesTheSpool(t *testing.T) {
	path := tempPath(t)
	spool(path, []Event{{ID: "01SPOOL", Kind: RunStarted, Source: Observed}})

	l, err := Open(context.Background(), path, BusySync, false)
	if err != nil {
		t.Fatal(err)
	}

	_ = l.Close()

	if _, err := os.Stat(spoolPath(path)); err != nil {
		t.Errorf("spool gone: %v", err)
	}

	openT(t, path).Close()

	if got := list(t, path, Filter{}); len(got) != 1 {
		t.Errorf("replayed %d", len(got))
	}
}
