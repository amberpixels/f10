package config

import (
	"context"
	"errors"
	"os"
	"runtime"

	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/doctor"
	"github.com/amberpixels/f10/cli/internal/remote"
)

// remoteState reads the tailnet listener and the service for doctor; it
// asks a running daemon and never starts one.
func remoteState(ctx context.Context, home string) *doctor.RemoteState {
	st := &doctor.RemoteState{}

	if dir, err := remote.Dir(); err != nil {
		st.ConfigErr = err.Error()
	} else if c, err := remote.ReadConfig(dir); err != nil {
		st.ConfigErr = err.Error()
	} else {
		st.Enabled, st.Port = c.Enabled, c.ListenPort()
	}

	if ip, ok := remote.TailnetAddr(remote.Interfaces); ok {
		st.Tailnet = ip.String()
	}

	ds, err := daemon.GetStatus(ctx, daemon.RunDir(), buildinfo.Version())
	if err == nil {
		st.DaemonRunning, st.Bound, st.Reason = true, ds.Remote.Addr, ds.Remote.Reason
		st.URL, st.HTTPSReason = ds.Remote.URL, ds.Remote.HTTPSReason
	} else if !errors.Is(err, daemon.ErrNotRunning) {
		st.DaemonRunning, st.Reason = true, err.Error()
	}

	svc := &remote.Service{GOOS: runtime.GOOS, Home: home}
	if path, err := svc.Path(); err == nil {
		st.ServiceFile = path
		st.ServiceProgram, st.ServiceInstalled = svc.InstalledProgram()

		if st.ServiceInstalled {
			if _, err := os.Stat(st.ServiceProgram); err != nil {
				st.ProgramMissing = true
			}
		}
	}

	return st
}
