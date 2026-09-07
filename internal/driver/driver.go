// Package driver runs a project's tracker driver: an executable at
// .f10/driver satisfying the verb contract in docs/driver-contract.md.
//
// f10 specifies that contract and implements none of it. What a driver
// talks to - Notion, Jira, a wiki, a spreadsheet - is the project's
// business, and so is the language it is written in. The one thing f10
// fixes is the shape of the conversation: a verb, an argument, data on
// stdout, and a reserved exit code for a verb the driver does not offer.
//
// Drivers return data and never act. f10 performs every side effect, which
// is what lets `open` mean the same thing for a task as for a pull request,
// where no driver is involved at all.
package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/amberpixels/f10/internal/shell"
)

// Verbs a driver may implement.
const (
	VerbRead   = "read"   // stdout: the task as markdown
	VerbURL    = "url"    // stdout: one url
	VerbSearch = "search" // stdout: JSON rows, one task each
)

// ExitUnsupported is the contract's reserved exit code: the driver ran and
// understood the call, but does not implement that verb. It is a normal
// answer, not a failure, so a driver can offer `read` without `search`.
const ExitUnsupported = 3

// ErrUnsupported reports the reserved exit code back to the caller, which
// then falls back or explains, rather than failing the command.
var ErrUnsupported = errors.New("driver does not implement this verb")

// A Driver is one project's executable.
type Driver struct {
	Path string // the executable
	Dir  string // the checkout it runs in
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
	res, err := shell.Capture(ctx, d.Dir, d.Path, append([]string{verb}, args...)...)
	if err != nil {
		return "", fmt.Errorf("running %s %s: %w", d.Path, verb, err)
	}

	switch res.Code {
	case 0:
		return res.Stdout, nil
	case ExitUnsupported:
		return "", fmt.Errorf("%s: %w", verb, ErrUnsupported)
	default:
		detail := res.Stderr
		if detail == "" {
			detail = fmt.Sprintf("exit %d, no stderr", res.Code)
		}

		return "", fmt.Errorf("%s %s: %s", filepath.Base(d.Path), verb, detail)
	}
}
