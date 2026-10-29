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
)

// A Record is one drive, kept at <storage root>/drives/<n>.yaml: what it
// was started with, the answers given, and how each task ended.
type Record struct {
	Number      int        `yaml:"number"`
	Created     time.Time  `yaml:"created"`
	Args        []string   `yaml:"args"` // the command line, --answer, --every and --foreground dropped
	Tasks       []Task     `yaml:"tasks"`
	Flow        string     `yaml:"flow,omitempty"`
	Agent       string     `yaml:"agent,omitempty"` // the run-wide --agent (or --model) as typed
	Model       string     `yaml:"model,omitempty"` // the run-wide --model of a record from before profiles, read only
	DriveFinish bool       `yaml:"drive_finish"`
	Cancelled   *time.Time `yaml:"cancelled,omitempty"`
	Session     string     `yaml:"session,omitempty"` // the session that created it, "" from a plain terminal
	// Answers are given and not yet taken, by task id, "" for the bare one
	Answers  map[string]string `yaml:"answers,omitempty"`
	Answered []Answer          `yaml:"answered,omitempty"`
}

// A Task is one task of a drive, as typed, and where it stands.
type Task struct {
	ID      string   `yaml:"id"`              // as typed, a suffix included
	Agent   string   `yaml:"agent,omitempty"` // its own :profile as typed
	Model   string   `yaml:"model,omitempty"` // its own :model from before profiles, read only
	After   string   `yaml:"after,omitempty"` // the base its body's After: line named, as an id
	Skill   string   `yaml:"skill,omitempty"` // in flight, "" for none
	Outcome *Outcome `yaml:"outcome,omitempty"`
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

// Same reports whether args names the same drive: the same tasks and flags,
// order kept, case ignored.
func (r *Record) Same(args []string) bool {
	return slices.EqualFunc(r.Args, args, strings.EqualFold)
}

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
	l, err := lockRecord(root, n)
	if err != nil {
		return nil, err
	}
	defer l.Release()

	rec, err := Read(root, n)
	if err != nil {
		return nil, err
	}

	change(rec)

	b, err := yaml.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("drive %d: %w", n, err)
	}

	tmp := recordPath(root, n) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return nil, fmt.Errorf("drive %d: %w", n, err)
	}

	if err := os.Rename(tmp, recordPath(root, n)); err != nil {
		return nil, fmt.Errorf("drive %d: %w", n, err)
	}

	return rec, nil
}

// lockRecord holds record n's write lock, waiting out a writer for a while.
func lockRecord(root string, n int) (*Lease, error) {
	key := "record-" + strconv.Itoa(n)

	for range 40 {
		l, err := Acquire(root, key, 0)
		if held := (HeldError{}); !errors.As(err, &held) {
			return l, err
		}

		time.Sleep(retryDelay)
	}

	return nil, fmt.Errorf("drive %d: its record stayed locked", n)
}

// Find is the newest record started with args that is neither cancelled
// nor done, nil for none.
func Find(root string, args []string) (*Record, error) {
	recs, err := List(root)
	if err != nil {
		return nil, err
	}

	for _, r := range recs {
		if r.Cancelled == nil && !r.Done() && r.Same(args) {
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
