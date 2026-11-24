package daemon

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/storage"
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

	return filepath.Join(storage.UserDir(home), "run")
}

// SocketPath is where the daemon listens.
func SocketPath(dir string) string { return filepath.Join(dir, "daemon.sock") }

// LogPath is the daemon's own log.
func LogPath(dir string) string { return filepath.Join(dir, "daemon.log") }

// ExitPath is the file holding a drive log's exit, once its process ended.
func ExitPath(log string) string { return log + ".exit" }

// ExitResume is a drive's exit when an answer came as it stopped: the
// daemon that runs it starts it again with --resume.
const ExitResume = 8

// Why a drive exited ExitDrained or ExitResume.
const (
	ReasonUpgrade = "upgrade" // the next daemon resumes it
	ReasonStopped = "stopped" // f10 daemon stop: it waits for its next run
	ReasonResumed = "resumed" // this daemon started it again
)

// An Exit is how a drive's process ended: its code, why it drained, and
// its record number, 0 when it never opened one.
type Exit struct {
	Code   int
	Reason string
	Drive  int
}

// writeExit records exit beside the log, as `<code> <drive> [<reason>]`.
func writeExit(log string, exit Exit) error {
	body := strconv.Itoa(exit.Code) + " " + strconv.Itoa(exit.Drive)
	if exit.Reason != "" {
		body += " " + exit.Reason
	}

	return os.WriteFile(ExitPath(log), []byte(body+"\n"), 0o600)
}

// ReadExit is how a drive's log ended, false while it runs.
func ReadExit(log string) (Exit, bool) {
	raw, err := os.ReadFile(ExitPath(log))
	if err != nil {
		return Exit{}, false
	}

	f := strings.Fields(string(raw))
	if len(f) < 2 {
		return Exit{}, false
	}

	var exit Exit

	exit.Code, err = strconv.Atoi(f[0])
	if err != nil {
		return Exit{}, false
	}

	exit.Drive, _ = strconv.Atoi(f[1])
	if len(f) > 2 {
		exit.Reason = f[2]
	}

	return exit, true
}

// APIVersion is the control-plane API version this binary serves and
// calls; it rises only when a v2 package lands.
const APIVersion = 1

// Info is what Hello answers: which daemon runs, and whether it is stopping.
// Legacy is a daemon of a release before the Connect API, reached through
// its JSON protocol only to replace it.
type Info struct {
	Version  string
	API      int
	PID      int
	Started  time.Time
	Stopping bool
	Legacy   bool
}

// Status is what the daemon runs and watches, with who it is.
type Status struct {
	Info

	Runs    []Run
	Watches []Watch
	Remote  RemoteState
}

// A RemoteListener is the daemon's listener on the tailnet: the address to
// bind, the peers it accepts and the check in front of the API. The daemon
// command builds it, so this package knows nothing of tokens.
type RemoteListener interface {
	// Addr is the address to bind and whether the user's setting is on;
	// with no address, the third value says why none is wanted or possible.
	Addr() (addr string, on bool, why string)
	Listen(ln net.Listener) net.Listener
	Handler(h http.Handler) http.Handler
}

// A RemoteTLS listener serves HTTPS once it holds a certificate, because a
// browser installs a web app and shows notifications only over HTTPS.
type RemoteTLS interface {
	// TLS is the config to serve with and the host name its certificate
	// names; a nil config comes with why there is none, and the listener
	// serves plain HTTP meanwhile.
	TLS() (cfg *tls.Config, host string, why string)
}

// RemoteState is the tailnet listener as the daemon last reconciled it.
type RemoteState struct {
	Enabled     bool
	Addr        string // bound; "" when none is
	Reason      string // why none is bound
	URL         string // the web client's address while bound
	HTTPS       bool
	HTTPSReason string // why the listener serves plain HTTP
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

// A Watch is one project the daemon watches for tasks no drive owns: a
// `watch --pass` in its main checkout every few minutes.
type Watch struct {
	Root string    `json:"root"`
	Dir  string    `json:"dir"`           // the main checkout
	Last time.Time `json:"last,omitzero"` // when the last pass ended
	Exit int       `json:"exit"`          // its exit code
	Log  string    `json:"log"`           // the project's pass log
}

// ExitNothingWatched is a pass's exit when the project has no task left
// to watch: the daemon stops watching it.
const ExitNothingWatched = 7

// NumberFileEnv names the file a batch the daemon runs writes its record
// number to, once the record is open.
const NumberFileEnv = "F10_BATCH_FILE"

// ResumeEnv is set on every batch a daemon runs that starts an ExitResume
// again. A batch under an older daemon, which lacks it, names the resume
// command instead of exiting for a restart that never comes.
const ResumeEnv = "F10_BATCH_RESUMES"

// OldNumberFileEnv and OldResumeEnv are the two names above as the last
// release spelt them. A daemon sets both spellings and its child reads
// either, because across an upgrade the two can be one release apart.
const (
	OldNumberFileEnv = "F10_DRIVE_FILE"
	OldResumeEnv     = "F10_DRIVE_RESUMES"
)

// EnvOf reads one of the two variables above under its new name, else its
// old one.
func EnvOf(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}

	switch name {
	case NumberFileEnv:
		return os.Getenv(OldNumberFileEnv)
	case ResumeEnv:
		return os.Getenv(OldResumeEnv)
	}

	return ""
}

// A DriveSpec is one drive to run: its arguments, where it was typed, its
// project's storage root, its environment, and the record a resume names.
type DriveSpec struct {
	Argv   []string
	Dir    string
	Root   string
	Env    []string
	Resume int
}

// ErrStopping is a drive refused because a stop drains the daemon.
var ErrStopping = errors.New("the daemon is stopping: run the command again")

// ErrNotRunning is a socket nobody listens on.
var ErrNotRunning = errors.New("no f10 daemon is running")

// callTimeout bounds one call.
const callTimeout = 10 * time.Second

// Newer reports a strictly newer than b, both release versions
// (`0.46.0`, `v0.46.0`); a development build is newer than nothing, and
// nothing is newer than it, so it is replaced only by `f10 daemon stop`.
func Newer(a, b string) bool {
	x, okA := release(a)
	y, okB := release(b)

	if !okA || !okB {
		return false
	}

	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}

	return false
}

func release(v string) ([3]int, bool) {
	var out [3]int

	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}

	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}

		out[i] = n
	}

	return out, true
}
