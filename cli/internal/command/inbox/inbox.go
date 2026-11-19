// Package inbox is the f10 inbox command: the questions driven agents wait
// on, listed, answered, or walked in a terminal.
package inbox

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
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
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/ui"
)

const recentAnswered = 24 * time.Hour

// Command is the inbox noun: the questions driven agents wait on, listed,
// answered or walked in a terminal.
func Command() *cli.Command {
	return noun("inbox")
}

// AsksAlias is the inbox under its old name, hidden, for a session started
// before a same-version local rebuild: its hooks.json still runs f10 asks
// hook against the new binary. Drop it in v0.68.
func AsksAlias() *cli.Command {
	c := noun("asks")
	c.Hidden = true

	return c
}

func noun(name string) *cli.Command {
	return &cli.Command{
		Name:  name,
		Usage: "the questions driven agents wait on in this project: list, answer, or walk them in a terminal",
		Description: "A driven agent's ask, or a drive's finish question, waits here until the first answer from " +
			"any way in: its own pane, f10 inbox, /f10:inbox or f10 start --answer. An answer to a drive's task " +
			"resumes the drive; one to an agent no drive watches is sent to it. Bare f10 inbox walks the open asks " +
			"in a terminal and lists them otherwise. Both show this project's asks, and --all every project's. " +
			"~/.f10/inbox.md sets notify, the command run for each new ask.",
		Flags:  []cli.Flag{allFlag()},
		Action: runInbox,
		Commands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "the open asks, then those answered in the last day",
				Flags:  []cli.Flag{ui.JSONFlag("print JSON, for /f10:inbox"), allFlag()},
				Action: runInboxList,
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
				Action: runInboxAnswer,
			},
			{
				Name:      "resume",
				Usage:     "resume the drive an answered ask belongs to",
				ArgsUsage: "<key>",
				Hidden:    true,
				Action:    runInboxResume,
			},
			{
				Name:   "hook",
				Usage:  "tell a session the open asks it has not been told; the payload on stdin",
				Hidden: true,
				Action: runInboxHook,
			},
		},
	}
}

func allFlag() cli.Flag {
	return &cli.BoolFlag{Name: "all", Usage: "every project's asks, not only this project's"}
}

// scopeOf is the project a list shows, the cwd's. It is "" for every
// project's, with --all or outside git.
func scopeOf(ctx context.Context, cmd *cli.Command) string {
	if cmd.Bool("all") {
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	return ProjectOf(layout.Locate(ctx, cwd))
}

func runInbox(ctx context.Context, cmd *cli.Command) error {
	if !ui.Interactive() {
		return runInboxList(ctx, cmd)
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	return walk(ctx, cmd.Writer, bufio.NewReader(os.Stdin), c.Inbox, c.AgentsCaller(cmd), scopeOf(ctx, cmd))
}

type askView struct {
	Key        string          `json:"key"`
	Task       string          `json:"task"`
	Project    string          `json:"project,omitempty"`
	URL        string          `json:"url,omitempty"`
	Skill      string          `json:"skill,omitempty"`
	Drive      int             `json:"drive,omitempty"`
	Text       string          `json:"text"`
	Context    string          `json:"context,omitempty"`
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
		Text: a.Text, Context: a.Context, Questions: a.Questions, AskedAt: a.AskedAt, Answer: a.Answer, By: a.By,
	}
	if !a.AnsweredAt.IsZero() {
		v.AnsweredAt = &a.AnsweredAt
	}

	return v
}

func runInboxList(ctx context.Context, cmd *cli.Command) error {
	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	return list(ctx, cmd.Writer, c.Inbox, c.AgentsCaller(cmd), scopeOf(ctx, cmd), time.Now(), cmd.Bool("json"))
}

// ListAsks prints the inbox in dir for the project at project, every
// project's when empty, served in process: the inbox command's list for a
// test's inbox.
func ListAsks(ctx context.Context, w io.Writer, dir, project string, now time.Time, asJSON bool) error {
	return list(ctx, w, apiclient.InboxIn(dir, Deliverer{}), nil, project, now, asJSON)
}

func list(
	ctx context.Context, w io.Writer, c agentsv1connect.InboxServiceClient, caller *agentsv1.CallerContext,
	project string, now time.Time, asJSON bool,
) error {
	inbox, err := readInbox(ctx, c, caller, project, now.Add(-recentAnswered))
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

		printAsk(w, links, a, project == "", now)
	}

	if len(answered) > 0 {
		fmt.Fprintln(w, "\nanswered in the last day:")

		for _, a := range answered {
			fmt.Fprintf(
				w,
				"  "+asks.InboxIcon+" %s answered %s at %s: %s\n",
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

// readInbox is the open asks of the project at project, every project's
// when empty, and those answered since since when it is set.
func readInbox(
	ctx context.Context, c agentsv1connect.InboxServiceClient, caller *agentsv1.CallerContext,
	project string, since time.Time,
) (asks.Inbox, error) {
	req := &agentsv1.ListAsksRequest{Caller: caller, Project: project}
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

// printAsk draws a as its banner, then how long it waited, its task's url
// and its context, which the banner leaves out.
func printAsk(w io.Writer, links *render.TaskLinks, a asks.Ask, named bool, now time.Time) {
	fmt.Fprint(w, asks.Banner(a, askName(links, a, named)))
	fmt.Fprintf(w, " asked %s ago\n", now.Sub(a.AskedAt).Round(time.Minute))

	if a.URL != "" {
		fmt.Fprintf(w, " %s\n", a.URL)
	}

	if a.Context != "" {
		for _, line := range render.Wrap(a.Context, max(0, ui.Width(w)-1)) {
			fmt.Fprintf(w, " %s\n", line)
		}
	}
}

// askName is a's task as a list labels it: linked on a terminal, and named
// with its project in a list that spans several.
func askName(links *render.TaskLinks, a asks.Ask, named bool) string {
	// the url an ask carries is its lead's, so a bundle's members link from the ledger
	if !strings.Contains(a.Task, ref.Separator) {
		links.Known(a.Project, a.Task, a.URL)
	}

	name := links.ID(a.Project, a.Task)

	if named && a.Project != "" {
		name = a.Project + ":" + name
	}

	return name
}

// A Deliverer moves a claimed answer on (agents.Deliverer).
type Deliverer = agents.Deliverer

func runInboxAnswer(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	if len(args) == 0 {
		return errors.New("answer needs a task: f10 inbox answer <task> [N=VALUE ...]")
	}

	by := cmd.String("by")
	if by != asks.ByAsks && by != asks.BySkill {
		return fmt.Errorf("--by takes %s or %s", asks.ByAsks, asks.BySkill)
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	return answer(ctx, cmd.Writer, c.Inbox, c.AgentsCaller(cmd), args[0], args[1:], by)
}

// AnswerAsk answers token's open ask in the inbox in dir with d moving it
// on, served in process: the inbox command's answer for a test's inbox.
func AnswerAsk(ctx context.Context, w io.Writer, dir, token string, values []string, by string, d Deliverer) error {
	return answer(ctx, w, apiclient.InboxIn(dir, d), nil, token, values, by)
}

func answer(
	ctx context.Context, w io.Writer, c agentsv1connect.InboxServiceClient, caller *agentsv1.CallerContext,
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
// process: the inbox command's walk for a test's inbox, every project's.
func WalkAsks(ctx context.Context, w io.Writer, in *bufio.Reader, dir string, d Deliverer) error {
	return walk(ctx, w, in, apiclient.InboxIn(dir, d), nil, "")
}

// walk puts each open ask's questions to the person at the terminal, and
// answers it unless they skip it.
func walk(
	ctx context.Context,
	w io.Writer,
	in *bufio.Reader,
	c agentsv1connect.InboxServiceClient,
	caller *agentsv1.CallerContext,
	project string,
) error {
	inbox, err := readInbox(ctx, c, caller, project, time.Time{})
	if err != nil {
		return err
	}

	open := inbox.Open
	if len(open) == 0 {
		fmt.Fprintln(w, "no open asks")

		return nil
	}

	links := linksOf(w, inbox)
	view := newWalkView(w, links.On)

	for i, a := range open {
		if i > 0 {
			fmt.Fprintln(w)
		}

		qs := shownQuestions(a)
		view.ask(a, qs, askName(links, a, project == ""), time.Now())

		var values []string

		skip := false

		for _, q := range qs {
			value, err := askLine(w, in, q)
			if err != nil {
				return err
			}

			if value == "s" {
				skip = true

				break
			}

			// against the stored question: a recovered option goes as its
			// text, which a question without options takes
			values = append(values, strconv.Itoa(q.N)+"="+wireValue(q.Stored, value))
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

// wireValue is a resolved answer as the daemon resolves it again: an option
// by its letter, which no option's own text can shadow, as the text "1"
// would among options 2 and 1; free text as typed.
func wireValue(q asks.Question, resolved string) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"

	if i := slices.Index(q.Options, resolved); i >= 0 && i < len(letters) {
		return letters[i : i+1]
	}

	return resolved
}

// askLine reads one question's answer until it resolves to an option, by
// letter, number or text. Enter takes the default, and s skips the ask. A
// recovered question takes free text too, its options being a guess.
func askLine(w io.Writer, in *bufio.Reader, q shownQuestion) (string, error) {
	for {
		// the default is option a, as the walk letters them
		prompt := fmt.Sprintf("%d> ", q.N)
		if q.Default() != "" {
			prompt = fmt.Sprintf("%d [a]> ", q.N)
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

		resolved, err := asks.Resolve(q.Question, line)
		if err == nil {
			return resolved, nil
		}

		if q.Recovered {
			return line, nil
		}

		fmt.Fprintln(w, err)
	}
}
