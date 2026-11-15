package finish

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A Request is a finish as its caller words it.
type Request struct {
	Token string // the task id, any bundle member, or "" for the branch's
	Close string // the riders the PR delivers: ids, all or none
	Flags Flags
	// Cwd and Workspace are where the caller runs; a finish from inside the
	// task's own workspace closes it last
	Cwd, Workspace string
}

// Run finishes the task req names in t's project, writing the report to w
// and the janitor's lines to progress.
func Run(ctx context.Context, w, progress io.Writer, t *target.Target, req Request, out *Finished) error {
	f := req.Flags
	if f.Merge && f.LeaveOpen {
		return errors.New("--merge and --local are exclusive: one merges the PR, the other leaves it open")
	}

	if f.Keep && (f.Merge || f.LeaveOpen) {
		return errors.New("--keep keeps a spike's experiment, which has no PR to --merge or leave --local")
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	r, suffix, branch, err := Ref(ctx, t, req.Token)
	if err != nil {
		return err
	}

	if suffix != "" && r.Bundle != nil {
		return fmt.Errorf("%s ships in bundle %s, which takes no suffix", r.ID, r.Label())
	}

	// any member finishes the whole bundle, found by its lead
	r = r.Head()

	var closing []string

	if v := strings.TrimSpace(req.Close); v != "" {
		if len(r.Riders()) == 0 {
			return fmt.Errorf("--close names riders, and %s ships alone", r.ID)
		}

		resolve := func(tok string) (ref.Ref, error) { return t.Reference(ctx, tok) }
		if closing, err = CloseChoice(r, v, resolve); err != nil {
			return err
		}
	}

	// drive's own finish runs under the lease drive holds, so only a caller
	// from outside takes it
	lease, err := Lease(t.Layout.StorageRoot.Path(), r.ID)
	if err != nil {
		return err
	}
	defer lease.Release()

	in := InputFor(t, cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot), h, r)
	in.Cwd, in.Workspace = req.Cwd, req.Workspace
	in.Suffix, in.Branch = suffix, branch
	in.Flags = f
	in.ChecksWait = ChecksBudget
	in.Close = closing
	in.Outcome = out
	in.Sweep = func(ctx context.Context) {
		SweepIfDue(
			ctx,
			progress,
			filepath.Join(t.Layout.StorageRoot.Path(), JanitorStamp),
			time.Now(),
			func(ctx context.Context) ([]string, error) { return Janitor(ctx, JanitorFor(t)) },
		)
	}

	err = Finish(ctx, w, in)

	if fp := (ForeignPullError{}); errors.As(err, &fp) {
		return ui.Exit(ui.ExitDeclined, "%v", fp)
	}

	if re := (RidersError{}); errors.As(err, &re) {
		return ui.Exit(ui.ExitRiders, "%v", re)
	}

	return err
}
