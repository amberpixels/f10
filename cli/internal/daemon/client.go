package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
)

// SocketURL is the base every call on the socket goes to; the host is a
// placeholder, since the transport dials the socket whatever it names.
const SocketURL = "http://f10-daemon"

// A Client calls the daemon listening in a run dir.
type Client struct {
	Dir    string
	Daemon f10v1connect.DaemonServiceClient
	Runs   f10v1connect.RunServiceClient
}

// Dial is the client of the daemon in dir; nothing is dialled until a call.
func Dial(dir string) *Client {
	hc := SocketHTTP(dir)

	return &Client{
		Dir:    dir,
		Daemon: f10v1connect.NewDaemonServiceClient(hc, SocketURL),
		Runs:   f10v1connect.NewRunServiceClient(hc, SocketURL),
	}
}

// SocketHTTP is an HTTP client whose every request goes to the socket in dir.
func SocketHTTP(dir string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: time.Second}

			return d.DialContext(ctx, "unix", SocketPath(dir))
		},
		DisableKeepAlives: true,
	}}
}

// listening reports whether anything accepts on the socket, so a refused
// dial reads as no daemon rather than as a protocol error.
func listening(ctx context.Context, dir string) error {
	d := net.Dialer{Timeout: time.Second}

	conn, err := d.DialContext(ctx, "unix", SocketPath(dir))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotRunning, err)
	}

	return conn.Close()
}

// Hello asks the daemon in dir who it is. A daemon serving another API
// version answers, and APIError says so; one that speaks only the old JSON
// protocol answers through it, marked Legacy.
func Hello(ctx context.Context, dir, version string) (Info, error) {
	if err := listening(ctx, dir); err != nil {
		return Info{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	res, err := Dial(dir).Daemon.Hello(ctx, connect.NewRequest(&f10v1.HelloRequest{
		Version:    version,
		ApiVersion: APIVersion,
	}))
	if err != nil {
		if info, lerr := legacyHello(ctx, dir); lerr == nil {
			return info, nil
		}

		return Info{}, fmt.Errorf("f10 daemon hello: %w", err)
	}

	m := res.Msg

	return Info{
		Version:  m.GetVersion(),
		API:      int(m.GetApiVersion()),
		PID:      int(m.GetPid()),
		Started:  m.GetStartedAt().AsTime(),
		Stopping: m.GetStopping(),
	}, nil
}

// APIError is a daemon kept by the release rule whose API this client does
// not speak: the two say so instead of failing on a decode.
func APIError(info Info, version string) error {
	if info.Legacy || info.API == APIVersion {
		return nil
	}

	if info.API > APIVersion {
		return fmt.Errorf(
			"the f10 daemon (%s) serves API v%d and this f10 (%s) speaks v%d: update f10",
			info.Version, info.API, version, APIVersion)
	}

	return fmt.Errorf(
		"the f10 daemon (%s) serves API v%d and this f10 (%s) speaks v%d: f10 daemon stop replaces it",
		info.Version, info.API, version, APIVersion)
}

// GetStatus is who the daemon in dir is, and what it runs and watches.
func GetStatus(ctx context.Context, dir, version string) (Status, error) {
	info, err := Hello(ctx, dir, version)
	if err != nil {
		return Status{}, err
	}

	if info.Legacy {
		return Status{}, errors.New(legacyStatusError(info.Version))
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	res, err := Dial(dir).Daemon.GetDaemonStatus(ctx, connect.NewRequest(&f10v1.GetDaemonStatusRequest{}))
	if err != nil {
		return Status{}, fmt.Errorf("f10 daemon status: %w", err)
	}

	st := Status{Info: info}

	for _, d := range res.Msg.GetBatches() {
		st.Runs = append(st.Runs, Run{
			Root: d.GetRoot(), Dir: d.GetDir(), Drive: int(d.GetBatch()), PID: int(d.GetPid()),
			Since: d.GetSince().AsTime(), Log: d.GetLog(),
		})
	}

	// one-release alias: the drives field of a daemon one release older
	// status asks whatever daemon runs, and one a release older fills only
	// the old field
	if len(st.Runs) == 0 {
		for _, d := range res.Msg.GetDrives() { //nolint:staticcheck // read from an older daemon
			st.Runs = append(st.Runs, Run{
				Root: d.GetRoot(), Dir: d.GetDir(), Drive: int(d.GetDrive()), PID: int(d.GetPid()),
				Since: d.GetSince().AsTime(), Log: d.GetLog(),
			})
		}
	}

	for _, w := range res.Msg.GetWatches() {
		wt := Watch{Root: w.GetRoot(), Dir: w.GetDir(), Exit: int(w.GetExit()), Log: w.GetLog()}
		if w.GetLastPassAt() != nil {
			wt.Last = w.GetLastPassAt().AsTime()
		}

		st.Watches = append(st.Watches, wt)
	}

	if r := res.Msg.GetRemote(); r != nil {
		st.Remote = RemoteState{
			Enabled: r.GetEnabled(), Addr: r.GetAddr(), Reason: r.GetReason(),
			URL: r.GetUrl(), HTTPS: r.GetHttps(), HTTPSReason: r.GetHttpsReason(),
		}
	}

	return st, nil
}

// Stop stops the daemon in dir: drain lets each drive reach its next tick,
// and upgrade hands the drained drives to the next daemon.
func Stop(ctx context.Context, dir string, drain, upgrade bool) error {
	if err := listening(ctx, dir); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	_, err := Dial(dir).Daemon.StopDaemon(ctx, connect.NewRequest(&f10v1.StopDaemonRequest{
		Drain:   drain,
		Upgrade: upgrade,
	}))
	if err != nil {
		if lerr := legacyStop(ctx, dir, drain, upgrade); lerr == nil {
			return nil
		}

		return fmt.Errorf("f10 daemon stop: %w", err)
	}

	return nil
}

// WatchProject hands the project at root, its main checkout dir, to the
// daemon's watch passes, and returns the pass log.
func WatchProject(ctx context.Context, runDir, root, dir string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	res, err := Dial(runDir).Daemon.WatchProject(ctx, connect.NewRequest(&f10v1.WatchProjectRequest{
		Root: root,
		Dir:  dir,
		Env:  env,
	}))
	if err != nil {
		return "", fmt.Errorf("f10 daemon watch: %w", err)
	}

	return res.Msg.GetLog(), nil
}

// A DriveCall is one drive for the daemon to run, or to attach to.
type DriveCall struct {
	Args   []string // the drive's arguments, as f10 start takes them
	Dir    string   // where the drive was typed
	Env    []string // the allowlisted environment
	Resume int      // the record --resume names
}

// Started is a drive the daemon runs: its log, and whether the call joined
// one already running.
type Started struct {
	Log    string
	Attach bool
	Drive  int
}

// RunDrive asks the daemon in dir to run the drive, or to attach to it.
func RunDrive(ctx context.Context, dir string, c DriveCall) (Started, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	req := &f10v1.StartBatchRequest{
		Args:   c.Args,
		Dir:    c.Dir,
		Env:    c.Env,
		Resume: int32(c.Resume), //nolint:gosec // a batch number
	}

	res, err := Dial(dir).Runs.StartBatch(ctx, connect.NewRequest(req))
	if err != nil {
		return Started{}, plainError(err)
	}

	return Started{Log: res.Msg.GetLog(), Attach: res.Msg.GetAttached(), Drive: int(res.Msg.GetBatch())}, nil
}

// ResumeCall is the call that picks drive n up again.
func ResumeCall(dir string, env []string, n int) DriveCall {
	return DriveCall{Args: []string{"--resume", strconv.Itoa(n)}, Dir: dir, Env: env, Resume: n}
}

// plainError is a refusal the daemon worded, without Connect's code prefix,
// so the user reads the daemon's sentence as the old protocol printed it.
func plainError(err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) && ce.Message() != "" && connect.CodeOf(err) != connect.CodeUnavailable {
		return errors.New(ce.Message())
	}

	return err
}

// LocalEnv is the environment a request passes, honored only from the unix
// socket; a request from any other listener runs under the daemon's session.
func LocalEnv(req connect.AnyRequest, env []string) []string {
	if !IsLocal(req) {
		return nil
	}

	return env
}

// IsLocal reports whether req came over the unix socket, or from this
// process: a unix socket's peer has no network address.
func IsLocal(req connect.AnyRequest) bool {
	addr := req.Peer().Addr

	return addr == "" || addr == "@"
}
