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
	// Spawn starts a daemon detached; by default `<exe> daemon` in its own
	// session, its output to the daemon log.
	Spawn func(ctx context.Context) error
	// Draining is told when an older daemon is being replaced.
	Draining func(old string)
	// StartWait bounds how long a new daemon has to listen; DrainWait how
	// long an old one has to drain.
	StartWait, DrainWait time.Duration
}

// Ensure returns a daemon of this version listening, starting one when
// none listens and replacing an older one by draining it first. Two
// clients at once get one daemon: the start lease, then the daemon's own.
func (e *Ensurer) Ensure(ctx context.Context) (Reply, error) {
	if e.StartWait == 0 {
		e.StartWait = 5 * time.Second
	}

	if e.DrainWait == 0 {
		e.DrainWait = 11 * time.Minute
	}

	rep, err := e.hello(ctx)

	// a daemon that stopped listening may still be draining its drives
	if _, held := drives.Probe(e.Dir, "daemon"); err != nil && held {
		if err := e.gone(ctx); err != nil {
			return Reply{}, err
		}
	}

	if err == nil {
		if rep.Version == e.Version {
			return rep, nil
		}

		if e.Draining != nil {
			e.Draining(rep.Version)
		}

		if _, err := Call(ctx, e.Dir, Request{Op: OpStop, Drain: true}, 0); err != nil {
			return Reply{}, fmt.Errorf("stopping daemon %s: %w", rep.Version, err)
		}

		if err := e.gone(ctx); err != nil {
			return Reply{}, err
		}
	}

	if err := os.MkdirAll(e.Dir, 0o700); err != nil {
		return Reply{}, fmt.Errorf("daemon: %w", err)
	}

	lease, err := e.startLease(ctx)
	if err != nil {
		return Reply{}, err
	}
	defer lease.Release()

	if rep, err := e.hello(ctx); err == nil && rep.Version == e.Version {
		return rep, nil
	}

	spawn := e.Spawn
	if spawn == nil {
		spawn = e.spawn
	}

	if err := spawn(ctx); err != nil {
		return Reply{}, fmt.Errorf("starting the f10 daemon: %w", err)
	}

	deadline := time.Now().Add(e.StartWait)
	for {
		rep, err := e.hello(ctx)
		if err == nil {
			return rep, nil
		}

		if time.Now().After(deadline) {
			return Reply{}, fmt.Errorf("the f10 daemon did not start within %s: see %s", e.StartWait, LogPath(e.Dir))
		}

		if err := sleep(ctx, 50*time.Millisecond); err != nil {
			return Reply{}, err
		}
	}
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
