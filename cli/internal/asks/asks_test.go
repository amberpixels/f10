package asks

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := []struct {
		text string
		want []Question
	}{
		{
			text: "1. Reuse the saved plan? [reuse | re-plan] 2. Base GH-7 has no code yet: [wait | proceed on its plan]",
			want: []Question{
				{N: 1, Text: "Reuse the saved plan?", Options: []string{"reuse", "re-plan"}},
				{N: 2, Text: "Base GH-7 has no code yet:", Options: []string{"wait", "proceed on its plan"}},
			},
		},
		{
			text: "1. Which port should it listen on? 2. Merge now? [yes | no]",
			want: []Question{
				{N: 1, Text: "Which port should it listen on?"},
				{N: 2, Text: "Merge now?", Options: []string{"yes", "no"}},
			},
		},
		{
			// a number inside a question is not the next question
			text: "1. Keep v1. 2 clients use it? [keep | drop]",
			want: []Question{{N: 1, Text: "Keep v1. 2 clients use it?", Options: []string{"keep", "drop"}}},
		},
		{
			text: "Should the drive merge #204 now?",
			want: []Question{{N: 1, Text: "Should the drive merge #204 now?"}},
		},
	}

	for _, c := range cases {
		got := Parse(c.text)
		if !slices.EqualFunc(got, c.want, func(a, b Question) bool {
			return a.N == b.N && a.Text == b.Text && slices.Equal(a.Options, b.Options)
		}) {
			t.Errorf("Parse(%q) = %+v, want %+v", c.text, got, c.want)
		}
	}
}

func TestResolveAndFormat(t *testing.T) {
	q := Question{N: 1, Text: "Reuse?", Options: []string{"reuse", "re-plan"}}

	for _, v := range []string{"b", "2", "Re-Plan"} {
		if got, err := Resolve(q, v); err != nil || got != "re-plan" {
			t.Errorf("Resolve(%q) = %q, %v; want re-plan", v, got, err)
		}
	}

	if _, err := Resolve(q, "maybe"); err == nil {
		t.Error("free text answered a question with options")
	}

	free := Question{N: 2, Text: "Which port?"}
	if got, _ := Resolve(free, "8080"); got != "8080" {
		t.Errorf("free text = %q", got)
	}

	if got := Format([]Question{q, free}, map[int]string{2: "8080"}); got != "1. reuse 2. 8080" {
		t.Errorf("Format = %q", got)
	}
}

func sample(root string) Ask {
	return Ask{Task: "FT-1", Root: root, Worktree: "/w", Skill: "review", Text: "1. Merge? [yes | no]"}
}

func TestRaiseOnce(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)

	a, fresh, err := Raise(dir, sample("/r"), t0)
	if err != nil || !fresh || !a.AskedAt.Equal(t0) {
		t.Fatalf("first raise = %v, %v, %v", a.AskedAt, fresh, err)
	}

	// the drive re-raising the same ask names itself and tells nobody
	again := sample("/r")
	again.Drive = 3

	a, fresh, err = Raise(dir, again, t0.Add(time.Minute))
	if err != nil || fresh || !a.AskedAt.Equal(t0) || a.Drive != 3 {
		t.Fatalf("second raise = %+v, fresh %v, %v", a, fresh, err)
	}

	changed := sample("/r")
	changed.Text = "1. Merge now? [yes | wait]"

	if _, fresh, _ := Raise(dir, changed, t0.Add(2*time.Minute)); !fresh {
		t.Error("a new question was not a new ask")
	}
}

func TestClaimFirstWins(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	a, _, err := Raise(dir, sample("/r"), now)
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		won  int
		lost []error
	)

	for _, by := range []string{ByPane, ByAsks, BySkill, ByDrive} {
		wg.Go(func() {
			_, err := Claim(dir, a.Key(), by, "1. yes", by == ByPane, now)

			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				won++
			} else {
				lost = append(lost, err)
			}
		})
	}

	wg.Wait()

	if won != 1 || len(lost) != 3 {
		t.Fatalf("won %d, lost %v", won, lost)
	}

	for _, err := range lost {
		var c ClosedError
		if !errors.As(err, &c) || c.By == "" {
			t.Errorf("loser told %v, want who won", err)
		}
	}

	if open, _ := Open(dir); len(open) != 0 {
		t.Errorf("open after claim: %+v", open)
	}

	got, isOpen, err := Get(dir, a.Key())
	if err != nil || isOpen || got.Answer != "1. yes" {
		t.Errorf("Get = %+v, open %v, %v", got, isOpen, err)
	}

	// re-raising an answered, untaken ask does not reopen it
	if _, fresh, _ := Raise(dir, sample("/r"), now); fresh {
		t.Error("an answered ask was raised again")
	}

	if open, _ := Open(dir); len(open) != 0 {
		t.Errorf("answered ask reopened: %+v", open)
	}
}

func TestUntakenAndPrune(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-8 * 24 * time.Hour)

	for i, root := range []string{"/a", "/b"} {
		a := sample(root)
		if _, _, err := Raise(dir, a, old); err != nil {
			t.Fatal(err)
		}

		at := old
		if i == 1 {
			at = time.Now()
		}

		if _, err := Claim(dir, a.Key(), ByAsks, "1. yes", false, at); err != nil {
			t.Fatal(err)
		}
	}

	if got, _ := Untaken(dir, "/a"); len(got) != 1 || got[0].Root != "/a" {
		t.Errorf("Untaken(/a) = %+v", got)
	}

	gone := sample("/c")
	gone.Worktree = filepath.Join(dir, "removed")

	here := sample("/e")
	here.Worktree = dir

	for _, a := range []Ask{gone, here} {
		if _, _, err := Raise(dir, a, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	Prune(dir, time.Now())

	left, _ := os.ReadDir(filepath.Join(dir, answeredDir))
	if len(left) != 1 {
		t.Errorf("after prune %d answered, want 1", len(left))
	}

	// an ask whose worktree is gone has nobody left to answer it
	if open, _ := Open(dir); len(open) != 1 || open[0].Root != "/e" {
		t.Errorf("open after prune = %+v", open)
	}
}

// A drive-less ask answered where the agent heard it is done: the same
// question asked again is a new ask, told again. One a drive will take
// stays the same ask until the drive takes it.
func TestRaiseAfterAnswer(t *testing.T) {
	dir, now := t.TempDir(), time.Now()

	a, _, _ := Raise(dir, sample("/r"), now)
	if _, err := Claim(dir, a.Key(), ByPane, "1. yes", true, now); err != nil {
		t.Fatal(err)
	}

	if _, fresh, _ := Raise(dir, sample("/r"), now.Add(time.Minute)); !fresh {
		t.Error("a re-ask after a heard answer was swallowed")
	}

	if open, _ := Open(dir); len(open) != 1 {
		t.Fatalf("open = %+v", open)
	}

	driven := sample("/d")
	driven.Drive = 2

	b, _, _ := Raise(dir, driven, now)
	if _, err := Claim(dir, b.Key(), ByPane, "1. yes", true, now); err != nil {
		t.Fatal(err)
	}

	if _, fresh, _ := Raise(dir, driven, now.Add(time.Minute)); fresh {
		t.Error("a drive's answered ask was raised again before the drive took it")
	}
}

// A raise merging into an open ask while claims race never reopens an
// answered one.
func TestRaiseMergeNeverReopens(t *testing.T) {
	for range 20 {
		dir, now := t.TempDir(), time.Now()

		a, _, _ := Raise(dir, sample("/r"), now)
		withDrive := sample("/r")
		withDrive.Drive = 5

		var wg sync.WaitGroup

		wg.Go(func() { _, _, _ = Raise(dir, withDrive, now) })
		wg.Go(func() { _, _ = Claim(dir, a.Key(), ByAsks, "1. no", false, now) })
		wg.Wait()

		_, isOpen, err := Get(dir, a.Key())
		if err != nil || isOpen {
			t.Fatalf("after a claim the ask reads open %v, %v", isOpen, err)
		}
	}
}
