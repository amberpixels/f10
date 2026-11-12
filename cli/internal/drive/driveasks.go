package drive

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
func (in *DriveInput) raise(ctx context.Context, r *DriveRun, skill, text string) {
	if in.AsksDir == "" {
		return
	}

	asknotify.RaiseAsk(ctx, in.Out, in.AsksDir, asks.Ask{
		Task:     r.Task.Ref.ID,
		Root:     in.Root,
		Main:     in.Main,
		Project:  filepath.Base(in.Main),
		Worktree: r.O.Path,
		Suffix:   r.Task.Suffix,
		Drive:    in.number(),
		Skill:    skill,
		Text:     text,
	}, in.Now(), in.Notify)
}

// takeInbox: an answer the agent already heard makes the next send follow
// that turn; any other joins the answers as an --answer would. The inbox's
// answer came first, so an --answer for the same task goes unused.
func (in *DriveInput) takeInbox(tasks []DriveTask) {
	if in.AsksDir == "" || in.Root == "" {
		return
	}

	untaken, err := asks.Untaken(in.AsksDir, in.Root)
	if err != nil {
		fmt.Fprintf(in.Out, "  reading the asks inbox: %v\n", err)
	}

	for _, a := range untaken {
		i := slices.IndexFunc(tasks, func(t DriveTask) bool { return strings.EqualFold(t.Ref.ID, a.Task) })
		if i < 0 {
			continue
		}

		id := tasks[i].Ref.ID

		if given, ok := in.Answers.Keyed(id); ok && given.Text != a.Answer {
			in.inboxNotes = append(in.inboxNotes, fmt.Sprintf("--answer for %s went unused: %s answered it %s at %s",
				id, id, asks.Way(a.By), a.AnsweredAt.Local().Format("15:04")))
		}

		from := heardInbox
		if a.DeliveredAt.IsZero() || a.Skill == "finish" {
			from = fromInbox
		}

		in.Answers.Give(answer{Task: id, Text: a.Answer, From: from, At: a.AnsweredAt})

		in.taken(a.Key())
	}
}

func (in *DriveInput) taken(key string) {
	if err := asks.Update(in.AsksDir, key, func(a *asks.Ask) { a.TakenAt = in.Now() }); err != nil {
		fmt.Fprintf(in.Out, "  marking %s's answer taken: %v\n", key, err)
	}
}

// claimSent closes the task's open ask with the answer this drive sends, so
// every other way in shows it answered.
func (in *DriveInput) claimSent(r *DriveRun, answer string) {
	if in.AsksDir == "" {
		return
	}

	key := asks.Key(in.Root, r.Task.Ref.ID)
	if _, err := asks.Claim(in.AsksDir, key, asks.ByDrive, answer, true, in.Now()); err == nil {
		in.taken(key)
	}
}

// resumeAnswered reports an ask answered after this drive last looked, for
// which the drive must run again: the answer's claimer saw the drive's lease
// held and left it here. Outside the daemon it names the command instead.
func (in *DriveInput) resumeAnswered(tasks []DriveTask) bool {
	if in.Rec == nil || in.AsksDir == "" || in.Draining.Load() || in.Rec.Cancelled != nil {
		return false
	}

	untaken, _ := asks.Untaken(in.AsksDir, in.Root)

	for _, a := range untaken {
		if !slices.ContainsFunc(tasks, func(t DriveTask) bool { return strings.EqualFold(t.Ref.ID, a.Task) }) {
			continue
		}

		if !in.DaemonRun {
			fmt.Fprintf(in.Out, "%s was answered %s as this drive stopped: f10 drive --resume %d takes the answer\n",
				a.Task, asks.Way(a.By), in.Rec.Number)

			return false
		}

		fmt.Fprintf(in.Out, "%s was answered %s as this drive stopped: drive %d resumes for it\n",
			a.Task, asks.Way(a.By), in.Rec.Number)

		return true
	}

	return false
}

// CloseDriveAsks closes drive n's open asks: a cancelled drive leaves
// nothing waiting in the inbox.
func CloseDriveAsks(dir, root string, n int, now time.Time) {
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

func (in *DriveInput) TakeAnswers(tasks []DriveTask) error {
	in.Answers = NewAnswerBook()

	for _, a := range in.Args.Answers {
		if id, text, ok := keyedAnswer(a, tasks); ok {
			in.Answers.Give(answer{Task: id, Text: text, From: fromFlag})

			continue
		}

		if in.Answers.Bare != "" {
			return errors.New("several --answer values name no task: key each one, --answer GH-1=\"1. …\"")
		}

		in.Answers.Give(answer{Text: a, From: fromFlag})
	}

	return nil
}

// keyedAnswer splits an --answer that names one of the drive's tasks into
// its id and text; any other answer is bare, an "=" in its text included.
func keyedAnswer(a string, tasks []DriveTask) (string, string, bool) {
	key, text, ok := strings.Cut(a, "=")
	if !ok {
		return "", "", false
	}

	i := slices.IndexFunc(tasks, func(t DriveTask) bool { return strings.EqualFold(t.Ref.ID, strings.TrimSpace(key)) })
	if i < 0 {
		return "", "", false
	}

	return tasks[i].Ref.ID, strings.TrimSpace(text), true
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
func (in *DriveInput) finishAnswer(id string) (string, error) {
	if a, ok := in.Answers.takeKeyed(id); ok {
		in.took(id, "finish", a)

		if c := finishChoice(a); c != "" {
			return c, nil
		}

		return "", fmt.Errorf("the answer to finish is %q: answer %s or %s", a, finishLeaveOpen, finishMerge)
	}

	if c := finishChoice(in.Answers.Bare); c != "" {
		in.Answers.Bare = ""
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

func (in *DriveInput) takeAnswer(id string) string { return in.Answers.Take(id) }
