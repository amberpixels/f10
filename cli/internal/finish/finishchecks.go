package finish

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
)

// ChecksBudget is how long a pending head may hold a finish, interactive or
// driven, before it is refused as if red.
const ChecksBudget = 20 * time.Minute

// ChecksPoll is how often a waiting finish reads the checks again.
var ChecksPoll = 15 * time.Second

// A ChecksError refuses a merge on the head's checks, with nothing changed;
// Waited is how long this finish waited on them.
type ChecksError struct {
	URL    string
	Checks forge.HeadChecks
	Waited time.Duration
}

// Pending reports a head whose checks were still running when refused.
func (e ChecksError) Pending() bool { return e.Checks.State == forge.Pending }

func (e ChecksError) Error() string {
	head := e.URL
	if sha := e.Checks.SHA; sha != "" {
		head += " at " + sha[:min(7, len(sha))]
	}

	if e.Checks.State == forge.Red {
		var jobs strings.Builder
		for _, c := range e.Checks.Failed {
			jobs.WriteString("\n  " + c.Name)
			if c.URL != "" {
				jobs.WriteString("  " + c.URL)
			}
		}

		return fmt.Sprintf("%s has failing checks:%s\nfix them and push, or pass --ignore-checks to merge anyway",
			head, jobs.String())
	}

	running := ""
	if n := forge.Names(e.Checks.Running); n != "" {
		running = " (" + n + ")"
	}

	waited := ""
	if e.Waited > 0 {
		waited = " after " + e.Waited.Round(time.Second).String()
	}

	return fmt.Sprintf("%s: checks still running%s%s: rerun once they pass, or pass --ignore-checks to merge anyway",
		head, running, waited)
}

// gateChecks reads the head's checks before a merge: green or none passes,
// red refuses, pending waits up to ChecksWait. It returns the sha it read,
// which the merge is pinned to.
func gateChecks(ctx context.Context, in FinishInput, p forge.PR) (string, error) {
	if in.Flags.IgnoreChecks {
		return "", nil
	}

	in.step("reading the checks on " + in.Host.Ref(p.Number))

	start := time.Now()

	for {
		hc, err := in.Host.HeadChecks(ctx, p)
		if err != nil {
			return "", fmt.Errorf("reading the checks on %s: %w (--ignore-checks merges without them)", p.URL, err)
		}

		waited := time.Since(start)

		switch {
		case hc.State != forge.Red && hc.State != forge.Pending:
			return hc.SHA, nil
		case hc.State == forge.Red, waited >= in.ChecksWait:
			return "", ChecksError{URL: p.URL, Checks: hc, Waited: waited}
		}

		in.step("waiting on checks: " + forge.Names(hc.Running))

		if err := sleep(ctx, min(ChecksPoll, in.ChecksWait-waited)); err != nil {
			return "", err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
