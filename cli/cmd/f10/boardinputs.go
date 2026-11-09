package main

import (
	"cmp"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

func boardPeople(people []person) []lookaround.Person {
	out := make([]lookaround.Person, 0, len(people))
	for _, p := range people {
		out = append(out, lookaround.Person{Name: p.Name, Tracker: p.Tracker, Host: p.Host, Emails: p.Emails})
	}

	return out
}

// collectBoards derives one board per entry; one that could not be bound is
// a board whose sources all failed, so the others still render. The feed's
// span is chosen once every entry's facts are in: the day is the person's.
func collectBoards(
	ctx context.Context, entries []scopeEntry, warn io.Writer, now time.Time, span boardSpan, stateView, remote bool,
	people []lookaround.Person,
) []lookaround.Board {
	facts := make([]*lookaround.Facts, len(entries))
	sem := make(chan struct{}, scopeParallel)

	var wg sync.WaitGroup

	for i, e := range entries {
		if e.err != nil {
			continue
		}

		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			var in lookaround.Inputs
			if e.repo != nil {
				in = repoInputs(*e.repo, warn, span.from())
			} else {
				in = boardInputs(ctx, e.target, warn, span.from(), remote)
			}

			in.People, in.Feed = people, !stateView
			f := lookaround.Collect(ctx, in)
			facts[i] = &f
		})
	}

	wg.Wait()

	view := lookaround.ViewFeed
	if stateView {
		view = lookaround.ViewState
	}

	var w lookaround.Window

	if span.named != nil {
		w = *span.named
	} else {
		var all []lookaround.Facts

		for _, f := range facts {
			if f != nil {
				all = append(all, *f)
			}
		}

		w = lookaround.ChooseWindow(span.today, span.lastWorkday, all...)
	}

	boards := make([]lookaround.Board, len(entries))

	for i, e := range entries {
		switch {
		case e.err != nil:
			p := lookaround.Project{ID: e.id, Name: filepath.Base(e.main), Checkout: e.main}
			boards[i] = lookaround.Unreachable(p, e.err, now, w, view)
		case stateView:
			boards[i] = lookaround.Derive(*facts[i], now, w.Since)
		default:
			boards[i] = lookaround.DeriveFeed(*facts[i], now, w)
		}
	}

	return boards
}

// boardInputs binds the collectors to the target. A checkout with no origin
// remote is local only: no host, and no tracker but a driver.
func boardInputs(ctx context.Context, t *target, warn io.Writer, since time.Time, remote bool) lookaround.Inputs {
	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	place := ledger.Where(ctx, main)

	in := lookaround.Inputs{
		Project: lookaround.Project{ID: place.Project, Name: t.lay.Project, Remote: remote},
		Prefix:  t.eff.IDPrefix,
		Since:   since,
		Remote:  remote,
	}

	if !remote {
		in.Project.Checkout = main
	}

	in.Tasks = func(ctx context.Context) ([]lookaround.Task, error) {
		rows, err := taskRows(ctx, t, boardPage)
		if err != nil {
			return nil, err
		}

		return boardTasks(settlePriorities(rows, warn), t.eff.IDPrefix), nil
	}

	if t.pb.RemoteURL == "" {
		in.LocalOnly, in.Project.LocalOnly = true, true

		if t.driver() == nil {
			in.Tasks = nil
		}
	} else if h, err := t.host(); err != nil {
		in.HostError = err.Error()
	} else {
		in.Host = &h.Host
		in.Project.Host = h.Name

		if t.eff.Review.Remote() {
			in.RemoteOpen = func(ctx context.Context, number int) (bool, error) {
				rv, err := newReviewer(ctx, h, t.eff.Review, strconv.Itoa(number))
				if err != nil {
					return false, err
				}

				switch _, err := rv.pick(ctx); {
				case err == nil:
					return true, nil
				case errors.Is(err, errNoReview):
					return false, nil
				default:
					return false, err
				}
			}
		}
	}

	if !remote {
		in.Local = &lookaround.LocalInputs{
			Main:       main,
			StateDir:   state.Dir(),
			TTL:        state.TTL(),
			LedgerPath: ledger.Path(),
			ProjectID:  place.Project,
			Agents:     reachableAgents(main),
			SharedRoot: t.lay.StorageRoot,
			Resolve:    t.idOf(ctx),
		}
	}

	return in
}

// repoInputs binds the collectors to a repository with no checkout: its host
// alone, no driver, no priority labels, nothing local, as --remote reads.
func repoInputs(r repoRef, warn io.Writer, since time.Time) lookaround.Inputs {
	h := host{Name: r.CLI, Repo: r.repo()}

	return lookaround.Inputs{
		Project: lookaround.Project{ID: r.id(), Name: r.name(), Host: r.CLI, Remote: true},
		Prefix:  r.Prefix,
		Since:   since,
		Remote:  true,
		Host:    &h.Host,
		Tasks: func(ctx context.Context) ([]lookaround.Task, error) {
			rows, err := h.listIssues(ctx, "", r.Prefix, nil, boardPage)
			if err != nil {
				return nil, err
			}

			return boardTasks(settlePriorities(rows, warn), r.Prefix), nil
		},
	}
}

// boardTasks is tracker rows in the board's shape, the base in the project's
// id format since a driver may send the bare number.
func boardTasks(rows []row, prefix string) []lookaround.Task {
	out := make([]lookaround.Task, 0, len(rows))

	for _, r := range rows {
		after := cmp.Or(r.After, afterIn(r.Body))
		if id := ref.Match(prefix, after); id != "" {
			after = id
		} else if prefix != "" && after != "" && strings.Trim(after, "0123456789") == "" {
			after = prefix + "-" + after
		}

		out = append(out, lookaround.Task{
			ID: r.ID, Title: r.Title, URL: r.URL, Status: r.Status, Priority: r.Priority, Assignee: r.Assignee,
			After: after, Created: r.Created,
		})
	}

	return out
}
