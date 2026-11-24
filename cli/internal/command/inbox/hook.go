package inbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/urfave/cli/v3"

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

// runInboxResume resumes the drive an answered ask names.
func runInboxResume(ctx context.Context, cmd *cli.Command) error {
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

// runInboxHook keeps the inbox's per-session bookkeeping at session start:
// it prunes old asks and records the session's project, which the status
// line counts asks by. It adds nothing to any agent's context.
func runInboxHook(ctx context.Context, cmd *cli.Command) error {
	// a hook never fails the prompt it runs for
	defer func() { _ = recover() }()

	in := io.Reader(os.Stdin)
	if r := cmd.Root().Reader; r != nil {
		in = r
	}

	raw, err := io.ReadAll(in)
	if err != nil {
		return nil
	}

	var p struct {
		Event   string `json:"hook_event_name"`
		Session string `json:"session_id"`
		Cwd     string `json:"cwd"`
	}

	// an older plugin cache may still run it on every prompt
	if json.Unmarshal(raw, &p) != nil || p.Session == "" || p.Event != "SessionStart" {
		return nil
	}

	asks.Prune(asks.Dir(), time.Now())

	root := ""
	if p.Cwd != "" {
		root = ProjectOf(layout.Locate(ctx, p.Cwd))
	}

	WriteProject(state.Dir(), p.Session, root)

	return nil
}

// ProjectOf is the storage root a layout's asks name as their project.
// Outside git it is "", because no project's asks get mixed there.
func ProjectOf(l *layout.Layout) string {
	if l.MainRoot == "" {
		return ""
	}

	return l.StorageRoot.Path()
}

// WriteProject records the hash of the session's project for the status
// line, or removes it outside any project, where every project's asks count.
func WriteProject(stateDir, session, root string) {
	path := state.ProjectPath(stateDir, session)
	if root == "" {
		_ = os.Remove(path)

		return
	}

	_ = os.MkdirAll(stateDir, 0o750)
	_ = os.WriteFile(path, []byte(asks.ProjectHash(root)+"\n"), 0o600)
}
