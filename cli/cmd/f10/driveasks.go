package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
)

// raise puts a task's ask in the inbox, naming this drive, so an answer
// from any way in finds its way back here.
func (in *driveInput) raise(ctx context.Context, r *driveRun, skill, text string) {
	if in.asksDir == "" {
		return
	}

	raiseAsk(ctx, in.out, in.asksDir, asks.Ask{
		Task:     r.task.ref.ID,
		Root:     in.root,
		Main:     in.main,
		Project:  filepath.Base(in.main),
		Worktree: r.o.path,
		Suffix:   r.task.suffix,
		Drive:    in.number(),
		Skill:    skill,
		Text:     text,
	}, in.now(), in.notify)
}

// takeInbox: an answer the agent already heard makes the next send follow
// that turn; any other joins the answers as an --answer would. The inbox's
// answer came first, so an --answer for the same task goes unused.
func (in *driveInput) takeInbox(tasks []driveTask) {
	in.answeredIn = map[string]time.Time{}

	if in.asksDir == "" || in.root == "" {
		return
	}

	untaken, err := asks.Untaken(in.asksDir, in.root)
	if err != nil {
		fmt.Fprintf(in.out, "  reading the asks inbox: %v\n", err)
	}

	for _, a := range untaken {
		i := slices.IndexFunc(tasks, func(t driveTask) bool { return strings.EqualFold(t.ref.ID, a.Task) })
		if i < 0 {
			continue
		}

		id := tasks[i].ref.ID

		if given, ok := in.answers[id]; ok && given != a.Answer {
			in.inboxNotes = append(in.inboxNotes, fmt.Sprintf("--answer for %s went unused: %s answered it %s at %s",
				id, id, asks.Way(a.By), a.AnsweredAt.Local().Format("15:04")))
		}

		if a.DeliveredAt.IsZero() || a.Skill == "finish" {
			in.answers[id] = a.Answer
		} else {
			delete(in.answers, id)
			in.answeredIn[id] = a.AnsweredAt
		}

		in.taken(a.Key())
	}
}

func (in *driveInput) taken(key string) {
	if err := asks.Update(in.asksDir, key, func(a *asks.Ask) { a.TakenAt = in.now() }); err != nil {
		fmt.Fprintf(in.out, "  marking %s's answer taken: %v\n", key, err)
	}
}

// claimSent closes the task's open ask with the answer this drive sends, so
// every other way in shows it answered.
func (in *driveInput) claimSent(r *driveRun, answer string) {
	if in.asksDir == "" {
		return
	}

	key := asks.Key(in.root, r.task.ref.ID)
	if _, err := asks.Claim(in.asksDir, key, asks.ByDrive, answer, true, in.now()); err == nil {
		in.taken(key)
	}
}

// resumeAnswered starts this drive again for an ask answered after it last
// looked: the answer's claimer saw the drive's lease held and left it here.
func (in *driveInput) resumeAnswered(ctx context.Context, tasks []driveTask) {
	if in.rec == nil || in.asksDir == "" || in.resume == nil || in.draining.Load() || in.rec.Cancelled != nil {
		return
	}

	untaken, _ := asks.Untaken(in.asksDir, in.root)

	for _, a := range untaken {
		if !slices.ContainsFunc(tasks, func(t driveTask) bool { return strings.EqualFold(t.ref.ID, a.Task) }) {
			continue
		}

		fmt.Fprintf(in.out, "%s was answered %s as this drive stopped: drive %d resumes for it\n",
			a.Task, asks.Way(a.By), in.rec.Number)

		if err := in.resume(ctx, in.rec.Number); err != nil {
			fmt.Fprintf(in.out, "  resuming drive %d: %v; f10 drive --resume %d takes it\n",
				in.rec.Number, err, in.rec.Number)
		}

		return
	}
}

// resumeSelf has the daemon start drive n once this process is gone, since
// the daemon attaches a resume to a drive still running.
func (in *driveInput) resumeSelf(_ context.Context, n int) error {
	if os.Getenv(daemon.NumberFileEnv) == "" {
		fmt.Fprintf(in.out, "  f10 drive --resume %d takes the answer\n", n)

		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	return startDetached(exe, "asks", "resume", "--after-pid", strconv.Itoa(os.Getpid()),
		"--drive", strconv.Itoa(n), "--root", in.root, "--main", in.main)
}

// closeDriveAsks closes drive n's open asks: a cancelled drive leaves
// nothing waiting in the inbox.
func closeDriveAsks(dir, root string, n int, now time.Time) {
	open, _ := asks.Open(dir)

	for _, a := range open {
		if a.Root != root || a.Drive != n {
			continue
		}

		if _, err := asks.Claim(dir, a.Key(), asks.ByCancelled, "", false, now); err == nil {
			_ = asks.Update(dir, a.Key(), func(x *asks.Ask) { x.TakenAt = now })
		}
	}
}
