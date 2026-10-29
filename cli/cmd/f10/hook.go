package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/hook"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The hook noun is what hooks/hooks.json runs; hidden, since nobody types it.
func hookCommand() *cli.Command {
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
		StorageRoot: func(ctx context.Context, cwd string) string { return layout.Locate(ctx, cwd).StorageRoot },
		TaskID:      hookTaskID,
		StatusText:  hookStatusText,
		PromptTyped: paneAnswered,
		Observers:   []observe.Observer{recorder},
	}

	_, err = cmd.Writer.Write(hook.Dispatch(ctx, raw, env))

	// the replay points: both run async, so a backlog delays no prompt
	if event.Name == "SessionStart" || event.Name == "Stop" {
		_ = ledger.Sync(ctx)
	}

	return err
}

func hookTaskID(ctx context.Context, cwd, url string) string {
	t, err := targetAt(ctx, cwd, true, "hook")
	if err != nil {
		return ""
	}

	r, err := t.reference(ctx, url)
	if err != nil {
		return ""
	}

	return r.ID
}

// hookStatusText runs `f10 status` in this process and says whether it
// printed a question a driven agent waits on.
func hookStatusText(ctx context.Context, session string) (string, bool) {
	if err := os.Setenv("F10_SESSION_ID", session); err != nil {
		return "", false
	}

	var out bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &out

	asks := &statusAsks{}
	if err := app.Run(context.WithValue(ctx, statusAsksKey{}, asks), []string{"f10", "status"}); err != nil {
		fmt.Fprintln(&out, "f10:", err)
	}

	return strings.TrimSpace(out.String()), asks.printed
}
