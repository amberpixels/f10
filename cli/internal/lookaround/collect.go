package lookaround

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
)

// Inputs binds the collectors to one project. A nil Host, Tasks or Local
// is that source absent; Remote skips the local collector on purpose.
type Inputs struct {
	Project Project
	Prefix  string
	// Since is where reading starts: the state view's done window, or the
	// earliest span the feed may show.
	Since time.Time

	// Feed reads what changed since Since instead of where every task
	// stands: the host's activity, the tracker's list, and nothing local
	// unless the checkout is local only.
	Feed bool

	// LocalOnly is a checkout with no origin remote: the host is skipped,
	// and the tracker too unless a driver answers it (Tasks set).
	LocalOnly bool

	Host      *forge.Host
	HostError string // why there is no host, for the source's error

	Tasks func(ctx context.Context) ([]Task, error)

	// RemoteOpen reports whether the project's declared remote reviewer
	// left a review on the request nobody handled; nil declares none. An
	// error is a note on the host source, the request kept without it.
	RemoteOpen func(ctx context.Context, number int) (bool, error)

	Remote bool
	Local  *LocalInputs

	// People is the user's map of one person's identities across the
	// sources; nil groups rows by whatever identity each source gives.
	People []Person

	// Now is the clock; nil is time.Now. A test fixes it so timings are
	// stable.
	Now func() time.Time
}

// Collect runs every collector and returns what each found, guarded: a
// collector that errors marks its source failed and contributes nothing,
// and every other one still runs.
func Collect(ctx context.Context, in Inputs) Facts {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}

	f := Facts{Project: in.Project, Prefix: in.Prefix, Tasks: []Task{}, PRs: []PR{}, People: in.People}

	timed := func(name string, run func() ([]string, error)) {
		start := now()
		notes, err := run()
		s := Source{Name: name, OK: err == nil, Notes: notes, TookMS: now().Sub(start).Milliseconds()}

		if err != nil {
			s.Error = err.Error()
		}

		f.Sources = append(f.Sources, s)
	}

	skip := func(name string) { f.Sources = append(f.Sources, Source{Name: name, OK: true, Skipped: true}) }

	switch {
	case in.LocalOnly:
		f.Sources = append(f.Sources, Source{Name: SourceHost, OK: true, Skipped: true, Notes: []string{"local only"}})
	case in.Feed:
		timed(SourceHost, func() ([]string, error) {
			if in.Host == nil {
				return nil, errors.New(cmp.Or(in.HostError, "no host CLI"))
			}

			a, earlier, notes, err := collectActivity(ctx, in)
			if err == nil {
				f.Activity, f.Earlier = a, earlier
			}

			return notes, err
		})
	default:
		timed(SourceHost, func() ([]string, error) {
			if in.Host == nil {
				return nil, errors.New(cmp.Or(in.HostError, "no host CLI"))
			}

			prs, notes, err := collectHost(ctx, in)
			if err == nil {
				f.PRs = prs
			}

			return notes, err
		})
	}

	if in.LocalOnly && in.Tasks == nil {
		skip(SourceTracker)
	} else {
		timed(SourceTracker, func() ([]string, error) {
			if in.Tasks == nil {
				return nil, errors.New("no tracker")
			}

			tasks, err := in.Tasks(ctx)
			if err == nil {
				f.Tasks = tasks
			}

			return nil, err
		})
	}

	switch {
	case in.Remote || in.Local == nil:
		skip(SourceLocal)
	case in.Feed && in.LocalOnly:
		timed(SourceLocal, func() ([]string, error) {
			tips, err := collectTips(ctx, in.Local.Main)
			if err == nil {
				f.Tips = tips
			}

			return nil, err
		})
	case in.Feed:
		skip(SourceLocal)
	default:
		timed(SourceLocal, func() ([]string, error) {
			l, notes, err := collectLocal(ctx, *in.Local, in.Prefix, now())
			if err == nil {
				f.Local = l
			}

			return notes, err
		})
	}

	return f
}

// Unreachable is the board of a project no collector could run for. Every
// source carries the error, so a reader of several boards sees the project
// failed rather than empty; a project with no checkout keeps local skipped.
func Unreachable(p Project, err error, now time.Time, w Window, view string) Board {
	b := Board{
		Version: Version, GeneratedAt: now, View: view, Project: p, Window: w, Rows: []Row{},
	}

	if view == ViewFeed {
		b.Feed = &Feed{Rows: []FeedRow{}}
	}

	for _, name := range []string{SourceHost, SourceTracker, SourceLocal} {
		s := Source{Name: name, Error: err.Error()}
		if name == SourceLocal && p.Remote {
			s = Source{Name: name, OK: true, Skipped: true}
		}

		b.Sources = append(b.Sources, s)
	}

	return b
}
