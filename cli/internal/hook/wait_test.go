package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// event is a hook payload; a tool given as `Bash#7` carries tool_use_id 7.
func event(t *testing.T, session, name, tool string) []byte {
	t.Helper()

	tool, id, _ := strings.Cut(tool, "#")

	raw, err := json.Marshal(map[string]any{
		"session_id": session, "cwd": "/work/acme", "hook_event_name": name, "tool_name": tool, "tool_use_id": id,
	})
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

// waitRows is each wait row as kind, reason and the event that ended it.
func waitRows(t *testing.T, db string) []string {
	t.Helper()

	var got []string

	if _, err := os.Stat(db); err != nil {
		return nil // nothing recorded opened no database
	}

	for _, e := range recorded(t, db, ledger.Filter{}) {
		if e.Kind != ledger.WaitStarted && e.Kind != ledger.WaitEnded {
			continue
		}

		row := e.Kind + " " + str(e.Payload.Val["reason"])
		if by := str(e.Payload.Val["by"]); by != "" {
			row += " by " + by
		}

		if e.Task != "FT-12" {
			t.Errorf("%s recorded for task %q", row, e.Task)
		}

		got = append(got, row)
	}

	return got
}

func withRun(t *testing.T, env *Env, session string) {
	t.Helper()

	run := "v 1\ntask FT-12\nroot /work/acme\nskill ship\nship running\nleaf implement\nupdated 1\n"
	if err := os.WriteFile(filepath.Join(env.StateDir, session+".state"), []byte(run), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWaitsOpenAndEnd(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events [][3]string // session, event, tool
		want   []string
	}{
		{
			name:   "a turn ends at the next prompt",
			events: [][3]string{{"s1", "Stop"}, {"s1", "PostToolUse", "Bash"}, {"s1", "UserPromptSubmit"}},
			want:   []string{"wait_started turn", "wait_ended turn by UserPromptSubmit"},
		},
		{
			name: "a question ends when its tool returns",
			events: [][3]string{
				{"s1", "PreToolUse", "AskUserQuestion"},
				{"s1", "PostToolUse", "Bash"},
				{"s1", "PostToolUse", "AskUserQuestion"},
			},
			want: []string{"wait_started question", "wait_ended question by PostToolUse"},
		},
		{
			name: "a permission dialog ends when its tool returns",
			events: [][3]string{
				{"s1", "PermissionRequest", "WebFetch"},
				{"s1", "PostToolUse", "Bash"},
				{"s1", "PostToolUse", "WebFetch"},
			},
			want: []string{"wait_started permission", "wait_ended permission by PostToolUse"},
		},
		{
			name:   "a denied permission ends at the turn's end",
			events: [][3]string{{"s1", "PermissionRequest", "Bash"}, {"s1", "Stop"}},
			want:   []string{"wait_started permission", "wait_ended permission by Stop", "wait_started turn"},
		},
		{
			name: "a tool that fails after its dialog ends it",
			events: [][3]string{
				{"s1", "PermissionRequest", "Bash"}, {"s1", "PostToolUseFailure", "Bash"},
			},
			want: []string{"wait_started permission", "wait_ended permission by PostToolUseFailure"},
		},
		{
			name: "another call of the same tool leaves the dialog open",
			events: [][3]string{
				{"s1", "PermissionRequest", "Bash#2"},
				{"s1", "PostToolUse", "Bash#1"},
				{"s1", "PostToolUse", "Bash#2"},
			},
			want: []string{"wait_started permission", "wait_ended permission by PostToolUse"},
		},
		{
			name:   "a session with no run waits on nothing",
			events: [][3]string{{"s2", "Stop"}, {"s2", "UserPromptSubmit"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "f10.db")
			t.Setenv(ledger.PathEnv, db)

			env := testEnv(t)
			withRun(t, env, "s1")

			for _, ev := range tc.events {
				dispatchRecorded(t, env, event(t, ev[0], ev[1], ev[2]))
			}

			if got := waitRows(t, db); !slices.Equal(got, tc.want) {
				t.Errorf("waits\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
