package main

import (
	"cmp"
	"context"
	"fmt"
	"strconv"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// The pr noun needs nothing declared anywhere. Which CLI to reach for is a
// function of the git remote, which probe already answers with its
// evidence, so `f10 pr open` is right by construction in a repo whose host
// you would otherwise have to remember.
func prCommand() *cli.Command {
	return &cli.Command{
		Name:  "pr",
		Usage: "open a pull or merge request, or their list",
		Commands: []*cli.Command{
			{
				Name:      "open",
				Usage:     "open the PR/MR in the browser; with no number, the PR/MR list",
				ArgsUsage: "[number]",
				Flags:     []cli.Flag{linkFlag(), printFlag()},
				Action:    runPROpen,
			},
		},
	}
}

func runPROpen(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	h, err := t.host()
	if err != nil {
		return err
	}

	number := cmd.Args().First()

	switch {
	case number == "" && cmd.Bool("print"):
		rows, err := h.pullRows(ctx)
		if err != nil {
			return err
		}

		return writeRows(cmd.Writer, rows, "no open requests")
	case number == "":
		url, err := h.pullsURL(ctx)
		if err != nil {
			return err
		}

		return follow(ctx, cmd.Writer, h.dir, url, cmd.Bool("link"))
	case cmd.Bool("print"):
		return shell.Passthrough(ctx, h.dir, h.name, h.prArgs(number)...)
	}

	if cmd.Bool("link") {
		url, err := h.prURL(ctx, number)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(cmd.Writer, url)

		return err
	}

	return shell.Passthrough(ctx, h.dir, h.name, h.prArgs(number, "--web")...)
}

// prArgs is the subcommand each CLI names this thing: GitHub has pull
// requests, GitLab has merge requests.
func (h host) prArgs(number string, tail ...string) []string {
	args := h.pick([]string{"pr", "view"}, []string{"mr", "view"})

	if number != "" {
		args = append(args, number)
	}

	return append(args, tail...)
}

// pullRows is the first page of open requests, newest first, as rows: gh
// numbers them #n, glab !n.
func (h host) pullRows(ctx context.Context) ([]row, error) {
	var found []pull

	err := h.decode(ctx, &found, h.pick(
		[]string{"pr", "list", "--json", "number,title,state,url", "--limit", "30"},
		[]string{"mr", "list", "-F", "json", "--per-page", "30"},
	)...)
	if err != nil {
		return nil, err
	}

	sigil := "#"
	if h.glab() {
		sigil = "!"
	}

	rows := make([]row, 0, len(found))
	for _, p := range found {
		rows = append(rows, row{
			ID:     sigil + strconv.Itoa(cmp.Or(p.Number, p.IID)),
			Title:  p.Title,
			Status: p.status(),
			URL:    p.url(),
		})
	}

	return rows, nil
}

// prURL is the url of the request number names.
func (h host) prURL(ctx context.Context, number string) (string, error) {
	var payload map[string]any

	err := h.decode(ctx, &payload, h.pick(
		h.prArgs(number, "--json", "url"),
		h.prArgs(number, "-F", "json"),
	)...)
	if err != nil {
		return "", err
	}

	field := "url"
	if h.glab() {
		field = "web_url"
	}

	url, _ := payload[field].(string)
	if url == "" {
		return "", fmt.Errorf("%s returned no %s", h.name, field)
	}

	return url, nil
}
