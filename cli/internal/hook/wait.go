package hook

import (
	"encoding/json"
	"os"

	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
)

// openWait is the marker a session's open wait leaves in the state
// directory, so an event that neither opens nor ends one costs a stat.
type openWait struct {
	Reason string `json:"reason"`
	Tool   string `json:"tool,omitempty"`
	UseID  string `json:"tool_use_id,omitempty"`
}

// waits ends the session's open wait when this event ends it, then opens
// the wait this event starts. Only a session with a run opens one, so an
// unrelated session never writes a wait row.
func waits(env *Env, sid string, p Payload) []observe.Observation {
	if sid == "" {
		return nil
	}

	var obs []observe.Observation

	path := state.WaitPath(env.StateDir, sid)
	reason, tool := opens(p)

	if w, ok := readWait(path); ok && (reason != "" || ends(w, p)) {
		_ = os.Remove(path)
		obs = append(obs, &observe.WaitEnded{Reason: w.Reason, Tool: w.Tool, By: p.Event})
	}

	if reason == "" {
		return obs
	}

	if _, err := os.Stat(state.Path(env.StateDir, sid)); err != nil {
		return obs
	}

	raw, err := json.Marshal(openWait{Reason: reason, Tool: tool, UseID: p.ToolUseID})
	if err != nil || os.MkdirAll(env.StateDir, 0o750) != nil || os.WriteFile(path, raw, 0o600) != nil {
		return obs
	}

	return append(obs, &observe.WaitStarted{Reason: reason, Tool: tool})
}

// opens is the wait an event starts: a turn ending, a question asked, a
// permission dialog shown.
func opens(p Payload) (string, string) {
	switch {
	case p.Event == "Stop":
		return observe.WaitTurn, ""
	case p.Event == "PreToolUse" && p.ToolName == "AskUserQuestion":
		return observe.WaitQuestion, p.ToolName
	case p.Event == "PermissionRequest":
		return observe.WaitPermission, p.ToolName
	}

	return "", ""
}

// ends reports whether an event ends an open wait: a prompt ends any; a tool
// finishing or failing ends only its own dialog (same id, else same tool),
// since the turn's last async PostToolUse can land after Stop opened the
// turn. A denied permission runs no tool, so the next prompt or Stop ends it.
func ends(w openWait, p Payload) bool {
	switch p.Event {
	case "UserPromptSubmit", "UserPromptExpansion":
		return true
	case "PostToolUse", "PostToolUseFailure":
		if w.Reason == observe.WaitTurn || w.Tool != p.ToolName {
			return false
		}

		return w.UseID == "" || p.ToolUseID == "" || w.UseID == p.ToolUseID
	}

	return false
}

func readWait(path string) (openWait, bool) {
	var w openWait

	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &w) != nil || w.Reason == "" {
		return w, false
	}

	return w, true
}
