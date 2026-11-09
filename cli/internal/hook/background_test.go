package hook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/amberpixels/f10/cli/internal/state"
)

func TestBackgroundMarkFollowsTheTurns(t *testing.T) {
	bash := func(bg bool) []byte {
		raw, err := json.Marshal(map[string]any{
			"session_id": "s1", "cwd": "/work/acme", "hook_event_name": "PreToolUse", "tool_name": "Bash",
			"tool_input": map[string]any{"command": "go test ./...", "run_in_background": bg},
		})
		if err != nil {
			t.Fatal(err)
		}

		return raw
	}
	launch, plain := bash(true), bash(false)
	monitor := event(t, "s1", "PreToolUse", "Monitor")
	stop := event(t, "s1", "Stop", "")
	read := event(t, "s1", "PreToolUse", "Bash")
	prompt := event(t, "s1", "UserPromptSubmit", "")

	for _, tc := range []struct {
		name   string
		run    bool
		events [][]byte
		want   bool // a mark is left
	}{
		{name: "a turn parked on a background command", run: true, events: [][]byte{launch, stop}, want: true},
		{name: "a parked turn the agent wakes from", run: true, events: [][]byte{launch, stop, read}},
		{name: "a woken turn that ends with no tool call", run: true, events: [][]byte{launch, stop, stop}},
		{name: "tools in the launching turn keep it", run: true, events: [][]byte{launch, plain, stop}, want: true},
		{name: "a Monitor launches too", run: true, events: [][]byte{monitor, stop}, want: true},
		{name: "a new prompt clears it", run: true, events: [][]byte{launch, stop, prompt}},
		{name: "a foreground command marks nothing", run: true, events: [][]byte{plain, stop}},
		{name: "a session with no run marks nothing", events: [][]byte{launch, stop}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			if tc.run {
				withRun(t, env, "s1")
			}

			for _, raw := range tc.events {
				Dispatch(context.Background(), raw, env)
			}

			b, ok := state.PendingBackground(env.StateDir, "s1")
			if ok != tc.want {
				t.Fatalf("mark left = %v (%+v), want %v", ok, b, tc.want)
			}

			if ok && !b.At.Equal(fixedNow) {
				t.Errorf("mark at %v, want %v", b.At, fixedNow)
			}
		})
	}
}
