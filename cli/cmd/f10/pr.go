package main

import (
	"context"
	"fmt"

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
		Usage: "open the pull or merge request for a branch",
		Commands: []*cli.Command{
			{
				Name:      "open",
				Usage:     "open the PR/MR in the browser",
				ArgsUsage: "[number]",
				Flags:     []cli.Flag{printFlag()},
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

	// no number is a question about a branch, and the other checkout's
	// branch is as real as this one's - so -C changes nothing here
	number := cmd.Args().First()

	if cmd.Bool("print") {
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

// prURL is the request's url: the one for number, or for the current
// branch's request when number is "".
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
