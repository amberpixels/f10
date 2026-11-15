//go:build unix

package daemon

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// call sends hello, status, stop and watch over the socket; a run goes to
// the registered server directly, with the test's attach key, because the
// handlers that resolve a drive's project import this package.
type request struct {
	Op      string
	Argv    []string
	Dir     string
	Root    string
	Env     []string
	Key     string
	Resume  int
	Drain   bool
	Upgrade bool
}

type reply struct {
	Log      string
	Attach   bool
	Stopping bool
	Runs     []Run
	Watches  []Watch
}

const (
	opHello  = "hello"
	opRun    = "run"
	opStatus = "status"
	opStop   = "stop"
	opWatch  = "watch"
)

var servers sync.Map // run dir -> *Server

func register(s *Server) { servers.Store(s.Dir, s) }

func call(ctx context.Context, dir string, req request, _ time.Duration) (reply, error) {
	switch req.Op {
	case opHello:
		info, err := Hello(ctx, dir, "test")

		return reply{Stopping: info.Stopping}, err
	case opStatus:
		st, err := GetStatus(ctx, dir, "test")

		return reply{Runs: st.Runs, Watches: st.Watches, Stopping: st.Stopping}, err
	case opStop:
		return reply{}, Stop(ctx, dir, req.Drain, req.Upgrade)
	case opWatch:
		log, err := WatchProject(ctx, dir, req.Root, req.Dir, req.Env)

		return reply{Log: log}, err
	case opRun:
		v, ok := servers.Load(dir)
		if !ok {
			return reply{}, fmt.Errorf("%w: no test server in %s", ErrNotRunning, dir)
		}

		st, err := v.(*Server).run(DriveSpec{
			Argv: req.Argv, Dir: req.Dir, Root: req.Root, Env: req.Env, Resume: req.Resume,
		}, req.Key)

		return reply{Log: st.Log, Attach: st.Attach}, err
	}

	return reply{}, fmt.Errorf("unknown op %q", req.Op)
}

func resumeRequest(root, dir string, env []string, n int) request {
	return request{Op: opRun, Argv: []string{"--resume", strconv.Itoa(n)}, Dir: dir, Root: root, Env: env, Resume: n}
}
