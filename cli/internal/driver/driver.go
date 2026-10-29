// Package driver runs the executable at .f10/driver, a project's tracker
// driver per docs/driver-contract.md. It touches only its tracker: f10 opens
// urls and creates branches, so `open` means the same with or without one.
package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// Verbs a driver may implement. All but create, comment and update are
// read verbs.
const (
	VerbRead     = "read"     // stdout: the task as markdown
	VerbURL      = "url"      // stdout: one url
	VerbSearch   = "search"   // stdout: JSON rows, one task each
	VerbBranch   = "branch"   // stdout: one branch name, never created
	VerbChildren = "children" // stdout: JSON rows, one subtask each
	VerbIndex    = "index"    // stdout: one url, the task list; no argument
	VerbList     = "list"     // stdout: JSON rows, the first page of open tasks; no argument

	// VerbCreate makes a task: stdin is the task as markdown, `# <title>`
	// first; stdout is the new task's id.
	VerbCreate = "create"

	// VerbComment adds a comment to a task: stdin is the comment as
	// markdown; stdout is the comment's url, or nothing.
	VerbComment = "comment"

	// VerbUpdate sets a task's properties from flags, each an absolute
	// value; stdin is empty and stdout ignored.
	VerbUpdate = "update"
)

// Priorities is the contract's priority vocabulary, most urgent first; a
// driver maps its tracker's values onto it, and a row without one has none.
var Priorities = []string{"urgent", "high", "medium", "low"}

// PriorityRank is value's index in Priorities, case and surrounding space
// ignored; -1 and false outside the vocabulary.
func PriorityRank(value string) (int, bool) {
	i := slices.Index(Priorities, strings.ToLower(strings.TrimSpace(value)))

	return i, i >= 0
}

// ExitUnsupported is the contract's reserved exit code: the driver ran and
// understood the call, but does not implement that verb. It is a normal
// answer, not a failure, so a driver can offer `read` without `search`.
const ExitUnsupported = 3

// ErrUnsupported is ExitUnsupported as an error; callers fall back, never fail.
var ErrUnsupported = errors.New("driver does not implement this verb")

// DefaultTimeout bounds one verb when a Driver sets no Timeout.
const DefaultTimeout = 30 * time.Second

// ErrTimeout is a verb that gave no answer within its limit: the driver
// hung on its tracker, and f10 stops waiting rather than hang with it.
var ErrTimeout = errors.New("no answer")

// A Driver is one project's executable.
type Driver struct {
	Path    string        // the executable
	Dir     string        // the checkout it runs in
	Timeout time.Duration // one verb's limit; zero is DefaultTimeout
}

// Find is the storage root's driver, nil when the project has none.
func Find(storageRoot, dir string) *Driver {
	path := filepath.Join(storageRoot, "driver")

	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return nil
	}

	return &Driver{Path: path, Dir: dir}
}

// Run invokes one verb and returns its stdout. The reserved code is
// ErrUnsupported; any other non-zero exit carries the driver's stderr
// verbatim, since only the driver knows why it failed.
func (d *Driver) Run(ctx context.Context, verb string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.limit())
	defer cancel()

	res, err := shell.Capture(ctx, d.Dir, d.Path, append([]string{verb}, args...)...)
	if terr := d.timedOut(ctx, verb, res, err); terr != nil {
		return "", terr
	}

	if err != nil {
		return "", fmt.Errorf("running %s %s: %w", d.Path, verb, err)
	}

	if err := d.classify(verb, res); err != nil {
		return "", err
	}

	return res.Stdout, nil
}

// RunInput invokes a write verb with input on stdin and returns its stdout
// and stderr, a warning about a property the tracker lacks. Never retried:
// a failed or timed-out write may still have changed the tracker.
func (d *Driver) RunInput(ctx context.Context, input, verb string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.limit())
	defer cancel()

	res, err := shell.CaptureInput(ctx, d.Dir, input, d.Path, append([]string{verb}, args...)...)
	if terr := d.timedOut(ctx, verb, res, err); terr != nil {
		return "", "", fmt.Errorf("%w; the write may still have reached the tracker", terr)
	}

	if err != nil {
		return "", "", fmt.Errorf("running %s %s: %w", d.Path, verb, err)
	}

	if err := d.classify(verb, res); err != nil {
		return "", "", err
	}

	return res.Stdout, res.Stderr, nil
}

func (d *Driver) limit() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}

	return DefaultTimeout
}

// timedOut names a verb the deadline killed. A verb that exited 0 answered,
// even when the deadline passed while a child it left held stdout; a
// cancelled parent is the caller's doing, not the driver's.
func (d *Driver) timedOut(ctx context.Context, verb string, res shell.Result, err error) error {
	if err == nil && res.Code == 0 || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil
	}

	return fmt.Errorf("%s %s: %w within %s", filepath.Base(d.Path), verb, ErrTimeout, d.limit())
}

func (d *Driver) classify(verb string, res shell.Result) error {
	switch res.Code {
	case 0:
		return nil
	case ExitUnsupported:
		return fmt.Errorf("%s: %w", verb, ErrUnsupported)
	default:
		detail := res.Stderr
		if detail == "" {
			detail = fmt.Sprintf("exit %d, no stderr", res.Code)
		}

		return fmt.Errorf("%s %s: %s", filepath.Base(d.Path), verb, detail)
	}
}
