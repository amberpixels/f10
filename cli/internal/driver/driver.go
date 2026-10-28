// Package driver runs a project's tracker driver: an executable at
// .f10/driver satisfying the verb contract in docs/driver-contract.md.
//
// f10 specifies that contract and implements none of it. What a driver
// talks to - Notion, Jira, a wiki, a spreadsheet - is the project's
// business, and so is the language it is written in. The one thing f10
// fixes is the shape of the conversation: a verb, an argument, data on
// stdout, and a reserved exit code for a verb the driver does not offer.
//
// Read verbs return data and change nothing; write verbs change the
// tracker and only the tracker. Neither acts on the local machine: f10 opens
// every url and creates every branch, which is what lets `open` mean the
// same thing for a task as for a pull request, where no driver is involved.
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

	// VerbCreate is the first write verb: stdin is the task as markdown,
	// `# <title>` first; stdout is the new task's id.
	VerbCreate = "create"

	// VerbComment adds a comment to a task: stdin is the comment as
	// markdown; stdout is the comment's url, or nothing.
	VerbComment = "comment"

	// VerbUpdate sets a task's properties from flags, each an absolute
	// value; stdin is empty and stdout ignored.
	VerbUpdate = "update"
)

// Priorities is the contract's ordered priority vocabulary, most urgent
// first. A search or list row may carry one as its `priority`; a driver maps
// its tracker's own values onto these, and a row without one has no priority.
var Priorities = []string{"urgent", "high", "medium", "low"}

// PriorityRank places a row's priority value in Priorities, case and
// surrounding space ignored: its index and true, or -1 and false for a value
// outside the vocabulary.
func PriorityRank(value string) (int, bool) {
	i := slices.Index(Priorities, strings.ToLower(strings.TrimSpace(value)))

	return i, i >= 0
}

// ExitUnsupported is the contract's reserved exit code: the driver ran and
// understood the call, but does not implement that verb. It is a normal
// answer, not a failure, so a driver can offer `read` without `search`.
const ExitUnsupported = 3

// ErrUnsupported reports the reserved exit code back to the caller, which
// then falls back or explains, rather than failing the command.
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

// Find returns the driver for a storage root, or nil when the project has
// none - the ordinary case, which callers answer by falling back to the
// host's issues.
func Find(storageRoot, dir string) *Driver {
	path := filepath.Join(storageRoot, "driver")

	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return nil
	}

	return &Driver{Path: path, Dir: dir}
}

// Run invokes one verb and returns its stdout. The reserved code becomes
// ErrUnsupported; any other non-zero exit is a failure carrying the
// driver's own stderr verbatim, because the driver knows why it failed and
// f10 does not.
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

// RunInput invokes a write verb with input on its stdin and returns its
// stdout and, on success, its stderr: a warning about a property the
// tracker has no field for. It is never retried, because a failed write
// may still have changed the tracker, a timed-out one too.
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

// classify reads an exit code per the contract: nil for 0, ErrUnsupported
// for the reserved code, the driver's stderr for anything else.
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
