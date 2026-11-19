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

// runInboxHook prints, as context, the open asks of the session's project
// it has not been told, leaving out the asking agent's own checkout and
// every agent a drive runs.
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

	if json.Unmarshal(raw, &p) != nil || p.Session == "" {
		return nil
	}

	lay := &layout.Layout{}
	located := false

	// at most once, and only where needed: it runs git
	locate := func() *layout.Layout {
		if !located && p.Cwd != "" {
			lay, located = layout.Locate(ctx, p.Cwd), true
		}

		return lay
	}

	// before the driven and empty-inbox returns: every session's status line,
	// a drive's included, counts by it
	if p.Event == "SessionStart" {
		asks.Prune(asks.Dir(), time.Now())
		WriteProject(state.Dir(), p.Session, ProjectOf(locate()))
	}

	// a driven agent's prompt runs no git
	if os.Getenv(launch.DrivenEnv) == "1" {
		return nil
	}

	// every prompt in every session runs this: an empty inbox costs no git
	if open, _ := asks.Open(asks.Dir()); len(open) == 0 {
		return nil
	}

	l := locate()
	_, _ = cmd.Writer.Write(InboxContext(asks.Dir(), state.Dir(), p.Session, p.Event, l.CheckoutRoot, ProjectOf(l)))

	return nil
}

// ProjectOf is the project a layout sits in, as its asks name it: the
// storage root, or "" outside git, where no project's asks get mixed.
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

// InboxContext is the hook's output: the untold open asks of the project
// at project, every project's when it is empty, outside the session's own
// checkout, as additional context; nothing when none.
func InboxContext(dir, stateDir, session, event, checkout, project string) []byte {
	open, err := asks.OpenIn(dir, project)
	if err != nil || len(open) == 0 {
		return nil
	}

	markPath := state.InboxPath(stateDir, session)
	raw, _ := os.ReadFile(markPath)
	told := strings.Fields(string(raw))

	var banners, marks []string

	for _, a := range open {
		mark := a.Key() + "@" + strconv.FormatInt(a.AskedAt.Unix(), 10)
		if a.Worktree == checkout && checkout != "" {
			continue
		}

		marks = append(marks, mark)
		if slices.Contains(told, mark) {
			continue
		}

		name := ""
		if project == "" && a.Project != "" {
			name = a.Project + ":" + a.Task
		}

		banners = append(banners, "```\n"+asks.Banner(a, name)+"```")
	}

	if len(banners) == 0 {
		return nil
	}

	_ = os.MkdirAll(stateDir, 0o750)
	_ = os.WriteFile(markPath, []byte(strings.Join(marks, "\n")+"\n"), 0o600)

	text := "f10: driven agents are waiting on these questions. Tell the user about them once, printing each " +
		"banner below verbatim inside a code fence; /f10:inbox answers them here, or " +
		"`f10 inbox answer <task> N=VALUE` in a terminal.\n\n" + strings.Join(banners, "\n\n")

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
