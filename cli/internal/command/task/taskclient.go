package task

import (
	"context"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	"github.com/amberpixels/f10/cli/internal/render"
)

// runTaskRead prints the task the daemon reads: its own text, with the
// images saved locally for an agent, never for a person at a terminal, who
// opens the links and would wait on the downloads.
func runTaskRead(ctx context.Context, cmd *cli.Command) error {
	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	caller, err := c.Caller(cmd)
	if err != nil {
		return err
	}

	res, err := c.Tasks.GetTask(ctx, connect.NewRequest(&f10v1.GetTaskRequest{
		Id: cmd.Args().First(), SkipImages: cmd.Bool("no-images"), Caller: caller,
	}))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	return render.WriteDoc(ctx, cmd.Writer, caller.GetCwd(), res.Msg.GetDocument())
}

// runTaskCreate sends the task on stdin to the daemon, which creates it
// through the driver, else as an issue on the host. f10 never retries it,
// since a failed write is an unknown outcome.
func runTaskCreate(ctx context.Context, cmd *cli.Command) error {
	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	caller, err := c.Caller(cmd)
	if err != nil {
		return err
	}

	// nowhere to create answers exit 3 before stdin is read, so capture falls back
	if _, err := c.Tasks.CreateTask(
		ctx,
		connect.NewRequest(&f10v1.CreateTaskRequest{Check: true, Caller: caller}),
	); err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	in, err := io.ReadAll(cmd.Reader)
	if err != nil {
		return fmt.Errorf("reading the task from stdin: %w", err)
	}

	p, err := readProps(cmd)
	if err != nil {
		return err
	}

	props := map[string]string{}

	for _, kv := range p.Set {
		k, v, _ := strings.Cut(kv, "=")
		props[k] = v
	}

	res, err := c.Tasks.CreateTask(ctx, connect.NewRequest(&f10v1.CreateTaskRequest{
		Markdown:   string(in),
		Status:     p.Status,
		Priority:   p.Priority,
		Labels:     p.Labels,
		Parent:     cmd.String("parent"),
		Properties: props,
		Caller:     caller,
	}))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	apiclient.Print(cmd.Writer, cmd.ErrWriter, res.Msg.GetPrinted())

	return nil
}
