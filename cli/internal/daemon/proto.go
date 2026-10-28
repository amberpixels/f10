// Package daemon runs drives for one user, outside any session: `f10 drive`
// starts the daemon when none listens, hands it the command line, and tails
// the drive's log. Each drive is its own child process,
// `f10 drive --foreground`, run in the client's directory with the client's
// environment, so leases and crash recovery stay the drive's own.
package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// RunDir holds the socket, the locks, the logs and the drained runs:
// F10_RUN_DIR, else $XDG_RUNTIME_DIR/f10, else ~/.f10/run.
func RunDir() string {
	if d := os.Getenv("F10_RUN_DIR"); d != "" {
		return d
	}

	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "f10")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".f10", "run")
}

// SocketPath is where the daemon listens.
func SocketPath(dir string) string { return filepath.Join(dir, "daemon.sock") }

// LogPath is the daemon's own log.
func LogPath(dir string) string { return filepath.Join(dir, "daemon.log") }

// ExitPath is the file holding a drive log's exit code, once its process ended.
func ExitPath(log string) string { return log + ".exit" }

// ReadExit is the exit code a drive's log ended with, false while it runs.
func ReadExit(log string) (int, bool) {
	raw, err := os.ReadFile(ExitPath(log))
	if err != nil {
		return 0, false
	}

	code, err := strconv.Atoi(string(trimNL(raw)))

	return code, err == nil
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}

	return b
}

// Ops a Request names.
const (
	OpHello  = "hello"
	OpRun    = "run"
	OpStatus = "status"
	OpStop   = "stop"
)

// A Request is one call: an op and what it needs.
type Request struct {
	Op      string   `json:"op"`
	Version string   `json:"version,omitempty"` // hello: the client's
	Argv    []string `json:"argv,omitempty"`    // run: drive's arguments, --foreground added by the daemon
	Dir     string   `json:"dir,omitempty"`     // run: where the drive was typed
	Root    string   `json:"root,omitempty"`    // run: the storage root, telling projects apart
	Env     []string `json:"env,omitempty"`     // run: the allowlisted environment
	Key     string   `json:"key,omitempty"`     // run: the record's command line, joined; "" never attaches
	Resume  int      `json:"resume,omitempty"`  // run: the record --resume names
	Drain   bool     `json:"drain,omitempty"`   // stop: let each drive reach a tick boundary
}

// A Reply answers one Request; Error set means it was refused.
type Reply struct {
	Error   string    `json:"error,omitempty"`
	Version string    `json:"version,omitempty"`
	PID     int       `json:"pid,omitempty"`
	Started time.Time `json:"started,omitzero"`
	Log     string    `json:"log,omitempty"`    // run, logs: the drive's log
	Attach  bool      `json:"attach,omitempty"` // run: the log is a drive already running
	Runs    []Run     `json:"runs,omitempty"`   // status
}

// A Run is one drive process the daemon supervises.
type Run struct {
	Root  string    `json:"root"`
	Dir   string    `json:"dir"`
	Drive int       `json:"drive,omitempty"` // 0 until the drive printed its number
	PID   int       `json:"pid"`
	Since time.Time `json:"since"`
	Log   string    `json:"log"`
}

// ErrNotRunning is a socket nobody listens on.
var ErrNotRunning = errors.New("no f10 daemon is running")

// callTimeout bounds one call; only a drain's stop waits longer.
const callTimeout = 10 * time.Second

// Call sends req to the daemon in dir and reads its reply.
func Call(ctx context.Context, dir string, req Request, wait time.Duration) (Reply, error) {
	d := net.Dialer{Timeout: time.Second}

	conn, err := d.DialContext(ctx, "unix", SocketPath(dir))
	if err != nil {
		return Reply{}, fmt.Errorf("%w: %w", ErrNotRunning, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(cmp.Or(wait, callTimeout)))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Reply{}, fmt.Errorf("daemon %s: %w", req.Op, err)
	}

	var rep Reply
	if err := json.NewDecoder(conn).Decode(&rep); err != nil {
		return Reply{}, fmt.Errorf("daemon %s: %w", req.Op, err)
	}

	if rep.Error != "" {
		return rep, errors.New(rep.Error)
	}

	return rep, nil
}
