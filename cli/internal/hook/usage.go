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
// end when the run is seeded. A run still counting keeps its position, so a
// second skill in it loses no turn.
func startUsage(dir, sid, path string) {
	if path == "" {
		return
	}

	if u, ok := state.LoadUsage(dir, sid); ok && !u.Ended {
		return
	}

	if pos, err := transcript.Start(path); err == nil {
		_ = state.SaveUsage(dir, sid, state.Usage{Position: pos})
	}
}

// turnUsage is what the transcript gained since the last turn, saved as
// the new position; nil when the run has no position yet (one is started
// instead), when its run ended at an earlier turn, or when the transcript
// cannot be read. ends marks this turn as the run's last counted one. The
// caller holds the session's lock, so two Stops never count one turn twice.
func turnUsage(dir, sid, path string, ends bool) *transcript.Usage {
	if path == "" {
		return nil
	}

	from, ok := state.LoadUsage(dir, sid)
	if !ok {
		startUsage(dir, sid, path)

		return nil
	}

	if from.Ended {
		return nil
	}

	u, next, err := transcript.Read(path, from.Position)
	if err != nil {
		return nil
	}

	_ = state.SaveUsage(dir, sid, state.Usage{Position: next, Ended: ends})

	return &u
}
