package finish

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/target"
)

func JanitorCommand() *cli.Command {
	return &cli.Command{
		Name: "janitor",
		Usage: "remove task branches whose PR merged or closed, locally and on origin, and archive their plans; " +
			"f10 start and f10 finish run it every few hours",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Usage: "list what would be removed and change nothing"},
		},
		Action: runJanitor,
	}
}

func runJanitor(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	in := finish.JanitorFor(t)
	in.Dry = cmd.Bool("dry-run")

	if !in.Dry {
		if err := finish.TouchStamp(
			filepath.Join(t.Layout.StorageRoot.Path(), finish.JanitorStamp),
			time.Now(),
		); err != nil {
			return err
		}
	}

	lines, err := finish.Janitor(ctx, in)
	for _, l := range lines {
		fmt.Fprintln(cmd.Writer, l)
	}

	return err
}
