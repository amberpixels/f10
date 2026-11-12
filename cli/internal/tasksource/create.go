package tasksource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
)

// MayExist follows every failed create but a driver's exit 4: the driver may
// have made the task before failing, so running create again could make it twice.
const MayExist = "the task may exist already: check the tracker before running create again"

// driverCreate returns the new task and its url, "" when the driver gives
// none; a declined create is driver.ErrUnsupported, unwrapped.
func driverCreate(
	ctx context.Context, warnTo io.Writer, t *target.Target, d *trackerdriver.Driver, doc string, args []string,
) (ref.Ref, string, error) {
	out, warn, err := d.RunInput(ctx, doc, trackerdriver.VerbCreate, args...)
	if errors.Is(err, trackerdriver.ErrUnsupported) {
		return ref.Ref{}, "", err
	}

	// the driver rejected the input before writing, so running it again is safe
	if errors.Is(err, trackerdriver.ErrNotWritten) {
		return ref.Ref{}, "", err
	}

	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("%w\n%s", err, MayExist)
	}

	if warn != "" {
		fmt.Fprintln(warnTo, warn)
	}

	// the driver may print the number or the prefixed id; f10 prints the id
	line := strings.TrimSpace(out)
	if line == "" || strings.ContainsAny(line, " \t\n"+ref.Separator) {
		return ref.Ref{}, "", fmt.Errorf("driver create printed %q, not one task id\n%s", out, MayExist)
	}

	r, err := t.Reference(ctx, line)
	if err != nil {
		return ref.Ref{}, "", fmt.Errorf("driver create printed %q: %w\n%s", line, err, MayExist)
	}

	url, _ := d.Run(ctx, trackerdriver.VerbURL, r.Number)

	return r, strings.TrimSpace(url), nil
}
