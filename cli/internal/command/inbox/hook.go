package inbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
)

// The hidden verbs stay in process: the hook runs on every prompt within a
// budget a daemon start would break, and resume is what an answer runs.

// liveDeliver moves an answer resume hands on.
func liveDeliver() Deliverer {
	return Deliverer{
		Prompt: func(ctx context.Context, w io.Writer, a asks.Ask) error {
			note, err := launch.PromptChecked(ctx, a.Worktree, a.Task, a.Suffix, a.Answer)
			if note != "" {
				fmt.Fprintln(w, note)
			}

			return err
		},
		Resume: resumeAskDrive,
		Live: func(a asks.Ask) bool {
			_, held := drivestore.Probe(a.Root, drivestore.DriveKey(a.Drive))

			return held
		},
	}
}

// runAsksResume resumes the drive an answered ask names.
func runAsksResume(ctx context.Context, cmd *cli.Command) error {
	dir := asks.Dir()

	a, isOpen, err := asks.Get(dir, cmd.Args().First())
	if err != nil {
		return err
	}

	if isOpen || a.Drive == 0 {
		return nil
	}

	_, err = liveDeliver().Deliver(ctx, cmd.Writer, dir, *a)

	return err
}

func resumeAskDrive(ctx context.Context, w io.Writer, a asks.Ask) error {
	n := strconv.Itoa(a.Drive)

	if !daemon.Supported {
		fmt.Fprintf(w, "drive %d is stopped: f10 start --resume %s in %s takes the answer\n", a.Drive, n, a.Main)

		return nil
	}

	var extra []string
	if t, err := target.At(ctx, a.Main, false, "inbox"); err == nil {
		extra = t.Effective.DriveEnv
	}

	dir := daemon.RunDir()

	e := &daemon.Ensurer{Dir: dir, Version: buildinfo.Version()}
	if _, err := e.Ensure(ctx); err != nil {
		return fmt.Errorf("resuming drive %d: %w", a.Drive, err)
	}

	call := daemon.ResumeCall(a.Main, daemon.DriveEnv(os.Environ(), extra), a.Drive)

	_, err := daemon.RunDrive(ctx, dir, call)
	if err != nil {
		return fmt.Errorf("resuming drive %d: %w", a.Drive, err)
	}

	fmt.Fprintf(w, "drive %d resumed: f10 daemon logs %d -f follows it\n", a.Drive, a.Drive)

	return nil
}

// runAsksHook prints, as context, the open asks this session has not been
// told, leaving out the asking agent's own session.
func runAsksHook(ctx context.Context, cmd *cli.Command) error {
	// a hook never fails the prompt it runs for
	defer func() { _ = recover() }()

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil
	}

	var p struct {
		Event   string `json:"hook_event_name"`
		Session string `json:"session_id"`
		Cwd     string `json:"cwd"`
	}

	if json.Unmarshal(raw, &p) != nil || p.Session == "" {
		return nil
	}

	if p.Event == "SessionStart" {
		asks.Prune(asks.Dir(), time.Now())
	}

	// every prompt in every session runs this: an empty inbox costs no git
	if open, _ := asks.Open(asks.Dir()); len(open) == 0 {
		return nil
	}

	root := ""
	if p.Cwd != "" {
		root = layout.Locate(ctx, p.Cwd).CheckoutRoot
	}

	_, _ = cmd.Writer.Write(AsksContext(asks.Dir(), state.Dir(), p.Session, p.Event, root))

	return nil
}

// AsksContext is the hook's output: the untold open asks, outside the
// session's own checkout, as additional context; nothing when none.
func AsksContext(dir, stateDir, session, event, root string) []byte {
	open, err := asks.Open(dir)
	if err != nil || len(open) == 0 {
		return nil
	}

	markPath := state.InboxPath(stateDir, session)
	raw, _ := os.ReadFile(markPath)
	told := strings.Fields(string(raw))

	var lines, marks []string

	for _, a := range open {
		mark := a.Key() + "@" + strconv.FormatInt(a.AskedAt.Unix(), 10)
		if a.Worktree == root && root != "" {
			continue
		}

		marks = append(marks, mark)
		if slices.Contains(told, mark) {
			continue
		}

		where := ""
		if a.Skill != "" {
			where = " at " + a.Skill
		}

		lines = append(lines, fmt.Sprintf("- %s asks%s: %s", agents.AskName(a), where, a.Text))
	}

	if len(lines) == 0 {
		return nil
	}

	_ = os.MkdirAll(stateDir, 0o750)
	_ = os.WriteFile(markPath, []byte(strings.Join(marks, "\n")+"\n"), 0o600)

	text := "f10: driven agents are waiting on these questions. Tell the user about them once; " +
		"/f10:inbox answers them here, or `f10 inbox answer <task> N=VALUE` in a terminal.\n" + strings.Join(lines, "\n")

	out, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     event,
			"additionalContext": text,
		},
	})
	if err != nil {
		return nil
	}

	return append(out, '\n')
}
