// Package asks is the f10 asks command: the questions driven agents wait on,
// listed, answered, or walked in a terminal.
package asks

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	"github.com/amberpixels/f10/cli/gen/agents/v1/agentsv1connect"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	"github.com/amberpixels/f10/cli/internal/asknotify"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/ui"
)

const recentAnswered = 24 * time.Hour

// Command is the asks noun: the questions driven agents wait on, listed,
// answered or walked in a terminal.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "asks",
		Usage: "the questions driven agents wait on, across projects: list, answer, or walk them in a terminal",
		Description: "A driven agent's ask, or a drive's finish question, waits here until the first answer from " +
			"any way in: its own pane, f10 asks, /f10:asks or f10 drive --answer. An answer to a drive's task " +
			"resumes the drive; one to an agent no drive watches is sent to it. Bare f10 asks walks the open asks " +
			"in a terminal and lists them otherwise. ~/.f10/asks.md sets notify, the command run for each new ask.",
		Action: runAsks,
		Commands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "the open asks, then those answered in the last day",
				Flags:  []cli.Flag{ui.JSONFlag("print JSON, for /f10:asks")},
				Action: runAsksList,
			},
			{
				Name:      "answer",
				Usage:     "answer a task's open ask; questions not named take their default",
				ArgsUsage: "<task | project:task> [N=VALUE ...]",
				Description: "VALUE is an option's letter (b), its number (2) or its text; free text answers only " +
					"a question with no options.",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "by", Value: asks.ByAsks, Usage: "the way in to record: asks or skill"},
				},
				Action: runAsksAnswer,
			},
			{
				Name:      "resume",
				Usage:     "resume the drive an answered ask belongs to",
				ArgsUsage: "<key>",
				Hidden:    true,
				Action:    runAsksResume,
			},
			{
				Name:   "hook",
				Usage:  "tell a session the open asks it has not been told; the payload on stdin",
				Hidden: true,
				Action: runAsksHook,
			},
		},
	}
}

func runAsks(ctx context.Context, cmd *cli.Command) error {
	if !ui.Interactive() {
		return runAsksList(ctx, cmd)
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	return walk(ctx, cmd.Writer, bufio.NewReader(os.Stdin), c.Asks, c.AgentsCaller(cmd))
}

type askView struct {
	Key        string          `json:"key"`
	Task       string          `json:"task"`
	Project    string          `json:"project,omitempty"`
	URL        string          `json:"url,omitempty"`
	Skill      string          `json:"skill,omitempty"`
	Drive      int             `json:"drive,omitempty"`
	Text       string          `json:"text"`
	Questions  []asks.Question `json:"questions"`
	AskedAt    time.Time       `json:"asked_at"`
	Answer     string          `json:"answer,omitempty"`
	By         string          `json:"by,omitempty"`
	AnsweredAt *time.Time      `json:"answered_at,omitempty"`
}

type AskList struct {
	Open     []askView `json:"open"`
	Answered []askView `json:"answered"`
}

// viewOf is the ask as a reader answers it.
//
//nolint:lostfield // a reduction: the paths and delivery bookkeeping stay in the inbox
func viewOf(a asks.Ask) askView {
	v := askView{
		Key: a.Key(), Task: a.Task, Project: a.Project, URL: a.URL, Skill: a.Skill, Drive: a.Drive,
		Text: a.Text, Questions: a.Questions, AskedAt: a.AskedAt, Answer: a.Answer, By: a.By,
	}
	if !a.AnsweredAt.IsZero() {
		v.AnsweredAt = &a.AnsweredAt
	}

	return v
}

func runAsksList(ctx context.Context, cmd *cli.Command) error {
	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	return list(ctx, cmd.Writer, c.Asks, c.AgentsCaller(cmd), time.Now(), cmd.Bool("json"))
}

// ListAsks prints the inbox in dir, served in process: the asks command's
// list for a test's inbox.
func ListAsks(ctx context.Context, w io.Writer, dir string, now time.Time, asJSON bool) error {
	return list(ctx, w, apiclient.AsksIn(dir, Deliverer{}), nil, now, asJSON)
}

func list(
	ctx context.Context, w io.Writer, c agentsv1connect.AskServiceClient, caller *agentsv1.CallerContext,
	now time.Time, asJSON bool,
) error {
	inbox, err := readInbox(ctx, c, caller, now.Add(-recentAnswered))
	if err != nil {
		return err
	}

	open, answered := inbox.Open, inbox.Answered

	if asJSON {
		out := AskList{Open: []askView{}, Answered: []askView{}}

		for _, a := range open {
			out.Open = append(out.Open, viewOf(a))
		}

		for _, a := range answered {
			out.Answered = append(out.Answered, viewOf(a))
		}

		return ui.JSON(w, out)
	}

	if len(open) == 0 {
		fmt.Fprintln(w, "no open asks")
	}

	links := linksOf(w, inbox)

	for i, a := range open {
		if i > 0 {
			fmt.Fprintln(w)
		}

		printAsk(w, links, a, now)
	}

	if len(answered) > 0 {
		fmt.Fprintln(w, "\nanswered in the last day:")

		for _, a := range answered {
			fmt.Fprintf(
				w,
				"  %s answered %s at %s: %s\n",
				asknotify.AskLink(
					links,
					a,
				),
				asks.Way(a.By),
				a.AnsweredAt.Local().Format("15:04"),
				cmp.Or(a.Answer, "(nothing sent)"),
			)
		}
	}

	return nil
}

// readInbox is the open asks, and those answered since since when it is set.
func readInbox(
	ctx context.Context, c agentsv1connect.AskServiceClient, caller *agentsv1.CallerContext, since time.Time,
) (asks.Inbox, error) {
	req := &agentsv1.ListAsksRequest{Caller: caller}
	if !since.IsZero() {
		req.AnsweredSince = timestamppb.New(since)
	}

	res, err := c.ListAsks(ctx, connect.NewRequest(req))
	if err != nil {
		return asks.Inbox{}, apiclient.Err(err, io.Discard, io.Discard)
	}

	var inbox asks.Inbox
	if err := apiclient.View(res.Msg.GetView(), &inbox); err != nil {
		return asks.Inbox{}, err
	}

	return inbox, nil
}

// linksOf links each ask's task on a terminal, by the urls the inbox knows.
func linksOf(w io.Writer, inbox asks.Inbox) *render.TaskLinks {
	links := render.LinksFor(w, false)

	for project, urls := range inbox.Links {
		for id, url := range urls {
			links.Known(project, id, url)
		}
	}

	return links
}

func printAsk(w io.Writer, links *render.TaskLinks, a asks.Ask, now time.Time) {
	where := ""
	if a.Skill != "" {
		where = " at " + a.Skill
	}

	fmt.Fprintf(w, "%s asks%s, %s ago\n", asknotify.AskLink(links, a), where, now.Sub(a.AskedAt).Round(time.Minute))

	for _, q := range a.Questions {
		fmt.Fprintf(w, "  %d. %s\n", q.N, q.Text)

		if len(q.Options) > 0 {
			fmt.Fprintf(w, "     %s  (default %s)\n", asks.Letters(q.Options), q.Default())
		}
	}

	if a.URL != "" {
		fmt.Fprintf(w, "  %s\n", a.URL)
	}
}

// A Deliverer moves a claimed answer on (agents.Deliverer).
type Deliverer = agents.Deliverer

func runAsksAnswer(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	if len(args) == 0 {
		return errors.New("answer needs a task: f10 asks answer <task> [N=VALUE ...]")
	}

	by := cmd.String("by")
	if by != asks.ByAsks && by != asks.BySkill {
		return fmt.Errorf("--by takes %s or %s", asks.ByAsks, asks.BySkill)
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	return answer(ctx, cmd.Writer, c.Asks, c.AgentsCaller(cmd), args[0], args[1:], by)
}

// AnswerAsk answers token's open ask in the inbox in dir with d moving it
// on, served in process: the asks command's answer for a test's inbox.
func AnswerAsk(ctx context.Context, w io.Writer, dir, token string, values []string, by string, d Deliverer) error {
	return answer(ctx, w, apiclient.AsksIn(dir, d), nil, token, values, by)
}

func answer(
	ctx context.Context, w io.Writer, c agentsv1connect.AskServiceClient, caller *agentsv1.CallerContext,
	token string, values []string, by string,
) error {
	res, err := c.AnswerAsk(ctx, connect.NewRequest(&agentsv1.AnswerAskRequest{
		Match: token, Values: values, AnsweredBy: by, Caller: caller,
	}))
	if err != nil {
		return apiclient.Err(err, w, w)
	}

	_, err = io.WriteString(w, res.Msg.GetOutput())

	return err
}

// WalkAsks walks the inbox in dir with d moving answers on, served in
// process: the asks command's walk for a test's inbox.
func WalkAsks(ctx context.Context, w io.Writer, in *bufio.Reader, dir string, d Deliverer) error {
	return walk(ctx, w, in, apiclient.AsksIn(dir, d), nil)
}

// walk puts each open ask's questions to the person at the terminal, and
// answers it unless they skip it.
func walk(
	ctx context.Context,
	w io.Writer,
	in *bufio.Reader,
	c agentsv1connect.AskServiceClient,
	caller *agentsv1.CallerContext,
) error {
	inbox, err := readInbox(ctx, c, caller, time.Time{})
	if err != nil {
		return err
	}

	open := inbox.Open
	if len(open) == 0 {
		fmt.Fprintln(w, "no open asks")

		return nil
	}

	links := linksOf(w, inbox)

	for i, a := range open {
		if i > 0 {
			fmt.Fprintln(w)
		}

		printAsk(w, links, a, time.Now())

		var values []string

		skip := false

		for _, q := range a.Questions {
			value, err := askLine(w, in, q)
			if err != nil {
				return err
			}

			if value == "s" {
				skip = true

				break
			}

			values = append(values, strconv.Itoa(q.N)+"="+value)
		}

		if skip {
			fmt.Fprintln(w, "skipped")

			continue
		}

		res, err := c.AnswerAsk(ctx, connect.NewRequest(&agentsv1.AnswerAskRequest{
			Id: a.Key(), Values: values, AnsweredBy: asks.ByAsks, Caller: caller,
		}))
		if err != nil {
			fmt.Fprintln(w, apiclient.Err(err, w, w))

			continue
		}

		_, _ = io.WriteString(w, res.Msg.GetOutput())
	}

	return nil
}

// askLine reads one question's answer until it resolves: Enter is the
// default, s skips the ask.
func askLine(w io.Writer, in *bufio.Reader, q asks.Question) (string, error) {
	for {
		prompt := fmt.Sprintf("%d> ", q.N)
		if d := q.Default(); d != "" {
			prompt = fmt.Sprintf("%d [%s]> ", q.N, d)
		}

		fmt.Fprint(w, prompt)

		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}

		line = strings.TrimSpace(line)

		switch {
		case line == "s":
			return "s", nil
		case line == "" && q.Default() != "":
			return q.Default(), nil
		}

		resolved, err := asks.Resolve(q, line)
		if err == nil {
			return resolved, nil
		}

		fmt.Fprintln(w, err)
	}
}
