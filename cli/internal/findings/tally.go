package findings

import "strings"

// Tally counts one findings file, or one half of it.
type Tally struct {
	Found   int // blocks the round counts: new ones, and prior ones it reopened
	Fixed   int
	Skipped int
	Open    int // waiting on someone: an `ask`, or no verdict yet
	Raised  int // new in this round, a reopened prior one not counted
}

// Word is the verdict's first word, lower-cased: `fix`, `skip`, `ask`, ""
// for none, or text outside that vocabulary, which counts as settled.
func (fd Finding) Word() string {
	word, _, _ := strings.Cut(strings.ToLower(fd.Verdict), " ")

	return word
}

// Counts reports whether the block belongs to its round's tally: a prior
// block only when the round reopened it, since a settled one takes no verdict.
func (fd Finding) Counts() bool { return !fd.Prior || Reopens(fd.Status) }

// Tally counts every block of the file.
func (f *File) Tally() Tally { return f.tally(func(Finding) bool { return true }) }

// TallyHalf counts the blocks the remote half raised, or the local half's.
func (f *File) TallyHalf(remote bool) Tally {
	return f.tally(func(fd Finding) bool { return f.Remote(fd) == remote })
}

func (f *File) tally(in func(Finding) bool) Tally {
	var t Tally

	for _, fd := range f.Findings {
		if !fd.Counts() || !in(fd) {
			continue
		}

		t.Found++

		if !fd.Prior {
			t.Raised++
		}

		switch fd.Word() {
		case "fix":
			t.Fixed++
		case "skip":
			t.Skipped++
		case "ask", "":
			t.Open++
		}
	}

	return t
}
