package drivectl

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/target"
)

// WatchProject hands the project to this user's daemon, starting one when
// none runs. It never replaces or waits out a running daemon (f10 start's
// job, which can tell the user), and an error is one warning, never a failed
// command. A variable so tests start no daemon.
var WatchProject = func(ctx context.Context, w io.Writer, t *target.Target) {
	if !daemon.Supported || t.Effective.Watch == "off" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, watchHandWait)
	defer cancel()

	dir := daemon.RunDir()

	info, err := daemon.Hello(ctx, dir, buildinfo.Version())

	switch {
	case err == nil && info.Stopping:
		fmt.Fprintln(w, "watch: the f10 daemon is stopping: the next f10 start hands this project over")

		return
	case errors.Is(err, daemon.ErrNotRunning):
		if _, err := (&daemon.Ensurer{Dir: dir, Version: buildinfo.Version()}).Ensure(ctx); err != nil {
			fmt.Fprintf(w, "watch: warning: %v\n", err)

			return
		}
	case err != nil:
		fmt.Fprintf(w, "watch: warning: %v\n", err)

		return
	case info.Legacy || daemon.APIError(info, buildinfo.Version()) != nil:
		// an older daemon is replaced by the next f10 start, which can say so
		fmt.Fprintf(w, "watch: the f10 daemon runs %s: the next f10 start replaces it and hands this project over\n",
			info.Version)

		return
	}

	_, err = daemon.WatchProject(
		ctx,
		dir,
		t.Layout.StorageRoot.Path(),
		cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		daemon.DriveEnv(os.Environ(), t.Effective.BatchEnv),
	)
	if err != nil {
		fmt.Fprintf(w, "watch: warning: %v\n", err)
	}
}

// watchHandWait bounds handing a project over, a daemon started included.
const watchHandWait = 10 * time.Second
