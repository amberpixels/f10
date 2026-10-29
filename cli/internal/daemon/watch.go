//go:build unix

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// A watcher is one watched project, and the pass running for it, if any.
type watcher struct {
	Watch

	env   []string
	cmd   *exec.Cmd // nil between passes
	done  chan struct{}
	rerun bool // registered again while a pass ran, which may predate the new task
	fails int  // passes in a row that failed
}

// watchedProject is a watcher as watch.json keeps it, env included, so a
// restarted daemon goes on watching with no client.
type watchedProject struct {
	Root string    `json:"root"`
	Dir  string    `json:"dir"`
	Env  []string  `json:"env"`
	Last time.Time `json:"last,omitzero"`
	Exit int       `json:"exit"`
}

func watchPath(dir string) string { return filepath.Join(dir, "watch.json") }

const watchFailMax = 10

// watchLogMax caps a project's watch log: a pass appends a few lines every
// few minutes for as long as the project has tasks.
const watchLogMax = 1 << 20

func watchLog(dir, root string) string {
	sum := sha256.Sum256([]byte(root))

	return filepath.Join(logsDir(dir), "watch-"+hex.EncodeToString(sum[:6])+".log")
}

// watch adds or refreshes a project, newest env winning, and starts a
// pass for it now, so a task just started is seen without waiting a tick.
func (s *Server) watch(req Request) Reply {
	if req.Root == "" || req.Dir == "" {
		return Reply{Error: "watch needs the storage root and the main checkout"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	i := slices.IndexFunc(s.watches, func(w *watcher) bool { return w.Root == req.Root })
	if i < 0 {
		w := &watcher{}
		w.Root, w.Log = req.Root, watchLog(s.Dir, req.Root)
		s.watches = append(s.watches, w)
		i = len(s.watches) - 1
		s.Log.Printf("watching %s", req.Dir)
	}

	w := s.watches[i]
	w.Dir, w.env, w.fails = req.Dir, req.Env, 0
	w.rerun = w.cmd != nil

	s.saveWatches()

	if !s.draining {
		s.startPass(w)
	}

	return Reply{Log: w.Log}
}

func (s *Server) passDue(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.draining {
		return
	}

	for _, w := range s.watches {
		if w.cmd == nil && now.Sub(w.Last) >= s.WatchEvery {
			s.startPass(w)
		}
	}
}

// startPass runs one pass; the caller holds mu. A failed start is retried
// next tick.
func (s *Server) startPass(w *watcher) {
	if w.cmd != nil {
		return
	}

	if fi, err := os.Stat(w.Log); err == nil && fi.Size() > watchLogMax {
		_ = os.Truncate(w.Log, 0)
	}

	f, err := os.OpenFile(w.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.Log.Printf("watch log for %s: %v", w.Dir, err)

		return
	}

	_, _ = fmt.Fprintf(f, "--- pass %s\n", time.Now().Format(time.DateTime))

	name, args := s.Pass(w.Dir)

	// a pass may be finishing a task: it ends on its own, never with a call
	cmd := exec.CommandContext(context.WithoutCancel(s.ctx), name, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.Env = w.Dir, f, f, slices.Clone(w.env)

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		w.Last, w.Exit = time.Now(), -1
		s.Log.Printf("starting a watch pass in %s: %v", w.Dir, err)
		s.failed(w)
		s.saveWatches()

		return
	}

	done := make(chan struct{})
	w.cmd, w.done = cmd, done

	go s.reapPass(w, cmd, done, f)
}

// reapPass ends one pass; cmd and done are that pass's, since the next
// pass replaces w's once this one lets go of mu.
func (s *Server) reapPass(w *watcher, cmd *exec.Cmd, done chan struct{}, f *os.File) {
	err := cmd.Wait()
	_ = f.Close()

	code := 0
	if exit := (&exec.ExitError{}); errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		code = 1
	}

	s.mu.Lock()
	w.cmd, w.Last, w.Exit = nil, time.Now(), code
	s.last = time.Now()
	rerun := w.rerun
	w.rerun = false

	switch {
	case code == 0:
		w.fails = 0
	case code == ExitNothingWatched && !rerun:
		s.drop(w, "nothing left to watch")
	case code == ExitNothingWatched:
		w.fails = 0
	default:
		s.Log.Printf("watch pass in %s exited %d: %s", w.Dir, code, w.Log)
		s.failed(w)
	}

	s.saveWatches()

	// the pass that ran during a registration may have listed the project
	// before the new task: one more pass reads it
	if rerun && !s.draining && slices.Contains(s.watches, w) {
		s.startPass(w)
	}

	s.mu.Unlock()

	close(done)
}

// failed counts a failed pass, dropping w once hopeless; the caller holds mu.
func (s *Server) failed(w *watcher) {
	w.fails++

	if _, err := os.Stat(w.Dir); err != nil {
		s.drop(w, "its checkout is gone")
	} else if w.fails >= watchFailMax {
		s.drop(w, fmt.Sprintf("%d passes in a row failed, see %s", w.fails, w.Log))
	}
}

// drop stops watching w; the caller holds mu.
func (s *Server) drop(w *watcher, why string) {
	s.watches = slices.DeleteFunc(s.watches, func(o *watcher) bool { return o == w })
	s.Log.Printf("stopped watching %s: %s", w.Dir, why)
}

// saveWatches writes the list; the caller holds mu.
func (s *Server) saveWatches() {
	list := make([]watchedProject, 0, len(s.watches))
	for _, w := range s.watches {
		list = append(list, watchedProject{Root: w.Root, Dir: w.Dir, Env: w.env, Last: w.Last, Exit: w.Exit})
	}

	raw, err := json.Marshal(list)
	if err == nil {
		err = os.WriteFile(watchPath(s.Dir), raw, 0o600)
	}

	if err != nil {
		s.Log.Printf("keeping the watch list: %v", err)
	}
}

// loadWatches reads the list a previous daemon kept; passes start on the
// first tick.
func (s *Server) loadWatches() {
	raw, err := os.ReadFile(watchPath(s.Dir))
	if err != nil {
		return
	}

	var list []watchedProject
	if err := json.Unmarshal(raw, &list); err != nil {
		s.Log.Printf("watch list unreadable: %v", err)

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range list {
		w := &watcher{env: p.Env}
		w.Root, w.Dir, w.Last, w.Exit, w.Log = p.Root, p.Dir, p.Last, p.Exit, watchLog(s.Dir, p.Root)
		s.watches = append(s.watches, w)
	}
}

func (s *Server) watchList() []Watch {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Watch, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, w.Watch)
	}

	return out
}

func (s *Server) endPasses(deadline <-chan time.Time) {
	s.mu.Lock()

	type pass struct {
		cmd  *exec.Cmd
		done chan struct{}
	}

	var running []pass

	for _, w := range s.watches {
		if w.cmd != nil {
			running = append(running, pass{cmd: w.cmd, done: w.done})
		}
	}
	s.mu.Unlock()

	late := false

	for _, p := range running {
		if !late {
			select {
			case <-p.done:
				continue
			case <-s.escalate:
			case <-deadline:
				late = true
			}
		}

		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		<-p.done
	}
}
