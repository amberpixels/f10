package doctor

import (
	"fmt"
	"regexp"
	"strings"
)

// RemoteState is what the remote check reads: the listener's setting, the
// Tailscale address, what the running daemon bound, and the service.
type RemoteState struct {
	Enabled   bool
	Port      int
	ConfigErr string // the setting does not read

	Tailnet string // this machine's Tailscale address; "" when Tailscale is down

	DaemonRunning bool
	Bound         string // the address the daemon listens on
	Reason        string // why it does not
	URL           string // the web client's address while bound
	HTTPSReason   string // why the listener serves plain HTTP

	ServiceFile      string // "" on a platform with no service
	ServiceInstalled bool
	ServiceProgram   string
	ProgramMissing   bool // the program the service runs is gone
}

// releaseDir is a path segment naming a release, as a marketplace plugin's
// versioned install directory does.
var releaseDir = regexp.MustCompile(`/v?(\d+\.\d+\.\d+)/`)

// pinnedRelease is the release a program's path pins, "" for a path that
// names none and so follows upgrades.
func pinnedRelease(program string) string {
	m := releaseDir.FindAllStringSubmatch(program, -1)
	if m == nil {
		return ""
	}

	return m[len(m)-1][1]
}

// remoteScope is what a remote problem affects: only remote calls.
var remoteScope = []string{"remote"}

func checkRemote(env *Env) Result {
	r := Result{Name: CheckRemote}

	st := env.Remote
	if st == nil || (!st.Enabled && !st.ServiceInstalled && st.ConfigErr == "") {
		r.Status = StatusNotNeeded
		r.Detail = "the tailnet listener is off and no service is installed (f10 remote on)"

		return r
	}

	problem := func(detail, remedy string) Result {
		r.Status, r.Scope, r.Detail, r.Remedy = StatusProblem, remoteScope, detail, remedy

		return r
	}

	switch {
	case st.ConfigErr != "":
		return problem(st.ConfigErr, "fix the file, or delete it and run f10 remote on")
	case st.ServiceInstalled && st.ProgramMissing:
		return problem("the service runs "+st.ServiceProgram+", which is gone", "f10 remote install")
	case st.ServiceInstalled && older(pinnedRelease(st.ServiceProgram), env.Version):
		return problem(fmt.Sprintf("the service runs %s, the %s release, and this f10 is %s",
			st.ServiceProgram, pinnedRelease(st.ServiceProgram), env.Version), "f10 remote install")
	case st.Enabled && st.Tailnet == "":
		return problem("the listener is on, but this machine has no Tailscale address", "start Tailscale: tailscale up")
	case st.Enabled && st.DaemonRunning && st.Bound == "":
		return problem("the daemon is not listening on the tailnet: "+st.Reason, "f10 daemon logs")
	case st.Bound != "" && st.HTTPSReason != "":
		return problem("the web client serves plain HTTP, so it does not install or notify: "+st.HTTPSReason,
			"enable HTTPS certificates on the DNS page of the tailnet's admin console")
	}

	var parts []string

	if st.Enabled {
		parts = append(parts, fmt.Sprintf("listener on, port %d", st.Port))
	} else {
		parts = append(parts, "listener off")
	}

	if st.Tailnet != "" {
		parts = append(parts, "Tailscale "+st.Tailnet)
	}

	switch {
	case st.URL != "":
		parts = append(parts, "web client at "+st.URL)
	case st.Bound != "":
		parts = append(parts, "daemon listening on "+st.Bound)
	case !st.DaemonRunning:
		parts = append(parts, "no daemon running")
	}

	switch {
	case st.ServiceInstalled:
		parts = append(parts, "service "+st.ServiceFile)
	case st.ServiceFile != "":
		parts = append(parts, "no service: f10 remote install starts the daemon after a reboot")
	}

	r.Status, r.Detail = StatusOK, strings.Join(parts, "; ")

	return r
}
