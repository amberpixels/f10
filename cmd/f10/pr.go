package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/internal/shell"
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

	host, err := t.hostCLI()
	if err != nil {
		return err
	}

	// no number is a question about a branch, and the other checkout's
	// branch is as real as this one's - so -C changes nothing here
	number := cmd.Args().First()

	if cmd.Bool("print") {
		url, err := prURL(ctx, t, host, number)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(cmd.Writer, url)

		return err
	}

	return shell.Passthrough(ctx, t.dir, host, prArgs(host, number, "--web")...)
}

// prArgs is the subcommand each CLI names this thing: GitHub has pull
// requests, GitLab has merge requests.
func prArgs(host, number string, tail ...string) []string {
	args := []string{"pr", "view"}
	if host == "glab" {
		args = []string{"mr", "view"}
	}

	if number != "" {
		args = append(args, number)
	}

	return append(args, tail...)
}

func prURL(ctx context.Context, t *target, host, number string) (string, error) {
	var (
		args  []string
		field string
	)

	switch host {
	case "glab":
		args, field = prArgs(host, number, "-F", "json"), "web_url"
	default:
		args, field = prArgs(host, number, "--json", "url"), "url"
	}

	res, err := shell.Capture(ctx, t.dir, host, args...)
	if err != nil {
		return "", fmt.Errorf("running %s: %w", host, err)
	}

	if res.Code != 0 {
		return "", fmt.Errorf("%s: %s", host, firstNonEmpty(res.Stderr, fmt.Sprintf("exit %d", res.Code)))
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		return "", fmt.Errorf("parsing %s output: %w", host, err)
	}

	url, _ := payload[field].(string)
	if url == "" {
		return "", fmt.Errorf("%s returned no %s", host, field)
	}

	return url, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}
