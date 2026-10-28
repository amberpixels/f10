//go:build unix

package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
)

// An Ensurer finds this user's daemon, or starts one.
type Ensurer struct {
	Dir     string
	Version string
	// Follow takes any daemon running, whatever its version: a client
	// following a drive into the daemon that resumed it.
	Follow bool
	// Spawn starts a daemon detached; by default `<exe> daemon` in its own
	// session, its output to the daemon log.
	Spawn func(ctx context.Context) error
	// Draining is told when an older daemon is being replaced, Kept when a
	// daemon this client does not replace runs another version.
	Draining, Kept func(running string)
	// StartWait bounds how long a new daemon has to listen; DrainWait how
	// long an old one has to drain.
	StartWait, DrainWait time.Duration
}

// Ensure returns a daemon listening, starting one when none does. Only a
// strictly newer client replaces a running daemon, draining it first, so
// two versions never take turns. Two clients at once get one daemon: the
// start lease, then the daemon's own.
func (e *Ensurer) Ensure(ctx context.Context) (Reply, error) {
	if e.StartWait == 0 {
		e.StartWait = 5 * time.Second
	}

	if e.DrainWait == 0 {
		e.DrainWait = 11 * time.Minute
	}

	// the client that drained the daemon starts the next one; a follower
	// starting its own could bring an older binary back
	if e.Follow {
		if rep, ok := e.awaitAny(ctx); ok {
			return rep, nil
		}
	}

	for range 3 {
		rep, done, err := e.settle(ctx)
		if err != nil || done {
			return rep, err
		}

		rep, started, err := e.start(ctx)
		if err != nil || started {
			return rep, err
		}
	}

	return Reply{}, errors.New("the f10 daemon kept changing while this client started: run the command again")
}

// settle uses a running daemon this client keeps, else waits out one that
// is stopping or drains one this client replaces; done is false when a
// daemon must be started.
func (e *Ensurer) settle(ctx context.Context) (Reply, bool, error) {
	rep, err := e.hello(ctx)

	_, held := drives.Probe(e.Dir, "daemon")

	switch {
	case (err == nil && rep.Stopping) || (err != nil && held):
		// a daemon draining its drives: let it finish, then start anew
		return Reply{}, false, e.gone(ctx)
	case err == nil && e.keeps(rep):
		if rep.Version != e.Version && !e.Follow && e.Kept != nil {
			e.Kept(rep.Version)
		}

		return rep, true, nil
	case err == nil:
		if e.Draining != nil {
			e.Draining(rep.Version)
		}

		if _, err := Call(ctx, e.Dir, Request{Op: OpStop, Drain: true, Upgrade: true}, 0); err != nil {
			return Reply{}, false, fmt.Errorf("stopping daemon %s: %w", rep.Version, err)
		}

		return Reply{}, false, e.gone(ctx)
	}

	return Reply{}, false, nil
}

// start spawns this binary's daemon under the start lease. started is
// false when, holding the lease, it finds a daemon it must not start over:
// one stopping, or one it replaces, which settle handles again.
func (e *Ensurer) start(ctx context.Context) (Reply, bool, error) {
	if err := os.MkdirAll(e.Dir, 0o700); err != nil {
		return Reply{}, false, fmt.Errorf("daemon: %w", err)
	}

	if err := ownDir(e.Dir); err != nil {
		return Reply{}, false, err
	}

	lease, err := e.startLease(ctx)
	if err != nil {
		return Reply{}, false, err
	}
	defer lease.Release()

	if rep, err := e.hello(ctx); err == nil {
		return rep, !rep.Stopping && e.keeps(rep), nil
	}

	if _, held := drives.Probe(e.Dir, "daemon"); held {
		return Reply{}, false, nil
	}

	spawn := e.Spawn
	if spawn == nil {
		spawn = e.spawn
	}

	if err := spawn(ctx); err != nil {
		return Reply{}, false, fmt.Errorf("starting the f10 daemon: %w", err)
	}

	deadline := time.Now().Add(e.StartWait)
	for {
		rep, err := e.hello(ctx)
		if err == nil {
			return rep, true, nil
		}

		if time.Now().After(deadline) {
			return Reply{}, false, fmt.Errorf(
				"the f10 daemon did not start within %s: see %s",
				e.StartWait,
				LogPath(e.Dir),
			)
		}

		if err := sleep(ctx, 50*time.Millisecond); err != nil {
			return Reply{}, false, err
		}
	}
}

// awaitAny waits for a daemon, any version, to listen and not be stopping.
// False when none appears: past DrainWait, or once no daemon has held the
// lease for StartWait, the upgrading client having died.
func (e *Ensurer) awaitAny(ctx context.Context) (Reply, bool) {
	deadline := time.Now().Add(e.DrainWait + e.StartWait)

	var free time.Time

	for time.Now().Before(deadline) {
		rep, err := e.hello(ctx)
		if err == nil && !rep.Stopping {
			return rep, true
		}

		_, held := drives.Probe(e.Dir, "daemon")

		switch {
		case err == nil || held:
			free = time.Time{}
		case free.IsZero():
			free = time.Now()
		case time.Since(free) > e.StartWait:
			return Reply{}, false
		}

		if sleep(ctx, 100*time.Millisecond) != nil {
			return Reply{}, false
		}
	}

	return Reply{}, false
}

// keeps reports a running daemon this client uses as it is: following,
// the same version, or one this client is not newer than.
func (e *Ensurer) keeps(rep Reply) bool {
	return e.Follow || rep.Version == e.Version || !Newer(e.Version, rep.Version)
}

func (e *Ensurer) hello(ctx context.Context) (Reply, error) {
	return Call(ctx, e.Dir, Request{Op: OpHello, Version: e.Version}, 0)
}

// gone waits for a stopping daemon's lease to free.
func (e *Ensurer) gone(ctx context.Context) error {
	deadline := time.Now().Add(e.DrainWait)

	for {
		if _, held := drives.Probe(e.Dir, "daemon"); !held {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("the old f10 daemon did not stop within %s: f10 daemon stop --now", e.DrainWait)
		}

		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

// startLease waits for the lease that lets one client start a daemon.
func (e *Ensurer) startLease(ctx context.Context) (*drives.Lease, error) {
	deadline := time.Now().Add(e.StartWait + time.Second)

	for {
		l, err := drives.Acquire(e.Dir, "start", 0)

		held := drives.HeldError{}
		if !errors.As(err, &held) {
			return l, err
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another client is starting the f10 daemon (pid %d)", held.Holder.PID)
		}

		if err := sleep(ctx, 50*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

func (e *Ensurer) spawn(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(LogPath(e.Dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	// the daemon outlives this client: its context never cancels it
	cmd := exec.CommandContext(context.WithoutCancel(ctx), exe, "daemon")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	return cmd.Process.Release()
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
