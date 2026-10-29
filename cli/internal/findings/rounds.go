package findings

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LastRound is the highest round a task gets: round two exists so fixes get
// read, and a third reviewer would only repeat it.
const LastRound = 2

// Decline is why no next round is allowed.
type Decline string

const (
	// Unsettled is round one holding findings resolve has not answered: a
	// round two then could only say `still open`.
	Unsettled Decline = "unsettled"
	// Exhausted is the last round already written.
	Exhausted Decline = "exhausted"
)

// Status is a findings directory read for the next review: the latest round
// and the next one, or the decline that stops it.
type Status struct {
	Dir     string  `json:"dir"`
	Latest  string  `json:"latest,omitempty"` // the highest round's file; empty with none
	Round   int     `json:"round,omitempty"`  // the next round; 0 on a decline
	Path    string  `json:"path,omitempty"`   // the next round's file
	Decline Decline `json:"decline,omitempty"`
	Open    []Open  `json:"open,omitempty"` // the findings behind the decline
}

// Open is a finding a decline names, with why it is still open: `no
// verdict`, `ask`, or the prior status that reopened it.
type Open struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Why   string `json:"why"`
}

// Rounds reads the round files in paths, in round order (`<n>.md` sorted by
// n), all under dir. A file that does not parse is an error naming it.
func Rounds(dir string, paths []string) (Status, error) {
	st := Status{Dir: dir}

	files := make([]*File, 0, len(paths))
	n := 0

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return st, err
		}

		f, err := Parse(string(raw))
		if err != nil {
			return st, fmt.Errorf("%s: %w", path, err)
		}

		files = append(files, f)

		if i, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(path), ".md")); err == nil {
			n = i
		}

		st.Latest = path
	}

	switch {
	case n >= LastRound:
		st.Decline, st.Open = Exhausted, lastOpen(files)
	case n == 1 && len(unanswered(files[len(files)-1])) > 0:
		st.Decline, st.Open = Unsettled, unanswered(files[len(files)-1])
	default:
		st.Round = n + 1
		st.Path = filepath.Join(dir, strconv.Itoa(st.Round)+".md")
	}

	return st, nil
}

func unanswered(f *File) []Open {
	var open []Open

	for _, fd := range f.Findings {
		if fd.Verdict == "" {
			open = append(open, Open{ID: fd.ID, Title: fd.Title, Why: "no verdict"})
		}
	}

	return open
}

// lastOpen is every finding whose last block, the latest file's naming its
// id, waits on someone: an `ask`, or no verdict where one is due. A fix or
// skip settles a reopened finding too; its status only says why it is open.
func lastOpen(files []*File) []Open {
	last := map[string]Finding{}

	var order []string

	for _, f := range files {
		for _, fd := range f.Findings {
			if _, seen := last[fd.ID]; !seen {
				order = append(order, fd.ID)
			}

			last[fd.ID] = fd
		}
	}

	var open []Open

	for _, id := range order {
		fd := last[id]

		verdict, _, _ := strings.Cut(strings.ToLower(fd.Verdict), " ")

		switch {
		case verdict == "ask":
			open = append(open, Open{ID: id, Title: fd.Title, Why: "ask"})
		case fd.Verdict != "":
		case !fd.Prior:
			open = append(open, Open{ID: id, Title: fd.Title, Why: "no verdict"})
		// a prior block `fixed` or `explanation accepted` takes no verdict
		case Reopens(fd.Status):
			open = append(open, Open{ID: id, Title: fd.Title, Why: fd.Status})
		}
	}

	return open
}
