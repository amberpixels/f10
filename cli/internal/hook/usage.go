package hook

import (
	"cmp"
	"context"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/transcript"
)

// runContext is what the session runs on now. model is the one this turn's
// usage last counted, else the transcript's last, else SessionStart's.
func runContext(ctx context.Context, env *Env, sid, cwd, path, model string) observe.RunContext {
	if model == "" && path != "" {
		model = transcript.LastModel(path)
	}

	var p agentctl.Profile
	if env.Profile != nil && cwd != "" {
		p = env.Profile(ctx, cwd)
	}

	return observe.RunContext{
		F10Version:    buildinfo.Version(),
		Model:         cmp.Or(model, state.SessionModel(env.StateDir, sid)),
		Agent:         cmp.Or(p.Kind, agentctl.DefaultKind),
		ProfileKind:   p.Kind,
		ProfileModel:  p.Model,
		ProfileEffort: p.Effort,
	}
}

// withContext sets the run context on each skill start.
func withContext(obs []observe.Observation, c observe.RunContext) []observe.Observation {
	for _, o := range obs {
		if s, ok := o.(*observe.SkillStarted); ok {
			s.RunContext = c
		}
	}

	return obs
}

// startUsage marks where a run's usage starts counting: the transcript's
// end when the run is seeded, unless an earlier run already marked it.
func startUsage(dir, sid, path string) {
	if path == "" {
		return
	}

	if _, ok := state.LoadUsage(dir, sid); ok {
		return
	}

	if pos, err := transcript.Start(path); err == nil {
		_ = state.SaveUsage(dir, sid, pos)
	}
}

// turnUsage is what the transcript gained since the last turn, saved as
// the new position; nil when the run has no position yet (one is started
// instead) or the transcript cannot be read. The caller holds the session's
// lock, so two Stops never count one turn twice.
func turnUsage(dir, sid, path string) *transcript.Usage {
	if path == "" {
		return nil
	}

	from, ok := state.LoadUsage(dir, sid)
	if !ok {
		startUsage(dir, sid, path)

		return nil
	}

	u, next, err := transcript.Read(path, from)
	if err != nil {
		return nil
	}

	_ = state.SaveUsage(dir, sid, next)

	return &u
}
