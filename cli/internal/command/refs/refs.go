// Package refs is the f10 refs command. It adds the links that pr and task
// writes add on the way out to a file f10 never sends, such as a page
// published another way.
package refs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/command"
	"github.com/amberpixels/f10/cli/internal/reflink"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/taskwrite"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the refs verb.
func Command() *cli.Command {
	return &cli.Command{
		Name:     "refs",
		Hidden:   true,
		Category: command.Plumbing,
		Usage:    "link the first mention of each task id in a file that renders links: markdown, or HTML by extension",
		Commands: []*cli.Command{
			{
				Name:      "check",
				Usage:     "name each first mention left bare that f10 can title (exit 1: any)",
				ArgsUsage: "<file>",
				Action:    runCheck,
			},
			{
				Name:      "link",
				Usage:     "rewrite each first mention left bare into a link, in place, and name the ids linked",
				ArgsUsage: "<file>",
				Action:    runLink,
			},
		},
	}
}

func runCheck(ctx context.Context, cmd *cli.Command) error {
	path, text, scan, t, err := open(ctx, cmd)
	if err != nil {
		return err
	}

	// the lookup runs, so only an id that could be linked fails the check
	_, mentions := taskwrite.LinkWith(ctx, t, scan, text, &strings.Builder{})

	var (
		bare    int
		unknown []string
	)

	for _, m := range mentions {
		if !m.Linked {
			unknown = append(unknown, m.ID)

			continue
		}

		bare++

		if _, err := fmt.Fprintf(cmd.Writer, "%s:%d: %s is not linked\n", path, m.Line, m.ID); err != nil {
			return err
		}
	}

	if len(unknown) > 0 {
		ui.Warnf(cmd.ErrWriter, "%s: no title or url f10 can read, left bare: %s", path, strings.Join(unknown, ", "))
	}

	if bare > 0 {
		return ui.Exit(1, "%d %s left bare: run f10 refs link %s", bare, textx.Plural(bare, "id", "ids"), path)
	}

	return nil
}

func runLink(ctx context.Context, cmd *cli.Command) error {
	path, text, scan, t, err := open(ctx, cmd)
	if err != nil {
		return err
	}

	out, mentions := taskwrite.LinkWith(ctx, t, scan, text, cmd.ErrWriter)
	if out == text {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(out), info.Mode().Perm()); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	for _, m := range mentions {
		if m.Linked {
			if _, err := fmt.Fprintln(cmd.Writer, m.ID); err != nil {
				return err
			}
		}
	}

	return nil
}

func open(ctx context.Context, cmd *cli.Command) (string, string, reflink.Scan, *target.Target, error) {
	if cmd.Args().Len() != 1 {
		return "", "", nil, nil, ui.Exit(ui.ExitUsage, "refs %s takes one <file>", cmd.Name)
	}

	path := filepath.Clean(cmd.Args().First())

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return "", "", nil, nil, err
	}

	scan := reflink.Markdown
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".html" || ext == ".htm" {
		scan = reflink.HTML
	}

	return path, string(data), scan, t, nil
}
