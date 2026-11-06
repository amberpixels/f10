package state

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// Two writers changing different fields of one session never lose each
// other's change: the hooks and f10 state run in separate processes.
func TestUpdateKeepsConcurrentChanges(t *testing.T) {
	dir := t.TempDir()

	const rounds = 200

	var wg sync.WaitGroup

	for _, field := range []string{"note", "next"} {
		wg.Go(func() {
			for i := range rounds {
				writeField(t, dir, field, strconv.Itoa(i))
			}
		})
	}

	wg.Wait()

	r, err := Load(Path(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}

	if want := strconv.Itoa(rounds - 1); r.Note != want || r.Next != want {
		t.Errorf("note %q next %q, want both %q: a writer saved over the other", r.Note, r.Next, want)
	}
}

func writeField(t *testing.T, dir, field, value string) {
	err := Update(dir, "s", 5*time.Second, time.Now(), func(r *Run, _ bool) (bool, error) {
		if field == "note" {
			r.Note = value
		} else {
			r.Next = value
		}

		return true, nil
	})
	if err != nil {
		t.Error(err)
	}
}
