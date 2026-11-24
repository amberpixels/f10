// Package apiclient is how f10's control-plane commands reach the API: the
// daemon on its unix socket, started when none runs, or the same handlers
// served in this process where no daemon runs (off unix) and in tests.
package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	"github.com/amberpixels/f10/cli/gen/agents/v1/agentsv1connect"
	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/agents/agentsrpc"
	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/printed"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
)

// Clients are the generated clients of every service a command calls.
type Clients struct {
	Runs   f10v1connect.RunServiceClient
	Tasks  f10v1connect.TaskServiceClient
	Board  f10v1connect.BoardServiceClient
	Agents agentsv1connect.AgentServiceClient
	Inbox  agentsv1connect.InboxServiceClient

	// inProcess: the handlers run here, in this process's environment, so
	// a caller passes none
	inProcess bool
}

// ensured is the one daemon check a process makes, however many calls it
// dials for.
var ensured struct {
	once sync.Once
	err  error
}

// Local serves the API in this process, over InProcess: the binary sets it,
// since the handlers sit above every command that calls them.
var Local func() http.Handler

// Dial is the clients of this user's daemon, started when none runs; in a
// test binary, or where no daemon runs, the handlers served in process.
func Dial(ctx context.Context) (*Clients, error) {
	if testing.Testing() || !daemon.Supported {
		if Local == nil {
			return nil, errors.New("this binary serves no API in process: it has no daemon to call either")
		}

		return over(&http.Client{Transport: handlerTransport{Local()}}, daemon.SocketURL, true), nil
	}

	dir := daemon.RunDir()

	ensured.once.Do(func() {
		_, ensured.err = (&daemon.Ensurer{Dir: dir, Version: buildinfo.Version()}).Ensure(ctx)
	})

	if ensured.err != nil {
		return nil, ensured.err
	}

	return over(daemon.SocketHTTP(dir), daemon.SocketURL, false), nil
}

//nolint:lostfield // a client per service over hc, not a conversion of it
func over(hc *http.Client, base string, local bool) *Clients {
	return &Clients{
		Runs:      f10v1connect.NewRunServiceClient(hc, base),
		Tasks:     f10v1connect.NewTaskServiceClient(hc, base),
		Board:     f10v1connect.NewBoardServiceClient(hc, base),
		Agents:    agentsv1connect.NewAgentServiceClient(hc, base),
		Inbox:     agentsv1connect.NewInboxServiceClient(hc, base),
		inProcess: local,
	}
}

// InboxIn is a client of the inbox in dir with d moving answers on,
// served in process: what a test of the inbox command calls instead of the
// daemon's inbox.
func InboxIn(dir string, d agents.Deliverer) agentsv1connect.InboxServiceClient {
	mux := http.NewServeMux()
	agentsrpc.Mount(mux, agentsrpc.Options{Deliverer: d, InboxDir: func() string { return dir }})

	return agentsv1connect.NewInboxServiceClient(&http.Client{Transport: handlerTransport{mux}}, daemon.SocketURL)
}

// handlerTransport serves each request with h in this process; its empty
// remote address reads as a local caller, as the unix socket's does.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)

	return rec.Result(), nil
}

// Caller is where cmd runs, for a request's caller context: its directory
// and -C checkout, its session and workspace, its environment when the
// handlers run in the daemon, and whether its output is a terminal.
func (c *Clients) Caller(cmd *cli.Command) (*f10v1.CallerContext, error) {
	out, err := c.BareCaller(cmd)
	if err != nil {
		return nil, err
	}

	dir, scoped, err := target.RootDir(cmd)
	if err != nil {
		return nil, err
	}

	if scoped {
		out.Checkout = dir
	}

	return out, nil
}

// BareCaller is Caller without the -C checkout, for a command that takes
// several and sends them itself.
func (c *Clients) BareCaller(cmd *cli.Command) (*f10v1.CallerContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}

	out := &f10v1.CallerContext{
		Cwd:         cwd,
		Session:     state.Session(),
		WorkspaceId: os.Getenv("HERDR_WORKSPACE_ID"),
		Terminal:    render.LinkTerminal(cmd.Writer),
	}

	if !c.inProcess {
		out.Env = os.Environ()
	}

	return out, nil
}

// AgentsCaller is the agents.v1 caller context for cmd.
func (c *Clients) AgentsCaller(cmd *cli.Command) *agentsv1.CallerContext {
	out := &agentsv1.CallerContext{Terminal: render.LinkTerminal(cmd.Writer)}
	if !c.inProcess {
		out.Env = os.Environ()
	}

	return out
}

// errNoView is a reply from a daemon older than this client's calls, which
// sends no view; it is kept by the release rule only between builds of one
// release.
var errNoView = errors.New("the running f10 daemon predates this f10's calls: f10 daemon stop replaces it")

// View reads a read reply's view into v.
func View(raw []byte, v any) error {
	if len(raw) == 0 {
		return errNoView
	}

	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("reading the daemon's reply: %w", err)
	}

	return nil
}

// Print writes p's stdout to w and its stderr to ew, stderr first: it holds
// what the call reported while it worked.
func Print(w, ew io.Writer, p *f10v1.Printed) {
	_, _ = io.WriteString(ew, p.GetStderr())
	_, _ = io.WriteString(w, p.GetStdout())
}

// Err is err as the command returns it, once what the call printed before
// failing is written: the daemon's sentence without Connect's code, and
// the exit code the command exited with in process.
func Err(err error, w, ew io.Writer) error {
	ce, ok := errors.AsType[*connect.Error](err)
	if !ok {
		return err
	}

	for _, d := range ce.Details() {
		if v, derr := d.Value(); derr == nil {
			if p, ok := v.(*f10v1.Printed); ok {
				Print(w, ew, p)
			}
		}
	}

	// the daemon being unreachable keeps its code, which says what is wrong
	if ce.Message() == "" ||
		(connect.CodeOf(err) == connect.CodeUnavailable && ce.Meta().Get(printed.ExitHeader) == "") {
		return err
	}

	if code, cerr := strconv.Atoi(ce.Meta().Get(printed.ExitHeader)); cerr == nil && code != 0 {
		return cli.Exit(ce.Message(), code)
	}

	if ce.Meta().Get(printed.KindHeader) == printed.KindOutside {
		return kindError{msg: ce.Message(), kind: agents.ErrNotInside}
	}

	return errors.New(ce.Message())
}

// kindError is a refusal in the daemon's words that still reads as its kind.
type kindError struct {
	msg  string
	kind error
}

func (e kindError) Error() string { return e.msg }

func (e kindError) Unwrap() error { return e.kind }

// InProcess is the supervisor of handlers served in this process: no
// daemon runs drives or watches here, and Herdr is this process's own.
type InProcess struct{}

func (InProcess) Status() daemon.Status { return daemon.Status{} }

func (InProcess) WatchProject(string, string, []string) (string, error) { return "", nil }

func (InProcess) RunDrive(daemon.DriveSpec) (daemon.Started, error) {
	return daemon.Started{}, errNoDaemon
}

func (InProcess) ResumeDrive(string, string, []string, int) (daemon.Started, error) {
	return daemon.Started{}, errNoDaemon
}

func (InProcess) SessionEnv() []string { return nil }

func (InProcess) Remember([]string) {}

var errNoDaemon = errors.New("no f10 daemon runs batches in this process: f10 start runs them")
