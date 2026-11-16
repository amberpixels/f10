package herdr

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// Backend is Herdr as an agentctl.Backend: a workspace's agent slot is its
// pane, and every call runs the herdr CLI from Dir.
type Backend struct {
	Dir string
	// Env is the environment the herdr CLI runs under, nil for this
	// process's own: the daemon's handlers pass a registered session's.
	Env []string
}

var _ agentctl.Backend = Backend{}

// New is the Herdr backend answering from dir.
func New(dir string) Backend { return Backend{Dir: dir} }

func (Backend) Name() string { return "Herdr" }

func (b Backend) Available() error {
	if b.Env == nil {
		return Available()
	}

	if !slices.Contains(b.Env, "HERDR_ENV=1") || !shell.Has("herdr") {
		return ErrNotInside
	}

	return nil
}

var _ agentctl.Lister = Backend{}

func (b Backend) Agents(ctx context.Context) ([]agentctl.AgentInfo, error) {
	ctx = b.scope(ctx)

	running, err := Agents(ctx, b.Dir)
	if err != nil {
		return nil, err
	}

	out := make([]agentctl.AgentInfo, 0, len(running))

	for _, r := range running {
		out = append(out, agentctl.AgentInfo{
			Name:      cmp.Or(r.Name, r.Pane),
			Workspace: r.Workspace,
			Slot:      r.Pane,
			Kind:      r.Kind,
			Status:    agentctl.Status(r.Status),
		})
	}

	return out, nil
}

// Open adopts a workspace Herdr already shows on the checkout (one with an
// agent in a pane, else the first), since `worktree open` can answer with a
// second one; others are noted, not closed: their panes may be the user's.
func (b Backend) Open(ctx context.Context, path, label string) (agentctl.Opened, []string, error) {
	ctx = b.scope(ctx)

	listed, err := Workspaces(ctx, b.Dir)
	if err != nil {
		return agentctl.Opened{}, nil, err
	}

	var shown []Listed

	for _, l := range listed {
		if l.Path != "" && gitx.SamePath(l.Path, path) {
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
	ctx = b.scope(ctx)

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
	ctx = b.scope(ctx)

	var report func(time.Duration)
	if waiting != nil {
		report = func(waited time.Duration) { waiting(waited, StartDeadline) }
	}

	return StartAgent(ctx, b.Dir, name, kind, ws.Slot, args, report)
}

func (b Backend) Prompt(ctx context.Context, name, text string) error {
	ctx = b.scope(ctx)

	return Prompt(ctx, b.Dir, name, text)
}

func (b Backend) Screen(ctx context.Context, name string) (string, error) {
	ctx = b.scope(ctx)

	return Screen(ctx, b.Dir, name)
}

func (b Backend) SendEnter(ctx context.Context, name string) error {
	ctx = b.scope(ctx)

	return SendKeys(ctx, b.Dir, name, "Enter")
}

func (b Backend) Focus(ctx context.Context, id string) error {
	ctx = b.scope(ctx)

	return FocusWorkspace(ctx, b.Dir, id)
}

func (b Backend) Close(ctx context.Context, id string) error {
	ctx = b.scope(ctx)

	return CloseWorkspace(ctx, b.Dir, id)
}

func (b Backend) Workspaces(ctx context.Context) ([]agentctl.Workspace, error) {
	ctx = b.scope(ctx)

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

// scope runs the calls under the session WithSession put on ctx, else b's
// environment, when it has one: a call made for one client runs in that
// client's session even while another one is the daemon's newest.
func (b Backend) scope(ctx context.Context) context.Context {
	if env := sessionOf(ctx); env != nil {
		return shell.WithEnv(ctx, env)
	}

	return shell.WithEnv(ctx, b.Env)
}

// sessionKey carries the Herdr session WithSession gives a context.
type sessionKey struct{}

// WithSession runs the Herdr calls made under ctx in env's session, and no
// other command: git and the host CLIs keep the process's environment.
func WithSession(ctx context.Context, env []string) context.Context {
	if len(env) == 0 {
		return ctx
	}

	return context.WithValue(ctx, sessionKey{}, env)
}

func sessionOf(ctx context.Context) []string {
	env, _ := ctx.Value(sessionKey{}).([]string)

	return env
}
