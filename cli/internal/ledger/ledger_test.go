package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/hook"
	"github.com/amberpixels/f10/cli/internal/observe"
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
				Record(Event{
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

	Record(Event{Session: "s1", Kind: RunStarted, Source: Observed},
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

func TestRefsRememberAndOverwrite(t *testing.T) {
	tempPath(t)
	ctx := context.Background()

	RememberRefs(ctx, "github.com/a/b",
		TaskRef{ID: "FT-1", Title: "One", URL: "https://x/1"},
		TaskRef{ID: "FT-2", Title: "Two"},
		TaskRef{ID: "FT-3"}, // no title: not stored
	)
	RememberRefs(ctx, "github.com/a/b", TaskRef{ID: "FT-1", Title: "One renamed"}) // a title-only read keeps the url
	RememberRefs(ctx, "github.com/c/d", TaskRef{ID: "FT-1", Title: "Other project"})

	got := LookupRefs(ctx, "github.com/a/b", []string{"FT-1", "FT-2", "FT-3", "FT-9"})
	if len(got) != 2 {
		t.Fatalf("rows = %v, want FT-1 and FT-2", got)
	}

	if r := got["FT-1"]; r.Title != "One renamed" || r.URL != "https://x/1" || r.SeenAt.IsZero() {
		t.Errorf("FT-1 = %+v", r)
	}

	if r := got["FT-2"]; r.Title != "Two" || r.URL != "" {
		t.Errorf("FT-2 = %+v", r)
	}

	if r := LookupRefs(ctx, "github.com/c/d", []string{"FT-1"})["FT-1"]; r.Title != "Other project" {
		t.Errorf("the other project's FT-1 = %+v", r)
	}
}

func TestLookupRefsKnowsNoneWithoutTheTable(t *testing.T) {
	path := tempPath(t)
	ctx := context.Background()

	if got := LookupRefs(ctx, "p", []string{"FT-1"}); len(got) != 0 {
		t.Errorf("missing database: %v", got)
	}

	// a database from before the table existed
	l := openT(t, path)
	if _, err := l.db.ExecContext(ctx, "DROP TABLE task_refs"); err != nil {
		t.Fatal(err)
	}

	if got := LookupRefs(ctx, "p", []string{"FT-1"}); len(got) != 0 {
		t.Errorf("missing table: %v", got)
	}
}

func userVersion(t *testing.T, l *Ledger) int {
	t.Helper()

	var v int
	if err := l.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}

	return v
}

func tableExists(t *testing.T, l *Ledger, name string) bool {
	t.Helper()

	var n int

	err := l.db.QueryRowContext(context.Background(),
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}

	return n > 0
}

func migrationCount(t *testing.T) int {
	t.Helper()

	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations: %v", err)
	}

	return len(files)
}

// A fresh open migrates and stamps user_version; an open of a stamped
// database skips goose, so its version table is never recreated.
func TestMigrateRunsOnlyWhenTheSchemaMoved(t *testing.T) {
	path := tempPath(t)
	ctx := context.Background()

	l := openT(t, path)
	if v := userVersion(t, l); v != migrationCount(t) {
		t.Fatalf("user_version %d after a fresh open", v)
	}

	if _, err := l.db.ExecContext(ctx, "DROP TABLE goose_db_version"); err != nil {
		t.Fatal(err)
	}

	_ = l.Close()

	l = openT(t, path)
	if tableExists(t, l, "goose_db_version") {
		t.Error("a current database ran goose again")
	}

	// a database stamped below the count migrates, here from scratch
	for _, stmt := range []string{"DROP TABLE events", "DROP TABLE task_refs", "PRAGMA user_version = 1"} {
		if _, err := l.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	_ = l.Close()

	l = openT(t, path)
	if !tableExists(t, l, "events") || !tableExists(t, l, "task_refs") || userVersion(t, l) != migrationCount(t) {
		t.Error("a lagging database did not migrate")
	}
}

// A database from before the stamp (version 0) migrates once and is stamped.
func TestMigrateStampsAnUnversionedDatabase(t *testing.T) {
	path := tempPath(t)

	l := openT(t, path)
	if _, err := l.db.ExecContext(context.Background(), "PRAGMA user_version = 0"); err != nil {
		t.Fatal(err)
	}

	_ = l.Close()

	if v := userVersion(t, openT(t, path)); v != migrationCount(t) {
		t.Errorf("user_version %d", v)
	}
}

func observeT(t *testing.T, ob *Observer, o observe.Observation, session string) {
	t.Helper()

	observe.Stamp(o, session, t.TempDir(), time.Now())

	if err := ob.Observe(context.Background(), o); err != nil {
		t.Fatal(err)
	}
}

// A hook's rows go to the spool without opening the database; Sync moves
// them in once, and a commit outside any run is dropped there.
func TestObservedRowsSpoolAndSyncMovesThemIn(t *testing.T) {
	path := tempPath(t)
	ob := &Observer{StateDir: t.TempDir(), Busy: BusyAsync}
	defer ob.Close()

	observeT(t, ob, &observe.SkillStarted{Skill: "ship"}, "s1")
	observeT(t, ob, &observe.Committed{SHA: "abc1234", Subject: "x"}, "s1")
	observeT(t, ob, &observe.Committed{SHA: "def5678", Subject: "y"}, "s2") // no run in s2

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an observation opened the database: %v", err)
	}

	if raw, _ := os.ReadFile(spoolPath(path)); strings.Count(string(raw), "\n") != 3 {
		t.Fatalf("spool:\n%s", raw)
	}

	for range 2 {
		if err := Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	got := list(t, path, Filter{})
	if len(got) != 2 || got[0].Kind != RunStarted || got[1].Kind != Committed || got[1].Session != "s1" {
		t.Errorf("synced %+v", got)
	}

	// s2's commit waits in the spool for a run start that may still come
	if raw, _ := os.ReadFile(
		spoolPath(path),
	); !strings.Contains(string(raw), "def5678") ||
		strings.Count(string(raw), "\n") != 1 {
		t.Errorf("spool after sync:\n%s", raw)
	}
}

// A gated row replayed before its run's start, as leftovers appended behind
// newer rows put it, is kept and lands once the start does; one with no
// start past the wait is dropped.
func TestGatedRowWaitsForItsRunStart(t *testing.T) {
	path := tempPath(t)
	commit := Event{Kind: Committed, Session: "s1", Source: Observed}
	stale := Event{Kind: Committed, Session: "s9", Source: Observed, At: time.Now().Add(-2 * gatedWait)}

	events := []Event{commit, stale}
	fill(path, events)
	spoolAs(path, events, true)

	if err := Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := list(t, path, Filter{}); len(got) != 0 {
		t.Fatalf("a commit landed before its run start: %+v", got)
	}

	Record(Event{Kind: RunStarted, Session: "s1", Source: Observed})

	if err := Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := list(t, path, Filter{})
	if len(got) != 2 || got[0].Kind != RunStarted && got[1].Kind != RunStarted {
		t.Errorf("synced %+v, want the run start and its commit", got)
	}

	if pending(path) {
		t.Error("the stale commit outside any run was kept")
	}
}

// The per-tool hooks never open SQLite: a run's start and its commit are
// spooled, a plain command and a prompt leave nothing, and Sync lands both.
func TestHotPathHooksNeverOpenTheDatabase(t *testing.T) {
	path := tempPath(t)
	env := &hook.Env{StateDir: t.TempDir(), Home: t.TempDir()}

	for _, name := range []string{"pre-skill", "post-bash-other", "prompt-submit", "post-bash-commit"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "hooks", name+".json"))
		if err != nil {
			t.Fatal(err)
		}

		ob := &Observer{StateDir: env.StateDir, Busy: BusyAsync}
		env.Observers = []observe.Observer{ob}
		hook.Dispatch(context.Background(), raw, env)
		ob.Close()

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s opened the database: %v", name, err)
		}
	}

	if err := Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	var kinds []string
	for _, e := range list(t, path, Filter{Session: "bench"}) {
		kinds = append(kinds, e.Kind)
	}

	if want := []string{RunStarted, Committed}; !slices.Equal(kinds, want) {
		t.Errorf("rows %q, want %q", kinds, want)
	}
}

// An observation that yields no row creates nothing.
func TestAnObservationWithNoRowCreatesNoDatabase(t *testing.T) {
	path := tempPath(t)
	ob := &Observer{StateDir: t.TempDir(), Busy: BusyAsync}
	defer ob.Close()

	observeT(t, ob, &observe.Committed{SHA: "abc1234"}, "") // no session: never a run's

	for _, p := range []string{path, spoolPath(path)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists: %v", p, err)
		}
	}

	if err := Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a sync with nothing spooled created the database: %v", err)
	}
}
