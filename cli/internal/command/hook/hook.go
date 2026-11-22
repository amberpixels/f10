// Package hook is the f10 hook command, which handles one Claude Code hook
// event read from stdin.
package hook

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/asknotify"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hook"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the hook noun: what hooks/hooks.json runs; hidden, since nobody types it.
func Command() *cli.Command {
	return &cli.Command{
		Name:   "hook",
		Usage:  "handle one Claude Code hook event; the payload on stdin",
		Hidden: true,
		Action: runHook,
	}
}

func runHook(ctx context.Context, cmd *cli.Command) error {
	// nothing a hook does may fail a run, a bug included
	defer func() { _ = recover() }()

	raw, err := io.ReadAll(os.Stdin)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	home, _ := os.UserHomeDir()

	// a synchronous event holds the prompt while it runs: a busy ledger
	// spools quickly, and the spool's replay waits for an async writer
	var event struct {
		Name string `json:"hook_event_name"`
	}

	_ = json.Unmarshal(raw, &event)

	recorder := &ledger.Observer{StateDir: state.Dir(), Busy: ledger.BusyAsync, TaskID: hookTaskID}
	if event.Name == "UserPromptExpansion" || event.Name == "PreToolUse" || event.Name == "PermissionRequest" {
		recorder.Busy, recorder.NoReplay = ledger.BusySync, true
	}
	defer recorder.Close()

	env := &hook.Env{
		StateDir: state.Dir(),
		Home:     home,
		// the shim exports its own root; Claude Code sets the other for hooks
		PluginRoot:  cmp.Or(os.Getenv("F10_PLUGIN_ROOT"), os.Getenv("CLAUDE_PLUGIN_ROOT")),
		Debug:       os.Getenv("F10_STATE_DEBUG") != "",
		StorageRoot: func(ctx context.Context, cwd string) string { return layout.Locate(ctx, cwd).StorageRoot.Path() },
		TaskID:      hookTaskID,
		StatusText:  HookStatusText,
		PromptTyped: func(ctx context.Context, cwd, prompt string) string {
			var told []string

			for _, a := range asknotify.PaneAnswered(ctx, cwd, prompt) {
				agents.ReleasePane(ctx, io.Discard, a)

				// the user typed letters: name the options they chose
				if a.Answer != strings.TrimSpace(prompt) {
					told = append(told, "f10: this reply answers your ask: "+a.Answer)
				}
			}

			return strings.Join(told, "\n")
		},
		Profile:   hookProfile,
		Observers: []observe.Observer{recorder},
	}

	_, err = cmd.Writer.Write(hook.Dispatch(ctx, raw, env))

	// the replay points: both run async, so a backlog delays no prompt
	if event.Name == "SessionStart" || event.Name == "Stop" {
		_ = ledger.Sync(ctx)
	}

	return err
}

// hookProfile is the profile the cwd's branch records, zero when it
// records none or git cannot tell.
func hookProfile(ctx context.Context, cwd string) agentctl.Profile {
	branch := gitx.CurrentBranch(ctx, cwd)
	if branch == "" {
		return agentctl.Profile{}
	}

	p, _ := runplan.RecordedProfile(ctx, cwd, branch)

	return p
}

func hookTaskID(ctx context.Context, cwd, url string) string {
	t, err := target.At(ctx, cwd, true, "hook")
	if err != nil {
		return ""
	}

	r, err := t.Reference(ctx, url)
	if err != nil {
		return ""
	}

	return r.ID
}

// HookStatusText is what `f10 status [token]` prints for the session in the
// hook's directory, and whether the model must take the turn.
func HookStatusText(ctx context.Context, session, token string) (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return ui.Prefixed(err.Error()), false
	}

	return statusview.StatusText(ctx, dir, session, token)
}
