package herdr

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

// Backend is Herdr as an agentctl.Backend: a workspace's agent slot is its
// pane, and every call runs the herdr CLI from Dir.
type Backend struct {
	Dir string
}

var _ agentctl.Backend = Backend{}

// New is the Herdr backend answering from dir.
func New(dir string) Backend { return Backend{Dir: dir} }

func (Backend) Name() string { return "Herdr" }

func (Backend) Available() error { return Available() }

func (b Backend) At(dir string) agentctl.Backend { return New(dir) }

func (b Backend) Workspaces(ctx context.Context) ([]agentctl.Workspace, error) {
	listed, err := Workspaces(ctx, b.Dir)
	if err != nil {
		return nil, err
	}

	wss := make([]agentctl.Workspace, 0, len(listed))

	for _, l := range listed {
		wss = append(wss, agentctl.Workspace{
			ID:      l.ID,
			Label:   l.Label,
			Path:    l.Path,
			Focused: l.Focused,
			Status:  agentctl.Status(l.Agent),
		})
	}

	return wss, nil
}

// Open adopts a workspace Herdr already shows on the checkout rather than
// opening another, since `worktree open` can answer with a second one
// beside it: the first with an agent in a pane wins, else the first. Any
// other is named in a note, never closed, since its panes may hold the
// user's own work.
func (b Backend) Open(ctx context.Context, path, label string) (agentctl.Opened, []string, error) {
	listed, err := Workspaces(ctx, b.Dir)
	if err != nil {
		return agentctl.Opened{}, nil, err
	}

	var shown []Listed

	for _, l := range listed {
		if l.Path != "" && samePath(l.Path, path) {
			shown = append(shown, l)
		}
	}

	if len(shown) == 0 {
		ws, err := OpenWorktree(ctx, b.Dir, path, label)

		return agentctl.Opened{ID: ws.ID, Slot: ws.RootPane, AlreadyOpen: ws.AlreadyOpen}, nil, err
	}

	var adopted agentctl.Opened

	for _, l := range shown {
		panes, err := Panes(ctx, b.Dir, l.ID)
		if err != nil {
			return agentctl.Opened{}, nil, err
		}

		if i := slices.IndexFunc(panes, func(p Pane) bool { return p.Agent != "" }); i >= 0 {
			adopted = agentctl.Opened{ID: l.ID, Slot: panes[i].ID, AlreadyOpen: true}

			break
		}

		if adopted.ID == "" && len(panes) > 0 {
			adopted = agentctl.Opened{ID: l.ID, Slot: panes[0].ID, AlreadyOpen: true}
		}
	}

	if adopted.ID == "" {
		return agentctl.Opened{}, nil, fmt.Errorf("herdr shows %s in workspace %s, which has no pane",
			path, shown[0].ID)
	}

	var notes []string

	for _, l := range shown {
		if l.ID != adopted.ID {
			notes = append(notes, fmt.Sprintf("workspace %s also shows this checkout and was left open: "+
				"close it with herdr workspace close %s", cmp.Or(l.Label, l.ID), l.ID))
		}
	}

	return adopted, notes, nil
}

// Occupancy asks Herdr for an agent in the pane, by the pane's id since
// its name is unknown here, then whether the pane sits at an idle shell.
func (b Backend) Occupancy(ctx context.Context, ws agentctl.Opened) (agentctl.Occupancy, error) {
	exists, err := AgentExists(ctx, b.Dir, ws.Slot)
	if err != nil {
		return 0, err
	}

	if exists {
		return agentctl.AgentPresent, nil
	}

	idle, err := PaneIdleShell(ctx, b.Dir, ws.Slot)
	if err != nil {
		return 0, err
	}

	if !idle {
		return agentctl.Busy, nil
	}

	return agentctl.Free, nil
}

func (b Backend) StartAgent(
	ctx context.Context,
	ws agentctl.Opened,
	name, kind string,
	args []string,
	waiting func(waited, limit time.Duration),
) error {
	var report func(time.Duration)
	if waiting != nil {
		report = func(waited time.Duration) { waiting(waited, StartDeadline) }
	}

	return StartAgent(ctx, b.Dir, name, kind, ws.Slot, args, report)
}

func (b Backend) Prompt(ctx context.Context, name, text string) error {
	return Prompt(ctx, b.Dir, name, text)
}

func (b Backend) Focus(ctx context.Context, id string) error {
	return FocusWorkspace(ctx, b.Dir, id)
}

func (b Backend) Close(ctx context.Context, id string) error {
	return CloseWorkspace(ctx, b.Dir, id)
}

// samePath compares two paths with symlinks resolved where they exist:
// Herdr may report a checkout through a symlink.
func samePath(a, b string) bool {
	return resolved(a) == resolved(b)
}

// resolved is p with symlinks resolved, up to the deepest part that exists.
func resolved(p string) string {
	p = filepath.Clean(p)

	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}

	parent := filepath.Dir(p)
	if parent == p {
		return p
	}

	return filepath.Join(resolved(parent), filepath.Base(p))
}
