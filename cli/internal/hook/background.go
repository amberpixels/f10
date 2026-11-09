package hook

import (
	"encoding/json"
	"os"

	"github.com/amberpixels/f10/cli/internal/state"
)

// background keeps the session's background mark, which a drive reads as the
// agent working: a launch marks it, the launching turn's Stop parks it, and a
// later turn clears it. Launches come from PreToolUse, which fires before the
// tool runs, so it lands ahead of the turn's Stop where a PostToolUse may not.
func background(env *Env, sid string, p Payload) {
	if sid == "" {
		return
	}

	if launches(p) {
		// only a session with a run marks one, as waits() does
		if _, err := os.Stat(state.Path(env.StateDir, sid)); err == nil {
			_ = state.MarkBackground(env.StateDir, sid, state.Background{At: env.now()})
		}

		return
	}

	b, ok := state.PendingBackground(env.StateDir, sid)
	if !ok {
		return
	}

	switch p.Event {
	case "Stop":
		if !b.Parked {
			b.Parked = true
			_ = state.MarkBackground(env.StateDir, sid, b)

			return
		}

		_ = os.Remove(state.BackgroundPath(env.StateDir, sid))
	case "UserPromptSubmit", "UserPromptExpansion":
		_ = os.Remove(state.BackgroundPath(env.StateDir, sid))
	case "PreToolUse":
		// a tool call in the launching turn leaves the work running
		if b.Parked {
			_ = os.Remove(state.BackgroundPath(env.StateDir, sid))
		}
	}
}

// launches reports a tool call that leaves work running past its turn. An
// Agent call counts whatever its input says, since hosts that run subagents
// in the background do so by default.
func launches(p Payload) bool {
	if p.Event != "PreToolUse" {
		return false
	}

	switch p.ToolName {
	case "Monitor", "Agent":
		return true
	case "Bash":
		var in struct {
			Background bool `json:"run_in_background"`
		}

		return json.Unmarshal(p.ToolInput, &in) == nil && in.Background
	}

	return false
}
