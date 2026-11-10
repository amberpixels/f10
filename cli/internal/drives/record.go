package drives

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/flock"
	"github.com/amberpixels/f10/cli/internal/fsx"
)

// A Record is one drive, kept at <storage root>/drives/<n>.yaml: what it
// was started with, the answers given, and how each task ended.
type Record struct {
	Number    int        `yaml:"number"`
	Created   time.Time  `yaml:"created"`
	Args      []string   `yaml:"args"` // the command line, --answer, --every, --parallel and --foreground dropped
	Tasks     []Task     `yaml:"tasks"`
	Flow      string     `yaml:"flow,omitempty"`
	Agent     string     `yaml:"agent,omitempty"` // the run-wide --agent (or --model) as typed
	Cancelled *time.Time `yaml:"cancelled,omitempty"`
	Session   string     `yaml:"session,omitempty"` // the session that created it, "" from a plain terminal
	// Parallel is the cap on tasks running a skill at once, as the drive
	// took it; a rerun or a resume keeps it unless it passes --parallel
	Parallel int `yaml:"parallel,omitempty"`
	// Answers are given and not yet taken, by task id, "" for the bare one
	Answers  map[string]string `yaml:"answers,omitempty"`
	Answered []Answer          `yaml:"answered,omitempty"`
}

// A Task is one task of a drive, as typed, and where it stands.
type Task struct {
	ID      string   `yaml:"id"`               // as typed, a suffix included
	Agent   string   `yaml:"agent,omitempty"`  // its own :profile as typed
	After   string   `yaml:"after,omitempty"`  // the base its body's After: line named, as an id
	Skill   string   `yaml:"skill,omitempty"`  // in flight, "" for none
	Queued  bool     `yaml:"queued,omitempty"` // held back by the drive's cap
	Outcome *Outcome `yaml:"outcome,omitempty"`
	// Catchup is the catchup the drive last sent for the task, so a resumed
	// drive still leases its push and counts its repeats
	Catchup *Catchup `yaml:"catchup,omitempty"`
}

// An Outcome is a task's row in the drive's report and its exit code.
type Outcome struct {
	Row  string `yaml:"row"`
	Code int    `yaml:"code"`
}

// An Answer is one taken: by which task, at which skill, when.
type Answer struct {
	Task  string    `yaml:"task"`
	Skill string    `yaml:"skill,omitempty"`
	Text  string    `yaml:"text"`
	At    time.Time `yaml:"at"`
}

// Done reports every task through with code 0: a drive with nothing left.
func (r *Record) Done() bool {
	return len(r.Tasks) > 0 && !slices.ContainsFunc(r.Tasks, func(t Task) bool {
		return t.Outcome == nil || t.Outcome.Code != 0
	})
}

// Task is the record's task with id, nil for none.
func (r *Record) Task(id string) *Task {
	for i := range r.Tasks {
		if strings.EqualFold(r.Tasks[i].ID, id) {
			return &r.Tasks[i]
		}
	}

	return nil
}

// Key is the drive a command line names: its args as the record keeps them,
// joined and lowercased, so the same tasks and flags in the same order match
// whatever their case. A client sends it to the daemon, so its form stays.
func Key(args []string) string { return strings.ToLower(strings.Join(args, " ")) }

// Key is the drive this record is.
func (r *Record) Key() string { return Key(r.Args) }

// Path is record n's file under the storage root.
func Path(root string, n int) string { return recordPath(root, n) }

func recordPath(root string, n int) string {
	return filepath.Join(root, Dir, strconv.Itoa(n)+".yaml")
}

// Create writes rec as the next numbered record, setting its number. The
// file is created exclusively, so two drives started at once get two.
func Create(root string, rec *Record) error {
	dir := filepath.Join(root, Dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("drive record: %w", err)
	}

	for range 100 {
		ns, err := numbers(root)
		if err != nil {
			return err
		}

		rec.Number = 1
		if len(ns) > 0 {
			rec.Number = ns[len(ns)-1] + 1
		}

		b, err := yaml.Marshal(rec)
		if err != nil {
			return fmt.Errorf("drive record: %w", err)
		}

		f, err := os.OpenFile(recordPath(root, rec.Number), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}

		if err != nil {
			return fmt.Errorf("drive record: %w", err)
		}

		_, err = f.Write(b)
		if cerr := f.Close(); err == nil {
			err = cerr
		}

		if err != nil {
			return fmt.Errorf("drive record %d: %w", rec.Number, err)
		}

		return nil
	}

	return errors.New("drive record: no free number after 100 tries")
}

// Read reads record n.
func Read(root string, n int) (*Record, error) {
	b, err := os.ReadFile(recordPath(root, n))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no drive %d: f10 drive list shows them", n)
	}

	if err != nil {
		return nil, fmt.Errorf("drive %d: %w", n, err)
	}

	var rec Record
	if err := yaml.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("drive %d: %w", n, err)
	}

	rec.Number = n

	return &rec, nil
}

// Update applies change to record n under its own lock and writes it back
// through a rename, so a cancel and the drive's own writes never lose one
// another. It returns the record as written.
func Update(root string, n int, change func(*Record)) (*Record, error) {
	release, err := lockRecord(root, n)
	if err != nil {
		return nil, err
	}
	defer release()

	rec, err := Read(root, n)
	if err != nil {
		return nil, err
	}

	change(rec)

	b, err := yaml.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("drive %d: %w", n, err)
	}

	if err := fsx.WriteFile(recordPath(root, n), b, 0o600); err != nil {
		return nil, fmt.Errorf("drive %d: %w", n, err)
	}

	return rec, nil
}

// recordLockWait is how long Update waits out another writer of a record.
const recordLockWait = 2 * time.Second

// lockRecord holds record n's write lock, waiting out a writer for a while.
func lockRecord(root string, n int) (func(), error) {
	release, err := flock.Wait(lockPath(root, "record-"+strconv.Itoa(n)), recordLockWait)
	if errors.Is(err, flock.ErrHeld) {
		return nil, fmt.Errorf("drive %d: its record stayed locked", n)
	}

	if err != nil {
		return nil, fmt.Errorf("drive %d: %w", n, err)
	}

	return release, nil
}

// Find is the newest record started with args that is neither cancelled
// nor done, nil for none.
func Find(root string, args []string) (*Record, error) {
	recs, err := List(root)
	if err != nil {
		return nil, err
	}

	key := Key(args)

	for _, r := range recs {
		if r.Cancelled == nil && !r.Done() && r.Key() == key {
			return r, nil
		}
	}

	return nil, nil //nolint:nilnil // none is not an error
}

// List is every record, newest first. One that will not parse is skipped.
func List(root string) ([]*Record, error) {
	ns, err := numbers(root)
	if err != nil {
		return nil, err
	}

	recs := make([]*Record, 0, len(ns))

	for _, n := range slices.Backward(ns) {
		if r, err := Read(root, n); err == nil {
			recs = append(recs, r)
		}
	}

	return recs, nil
}

// numbers are the records' numbers, ascending.
func numbers(root string) ([]int, error) {
	entries, err := os.ReadDir(filepath.Join(root, Dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("drive records: %w", err)
	}

	var ns []int

	for _, e := range entries {
		if s, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && !e.IsDir() {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				ns = append(ns, n)
			}
		}
	}

	sort.Ints(ns)

	return ns, nil
}
