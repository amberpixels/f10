package gitx

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// realRepo is a repo with main at "base" and side branched from it, so a
// test commits on each and integrates side onto main.
type realRepo struct {
	t   *testing.T
	dir string
}

func newRealRepo(t *testing.T) realRepo {
	t.Helper()

	r := realRepo{t, t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "t@t")
	r.git("config", "user.name", "t")
	r.commit("f", "base\n", "base")
	r.git("branch", "side")

	return r
}

func (r realRepo) git(args ...string) string {
	r.t.Helper()

	out, err := Exec(r.t.Context(), r.dir, args...)
	if err != nil {
		r.t.Fatal(err)
	}

	return out
}

func (r realRepo) commit(file, body, msg string) {
	r.t.Helper()

	if err := os.WriteFile(filepath.Join(r.dir, file), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}

	r.git("add", file)
	r.git("commit", "-qm", msg)
}

func TestIntegrateMergesCleanWithItsMessage(t *testing.T) {
	r := newRealRepo(t)
	r.commit("m", "main\n", "on main")
	r.git("checkout", "-q", "side")
	r.commit("s", "side\n", "on side")

	stopped, err := Integrate(t.Context(), r.dir, "merge", "main", "", "Merge main into side", "Task: FT-1")
	if stopped || err != nil {
		t.Fatalf("a clean merge = %v, %v", stopped, err)
	}

	if got := r.git("log", "-1", "--format=%B"); got != "Merge main into side\n\nTask: FT-1" {
		t.Errorf("merge message = %q", got)
	}
}

func TestIntegrateStopsOnAConflictAndReportsTheRebase(t *testing.T) {
	r := newRealRepo(t)
	r.commit("f", "main\n", "on main")
	r.git("checkout", "-q", "side")
	r.commit("a", "a\n", "first")
	r.commit("f", "side\n", "second, conflicting")
	r.commit("b", "b\n", "third")

	stopped, err := Integrate(t.Context(), r.dir, "rebase", "main", "")
	if !stopped || err != nil {
		t.Fatalf("a conflicting rebase = %v, %v, want stopped", stopped, err)
	}

	op, err := InProgress(t.Context(), r.dir)
	if err != nil {
		t.Fatal(err)
	}

	if op.Kind != "rebase" || op.Done != 2 || op.Total != 3 || op.Onto != r.git("rev-parse", "main") {
		t.Errorf("in progress = %+v, want a rebase onto main at 2 of 3", op)
	}

	if got, err := Unmerged(t.Context(), r.dir); err != nil || !slices.Equal(got, []string{"f"}) {
		t.Errorf("unmerged = %v, %v, want [f]", got, err)
	}
}

func TestIntegrateRefusalIsAnError(t *testing.T) {
	r := newRealRepo(t)

	if stopped, err := Integrate(t.Context(), r.dir, "rebase", "no-such-ref", ""); stopped || err == nil {
		t.Errorf("a bad base = %v, %v, want an error", stopped, err)
	}
}

func TestInProgressReadsAMerge(t *testing.T) {
	r := newRealRepo(t)
	r.commit("f", "main\n", "on main")
	r.git("checkout", "-q", "side")
	r.commit("f", "side\n", "on side")

	if stopped, err := Integrate(t.Context(), r.dir, "merge", "main", ""); !stopped || err != nil {
		t.Fatalf("a conflicting merge = %v, %v", stopped, err)
	}

	op, err := InProgress(t.Context(), r.dir)
	if err != nil || op.Kind != "merge" || op.Onto != r.git("rev-parse", "main") {
		t.Errorf("in progress = %+v, %v, want a merge of main", op, err)
	}

	r.git("merge", "--abort")

	if op, err := InProgress(t.Context(), r.dir); err != nil || op.Kind != "" {
		t.Errorf("after abort = %+v, %v, want none", op, err)
	}
}

func TestMarkersSkipsAHeadingUnderline(t *testing.T) {
	r := newRealRepo(t)
	base := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "side")
	r.commit("doc.md", "Title\n=======\n", "a heading")
	r.commit("left.go", "x\n<<<<<<< HEAD\ny\n", "a marker left")

	got, err := Markers(t.Context(), r.dir, base)
	if err != nil || !slices.Equal(got, []string{"left.go"}) {
		t.Errorf("markers = %v, %v, want [left.go]", got, err)
	}

	r.commit("left.go", "x\ny\n", "settled")

	if got, err := Markers(t.Context(), r.dir, base); err != nil || len(got) != 0 {
		t.Errorf("markers after settling = %v, %v, want none", got, err)
	}
}
