package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/asknotify"
	"github.com/amberpixels/f10/cli/internal/asks"
)

// raise puts a task's ask in the inbox, naming this drive, so an answer
// from any way in finds its way back here.
func (in *driveInput) raise(ctx context.Context, r *driveRun, skill, text string) {
	if in.asksDir == "" {
		return
	}

	asknotify.RaiseAsk(ctx, in.out, in.asksDir, asks.Ask{
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

		if given, ok := in.answers.keyed(id); ok && given.text != a.Answer {
			in.inboxNotes = append(in.inboxNotes, fmt.Sprintf("--answer for %s went unused: %s answered it %s at %s",
				id, id, asks.Way(a.By), a.AnsweredAt.Local().Format("15:04")))
		}

		from := heardInbox
		if a.DeliveredAt.IsZero() || a.Skill == "finish" {
			from = fromInbox
		}

		in.answers.give(answer{task: id, text: a.Answer, from: from, at: a.AnsweredAt})

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

// resumeAnswered reports an ask answered after this drive last looked, for
// which the drive must run again: the answer's claimer saw the drive's lease
// held and left it here. Outside the daemon it names the command instead.
func (in *driveInput) resumeAnswered(tasks []driveTask) bool {
	if in.rec == nil || in.asksDir == "" || in.draining.Load() || in.rec.Cancelled != nil {
		return false
	}

	untaken, _ := asks.Untaken(in.asksDir, in.root)

	for _, a := range untaken {
		if !slices.ContainsFunc(tasks, func(t driveTask) bool { return strings.EqualFold(t.ref.ID, a.Task) }) {
			continue
		}

		if !in.daemonRun {
			fmt.Fprintf(in.out, "%s was answered %s as this drive stopped: f10 drive --resume %d takes the answer\n",
				a.Task, asks.Way(a.By), in.rec.Number)

			return false
		}

		fmt.Fprintf(in.out, "%s was answered %s as this drive stopped: drive %d resumes for it\n",
			a.Task, asks.Way(a.By), in.rec.Number)

		return true
	}

	return false
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

func (in *driveInput) takeAnswers(tasks []driveTask) error {
	in.answers = newAnswerBook()

	for _, a := range in.args.answers {
		if id, text, ok := keyedAnswer(a, tasks); ok {
			in.answers.give(answer{task: id, text: text, from: fromFlag})

			continue
		}

		if in.answers.bare != "" {
			return errors.New("several --answer values name no task: key each one, --answer GH-1=\"1. …\"")
		}

		in.answers.give(answer{text: a, from: fromFlag})
	}

	return nil
}

// keyedAnswer splits an --answer that names one of the drive's tasks into
// its id and text; any other answer is bare, an "=" in its text included.
func keyedAnswer(a string, tasks []driveTask) (string, string, bool) {
	key, text, ok := strings.Cut(a, "=")
	if !ok {
		return "", "", false
	}

	i := slices.IndexFunc(tasks, func(t driveTask) bool { return strings.EqualFold(t.ref.ID, strings.TrimSpace(key)) })
	if i < 0 {
		return "", "", false
	}

	return tasks[i].ref.ID, strings.TrimSpace(text), true
}

func answerFlags(asking []string) string {
	if len(asking) == 1 {
		return ` --answer "1. … 2. …"`
	}

	var b strings.Builder
	for _, id := range asking {
		b.WriteString(` --answer "` + id + `=1. … 2. …"`)
	}

	return b.String()
}

// finishAnswer takes a bare answer only when it is merge or local, so one
// meant for an agent's question stays there.
func (in *driveInput) finishAnswer(id string) (string, error) {
	if a, ok := in.answers.takeKeyed(id); ok {
		in.took(id, "finish", a)

		if c := finishChoice(a); c != "" {
			return c, nil
		}

		return "", fmt.Errorf("the answer to finish is %q: answer %s or %s", a, finishLeaveOpen, finishMerge)
	}

	if c := finishChoice(in.answers.bare); c != "" {
		in.answers.bare = ""
		in.took(id, "finish", c)

		return c, nil
	}

	return "", nil
}

func finishChoice(answer string) string {
	a := strings.ToLower(strings.TrimSpace(answer))
	a = strings.TrimSpace(strings.TrimPrefix(a, "1."))

	if a == finishMerge || a == finishLeaveOpen {
		return a
	}

	return ""
}

func (in *driveInput) takeAnswer(id string) string { return in.answers.take(id) }
