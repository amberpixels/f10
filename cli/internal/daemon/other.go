//go:build !unix

package daemon

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
)

// Supported reports whether this platform runs a daemon; elsewhere drive
// runs in the foreground.
const Supported = false

// ExitDrained is a drive's exit when it stopped for an upgrade.
const ExitDrained = 6

var errUnsupported = errors.New("the f10 daemon needs a unix system")

// ErrRunning is a second daemon finding the first one running.
var ErrRunning = errors.New("an f10 daemon is already running")

// A Server is unsupported here.
type Server struct {
	Dir, Version          string
	Child                 func(argv []string) (string, []string)
	Idle, DrainWait, Tick time.Duration
	Log                   *log.Logger
	Handler               func(s *Server) http.Handler
	Key                   func(argv []string) string
	Remote                RemoteListener
	Service               bool
}

// Serve refuses: there is no daemon on this platform.
func (*Server) Serve(context.Context) error { return errUnsupported }

// An Ensurer is unsupported here.
type Ensurer struct {
	Dir, Version         string
	Spawn                func(context.Context) error
	Follow               bool
	Draining, Kept       func(running string)
	StartWait, DrainWait time.Duration
}

// Ensure refuses: drive runs in the foreground on this platform.
func (*Ensurer) Ensure(context.Context) (Info, error) { return Info{}, errUnsupported }

// FindLog finds nothing: no daemon wrote one.
func FindLog(string, string, int) string { return "" }

// Status, WatchProject, RunDrive, ResumeDrive and SessionEnv have nothing
// to report or run on this platform.
func (*Server) Status() Status { return Status{} }

func (*Server) WatchProject(string, string, []string) (string, error) { return "", errUnsupported }

func (*Server) RunDrive(DriveSpec) (Started, error) { return Started{}, errUnsupported }

func (*Server) ResumeDrive(string, string, []string, int) (Started, error) {
	return Started{}, errUnsupported
}

func (*Server) SessionEnv() []string { return nil }
