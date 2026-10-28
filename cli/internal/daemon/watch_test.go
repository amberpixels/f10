//go:build unix

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// passScript is a pass that logs a line with $MARK to the project's
// passes file and exits with the code its code file holds, 0 without one.
func passScript(string) (string, []string) {
	return "/bin/sh", []string{"-c", `echo "pass $MARK" >> passes; exit "$(cat code 2>/dev/null || echo 0)"`}
}

func passes(t *testing.T, project string) []string {
	t.Helper()

	raw, _ := os.ReadFile(filepath.Join(project, "passes"))

	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(raw)), "pass ", "pass_"))
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// A watched project gets a pass at once and again every WatchEvery, keeps
// the daemon from idling out, and is dropped once a pass exits 7.
func TestWatchRunsPassesUntilNothingIsLeft(t *testing.T) {
	project := t.TempDir()
	s := &Server{
		Dir: runDir(t), Version: "v1", Child: shell, Pass: passScript,
		WatchEvery: 30 * time.Millisecond, Tick: 10 * time.Millisecond, Idle: 50 * time.Millisecond,
	}
	done := serve(t, s)

	rep, err := Call(t.Context(), s.Dir, Request{
		Op: OpWatch, Root: "/r", Dir: project, Env: []string{"PATH=" + os.Getenv("PATH"), "MARK=a"},
	}, 0)
	if err != nil || rep.Log == "" {
		t.Fatalf("watch: %+v, %v", rep, err)
	}

	waitFor(t, "a second pass", func() bool { return len(passes(t, project)) >= 2 })

	if got := passes(t, project)[0]; got != "pass_a" {
		t.Errorf("the pass ran with %q, want the client's env", got)
	}

	status, err := Call(t.Context(), s.Dir, Request{Op: OpStatus}, 0)
	if err != nil || len(status.Watches) != 1 || status.Watches[0].Dir != project {
		t.Errorf("status = %+v, %v", status.Watches, err)
	}

	select {
	case <-done:
		t.Fatal("the daemon idled out while it watched a project")
	case <-time.After(150 * time.Millisecond):
	}

	if err := os.WriteFile(filepath.Join(project, "code"), []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon kept a project with nothing left to watch")
	}

	raw, _ := os.ReadFile(watchPath(s.Dir))
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Errorf("watch list = %s, want empty", raw)
	}
}

// The watch list outlives the daemon: the next one goes on with the same
// projects and environment, no client needed.
func TestWatchListSurvivesARestart(t *testing.T) {
	dir, project := runDir(t), t.TempDir()

	first := &Server{Dir: dir, Version: "v1", Child: shell, Pass: passScript, WatchEvery: time.Hour}
	firstDone := serve(t, first)

	if _, err := Call(t.Context(), dir, Request{
		Op: OpWatch, Root: "/r", Dir: project, Env: []string{"PATH=" + os.Getenv("PATH"), "MARK=b"},
	}, 0); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the first pass", func() bool { return len(passes(t, project)) == 1 })

	if _, err := Call(t.Context(), dir, Request{Op: OpStop}, 0); err != nil {
		t.Fatal(err)
	}

	<-firstDone

	if fi, err := os.Stat(watchPath(dir)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("watch list: %v, %v", fi, err)
	}

	second := &Server{
		Dir:        dir,
		Version:    "v1",
		Child:      shell,
		Pass:       passScript,
		WatchEvery: time.Nanosecond,
		Tick:       10 * time.Millisecond,
	}
	serve(t, second)

	waitFor(t, "a pass from the next daemon", func() bool { return len(passes(t, project)) >= 2 })

	if got := passes(t, project); got[1] != "pass_b" {
		t.Errorf("passes = %v, want the kept env", got)
	}
}
