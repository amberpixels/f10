//go:build unix

package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
)

// Supported reports whether this platform runs a daemon; elsewhere drive
// runs in the foreground.
const Supported = true

// ExitDrained is a drive's exit when it stopped for an upgrade, to be
// resumed by the next daemon; drive.go's exitDrained.
const ExitDrained = 6

// ErrRunning is a second daemon finding the first one's lease held.
var ErrRunning = errors.New("an f10 daemon is already running")

// A Server supervises drive processes for one user.
type Server struct {
	Dir     string // the run dir
	Version string
	// Child is the command a drive's argv runs as: by default the daemon's
	// own binary, `drive --foreground <argv>`.
	Child     func(argv []string) (string, []string)
	Idle      time.Duration // no drive and no call this long: exit
	DrainWait time.Duration // how long a drain waits for drives before SIGTERM
	Tick      time.Duration // how often idleness is checked
	Log       *log.Logger

	mu       sync.Mutex
	runs     []*child
	last     time.Time
	started  time.Time
	draining bool
	drained  []drained
	ln       net.Listener
	ctx      context.Context //nolint:containedctx // the serve call's, for the drives it starts
	stop     chan bool       // true for a drain
	seq      int
}

type child struct {
	Run

	key  string // the record's command line, joined
	env  []string
	cmd  *exec.Cmd
	done chan struct{}
}

// drained is a drive a drain stopped, for the next daemon to resume.
type drained struct {
	Root  string   `json:"root"`
	Dir   string   `json:"dir"`
	Drive int      `json:"drive"`
	Env   []string `json:"env"`
}

func logsDir(dir string) string { return filepath.Join(dir, "logs") }

func drainedPath(dir string) string { return filepath.Join(dir, "drained.json") }

// Serve holds the daemon's lease, listens, and supervises until it is idle,
// stopped, or ctx ends (a drain).
func (s *Server) Serve(ctx context.Context) error {
	s.Idle = cmp.Or(s.Idle, 10*time.Minute)
	s.DrainWait = cmp.Or(s.DrainWait, 10*time.Minute)
	s.Tick = cmp.Or(s.Tick, time.Minute)

	if s.Log == nil {
		s.Log = log.New(io.Discard, "", 0)
	}

	if s.Child == nil {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("daemon: %w", err)
		}

		s.Child = func(argv []string) (string, []string) {
			return exe, append([]string{"drive", "--foreground"}, argv...)
		}
	}

	if err := os.MkdirAll(logsDir(s.Dir), 0o700); err != nil {
		return fmt.Errorf("daemon: %w", err)
	}

	_ = os.Chmod(s.Dir, 0o700) //nolint:gosec // a directory needs its x bit; 0700 keeps it to this user

	own, err := drives.Acquire(s.Dir, "daemon", 0)
	if held := (drives.HeldError{}); errors.As(err, &held) {
		return ErrRunning
	}

	if err != nil {
		return err
	}
	defer own.Release()

	sock := SocketPath(s.Dir)
	_ = os.Remove(sock) // the lease is ours, so any socket left is a dead daemon's

	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", sock)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}

	_ = os.Chmod(sock, 0o600)

	s.ln, s.stop, s.ctx = ln, make(chan bool, 1), ctx
	s.started, s.last = time.Now(), time.Now()
	s.Log.Printf("daemon %s started, pid %d", s.Version, os.Getpid())

	s.resumeDrained()

	go s.accept()

	tick := time.NewTicker(s.Tick)
	defer tick.Stop()

	drain := true

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case drain = <-s.stop:
			break loop
		case <-tick.C:
			if s.idle() {
				s.Log.Printf("idle for %s: exiting", s.Idle)

				break loop
			}
		}
	}

	_ = ln.Close()
	_ = os.Remove(sock)

	s.shutdown(drain)
	s.Log.Printf("daemon %s stopped", s.Version)

	return nil
}

func (s *Server) idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.runs) == 0 && time.Since(s.last) >= s.Idle
}

func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}

		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(callTimeout))

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}

	s.mu.Lock()
	s.last = time.Now()
	s.mu.Unlock()

	rep := s.reply(req)
	rep.Version, rep.PID, rep.Started = s.Version, os.Getpid(), s.started

	_ = conn.SetWriteDeadline(time.Now().Add(callTimeout))
	_ = json.NewEncoder(conn).Encode(rep)
}

func (s *Server) reply(req Request) Reply {
	switch req.Op {
	case OpHello:
		return Reply{}
	case OpRun:
		return s.run(req)
	case OpStatus:
		return Reply{Runs: s.list()}
	case OpStop:
		s.mu.Lock()
		s.draining = true
		s.mu.Unlock()

		select {
		case s.stop <- req.Drain:
		default:
		}

		return Reply{}
	}

	return Reply{Error: "unknown daemon op " + strconv.Quote(req.Op)}
}

// run attaches to the drive already running this record, else starts it.
func (s *Server) run(req Request) Reply {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.draining {
		return Reply{Error: "the daemon is stopping: run the command again"}
	}

	for _, c := range s.runs {
		if c.Root == req.Root && ((req.Key != "" && c.key == req.Key) || (req.Resume > 0 && c.Drive == req.Resume)) {
			return Reply{Log: c.Log, Attach: true}
		}
	}

	c, err := s.start(req.Root, req.Dir, req.Argv, req.Env)
	if err != nil {
		return Reply{Error: err.Error()}
	}

	c.key = req.Key

	return Reply{Log: c.Log}
}

// start runs one drive process; the caller holds mu.
func (s *Server) start(root, dir string, argv, env []string) (*child, error) {
	s.seq++

	file := fmt.Sprintf("%s-%d-%d.log", time.Now().Format("20060102-150405"), os.Getpid(), s.seq)
	path := filepath.Join(logsDir(s.Dir), file)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("drive log: %w", err)
	}

	name, args := s.Child(argv)

	c := &child{env: env, done: make(chan struct{})}
	c.Root, c.Dir, c.Since, c.Log = root, dir, time.Now(), path
	out := &numberWriter{w: f, found: func(n int) { s.numbered(c, n) }}

	// a drive outlives the call that started it, and stops by its own signals
	cmd := exec.CommandContext(context.WithoutCancel(s.ctx), name, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, out, out

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)

		return nil, fmt.Errorf("starting the drive: %w", err)
	}

	c.cmd, c.PID = cmd, cmd.Process.Pid
	s.runs = append(s.runs, c)
	s.Log.Printf("drive started in %s, pid %d, log %s", dir, c.PID, path)

	go s.reap(c, f)

	return c, nil
}

// numbered records the drive number the child printed, and the record's
// command line, so another client of the same drive attaches.
func (s *Server) numbered(c *child, n int) {
	key := ""
	if rec, err := drives.Read(c.Root, n); err == nil {
		key = strings.Join(rec.Args, " ")
	}

	s.mu.Lock()
	c.Drive = n
	if key != "" {
		c.key = key
	}
	s.mu.Unlock()

	idx, err := os.OpenFile(filepath.Join(logsDir(s.Dir), "index"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}

	_, _ = fmt.Fprintf(idx, "%s\t%d\t%s\n", c.Root, n, c.Log)
	_ = idx.Close()
}

func (s *Server) reap(c *child, f *os.File) {
	err := c.cmd.Wait()
	_ = f.Close()

	code := 0
	if exit := (&exec.ExitError{}); errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		code = 1
	}

	_ = os.WriteFile(ExitPath(c.Log), []byte(strconv.Itoa(code)+"\n"), 0o600)

	s.mu.Lock()
	s.runs = slices.DeleteFunc(s.runs, func(o *child) bool { return o == c })
	s.last = time.Now()

	if code == ExitDrained && c.Drive > 0 {
		s.drained = append(s.drained, drained{Root: c.Root, Dir: c.Dir, Drive: c.Drive, Env: c.env})
	}

	n := c.Drive
	s.mu.Unlock()

	s.Log.Printf("drive %d (pid %d) exited %d", n, c.PID, code)
	close(c.done)
}

func (s *Server) list() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Run, 0, len(s.runs))
	for _, c := range s.runs {
		out = append(out, c.Run)
	}

	return out
}

// FindLog is drive n's newest log under root, from the index; "" for none.
func FindLog(dir, root string, n int) string {
	raw, err := os.ReadFile(filepath.Join(logsDir(dir), "index"))
	if err != nil {
		return ""
	}

	found := ""

	for line := range strings.SplitSeq(string(raw), "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 3 && f[0] == root && f[1] == strconv.Itoa(n) {
			found = f[2]
		}
	}

	return found
}

// shutdown ends every drive: a drain asks each to stop at its next tick
// (SIGUSR1) and keeps the ones that did for the next daemon; otherwise,
// or past DrainWait, SIGTERM.
func (s *Server) shutdown(drain bool) {
	s.mu.Lock()
	s.draining = true
	runs := slices.Clone(s.runs)
	s.mu.Unlock()

	sig := syscall.SIGTERM
	if drain {
		sig = syscall.SIGUSR1
	}

	for _, c := range runs {
		_ = c.cmd.Process.Signal(sig)
	}

	deadline := time.After(s.DrainWait)

	for _, c := range runs {
		select {
		case <-c.done:
		case <-deadline:
			_ = c.cmd.Process.Signal(syscall.SIGTERM)
			<-c.done
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.drained) == 0 {
		return
	}

	raw, err := json.Marshal(s.drained)
	if err == nil {
		err = os.WriteFile(drainedPath(s.Dir), raw, 0o600)
	}

	if err != nil {
		s.Log.Printf("keeping drained drives: %v", err)
	}
}

// resumeDrained starts every drive a drained predecessor left.
func (s *Server) resumeDrained() {
	path := drainedPath(s.Dir)

	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}

	_ = os.Remove(path)

	var list []drained
	if err := json.Unmarshal(raw, &list); err != nil {
		s.Log.Printf("drained drives unreadable: %v", err)

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, d := range list {
		c, err := s.start(d.Root, d.Dir, []string{"--resume", strconv.Itoa(d.Drive)}, d.Env)
		if err != nil {
			s.Log.Printf("resuming drive %d in %s: %v", d.Drive, d.Dir, err)

			continue
		}

		c.Drive = d.Drive
	}
}

var driveLine = regexp.MustCompile(`^drive (\d+)$`)

// numberWriter copies a drive's output to its log and reports the number
// the drive prints on its `drive <n>` line, within its first lines.
type numberWriter struct {
	w     io.Writer
	found func(int)

	mu   sync.Mutex
	head []byte
	done bool
}

func (n *numberWriter) Write(p []byte) (int, error) {
	n.mu.Lock()
	if !n.done {
		n.head = append(n.head, p...)

		for {
			i := bytes.IndexByte(n.head, '\n')
			if i < 0 {
				break
			}

			if m := driveLine.FindSubmatch(bytes.TrimSpace(n.head[:i])); m != nil {
				num, _ := strconv.Atoi(string(m[1]))
				n.done = true

				go n.found(num)

				break
			}

			n.head = n.head[i+1:]
		}

		if n.done || len(n.head) > 8<<10 {
			n.done, n.head = true, nil
		}
	}
	n.mu.Unlock()

	return n.w.Write(p)
}
