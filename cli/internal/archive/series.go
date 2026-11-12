package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/taskid"
)

// A SeriesEnd is what an archived series records in its closed file: the PR
// (#204, or none), the commit it ended at, and when. A restart is rounds
// ended by a rebase before any PR existed: still the same PR, so it counts
// for no lore change.
type SeriesEnd struct {
	PR      int // 0 for none
	Head    string
	At      time.Time
	Restart bool
}

func (e SeriesEnd) Write(dir string) error {
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}

	pr := "none"
	if e.PR > 0 {
		pr = strconv.Itoa(e.PR)
	}

	body := fmt.Sprintf("pr: %s\nhead: %s\nclosed: %s\n", pr, e.Head, at.UTC().Format(time.RFC3339))
	if e.Restart {
		body += "restart: yes\n"
	}

	if err := os.WriteFile(filepath.Join(dir, seriesClosedFile), []byte(body), 0o600); err != nil {
		return fmt.Errorf("recording the end of the review series at %s: %w", dir, err)
	}

	return nil
}

// Same compares by PR number, else by head.
func (e SeriesEnd) Same(o SeriesEnd) bool {
	if e.PR > 0 && e.PR == o.PR {
		return true
	}

	return e.Head != "" && e.Head == o.Head
}

const seriesClosedFile = "closed"

// ReadSeriesEnd is zero for a series archived before the file existed.
func ReadSeriesEnd(dir string) SeriesEnd {
	raw, err := os.ReadFile(filepath.Join(dir, seriesClosedFile))
	if err != nil {
		return SeriesEnd{}
	}

	var e SeriesEnd

	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, _ := strings.Cut(line, ":")

		switch value = strings.TrimSpace(value); key {
		case "pr":
			e.PR = taskid.RequestNumber(value) // #7 or !7 in a file older than the bare number
		case "head":
			e.Head = value
		case "closed":
			e.At, _ = time.Parse(time.RFC3339, value)
		case "restart":
			e.Restart = value == "yes"
		}
	}

	return e
}

// seriesName is <id>.<n>, the name of an archived series and of a later
// PR's lore change alike.
func seriesName(id string, n int) string { return id + "." + strconv.Itoa(n) }

// seriesNumber is n from a name seriesName wrote for id.
func seriesNumber(name, id string) (int, bool) {
	rest, ok := strings.CutPrefix(name, id+".")
	n, err := strconv.Atoi(rest)

	return n, ok && err == nil && n > 0
}

// SeriesNumbers is the archived series of id, ascending.
func SeriesNumbers(archived, id string) []int {
	entries, _ := os.ReadDir(archived)

	var out []int

	for _, e := range entries {
		if n, ok := seriesNumber(e.Name(), id); ok && e.IsDir() {
			out = append(out, n)
		}
	}

	slices.Sort(out)

	return out
}

func NextSeries(archived, id string) string {
	n := 1
	if have := SeriesNumbers(archived, id); len(have) > 0 {
		n = have[len(have)-1] + 1
	}

	return filepath.Join(archived, seriesName(id, n))
}

// ClosedPRs is the archived series that each ended a PR, oldest first: a
// restart is left out.
func ClosedPRs(dir string) []string {
	archived, id := storage.ArchiveOf(filepath.Dir(dir)), filepath.Base(dir)

	var out []string

	for _, n := range SeriesNumbers(archived, id) {
		path := filepath.Join(archived, seriesName(id, n))
		if !ReadSeriesEnd(path).Restart {
			out = append(out, path)
		}
	}

	return out
}

// ChangeOf is the lore change of the task's open series: the task id for its
// first PR, <id>.<n> for its n-th, counted from the PRs archived.
func ChangeOf(dir string) string {
	return SeriesChange(filepath.Base(dir), len(ClosedPRs(dir))+1)
}

func SeriesChange(id string, n int) string {
	if n <= 1 {
		return id
	}

	return seriesName(id, n)
}

// LoreSeries is the changes a show of id reads: every <id>.<n> newest first,
// then id itself; a change named with its number, or a task with one PR,
// is that change alone.
func LoreSeries(changes []string, id string) []string {
	var later []int

	for _, c := range changes {
		if n, ok := seriesNumber(c, id); ok && n > 1 {
			later = append(later, n)
		}
	}

	slices.Sort(later)
	slices.Reverse(later)

	out := make([]string, 0, len(later)+1)
	for _, n := range later {
		out = append(out, seriesName(id, n))
	}

	return append(out, id)
}

// SeriesStart is when the task's open series began, the newest archived
// PR's end; zero for a first series.
func SeriesStart(dir string) time.Time {
	closed := ClosedPRs(dir)
	if len(closed) == 0 {
		return time.Time{}
	}

	return SeriesEndAt(closed[len(closed)-1])
}

// SeriesEndAt is when an archived series closed: its closed file's time,
// else the directory's, for a series archived before the file recorded one.
func SeriesEndAt(dir string) time.Time {
	if e := ReadSeriesEnd(dir); !e.At.IsZero() {
		return e.At
	}

	return ModTime(dir)
}
