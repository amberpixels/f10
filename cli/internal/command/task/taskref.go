package task

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/taskwrite"
)

// Text that leaves the machine names another task by its title and url.
// `task ref` hands them out from the ledger's task refs, which every
// tracker read f10 makes fills, and asks the tracker only on a miss.

// MdLink is taskwrite.MdLink.
func MdLink(id, title, url string) string { return taskwrite.MdLink(id, title, url) }

func runTaskRef(ctx context.Context, cmd *cli.Command) error {
	tokens := cmd.Args().Slice()
	if len(tokens) == 0 {
		return errors.New("ref needs a task id")
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	md := cmd.Bool("md")
	refs := make([]ref.Ref, len(tokens))

	for i, token := range tokens {
		// a bundle is several tasks, not one reference
		if strings.Contains(token, ref.Separator) {
			continue
		}

		if r, err := t.Reference(ctx, token); err == nil {
			refs[i] = r
		}
	}

	known := statusview.TaskRefsFor(t, ctx, refs, md, cmd.Bool("fresh"), cmd.ErrWriter)

	for i, token := range tokens {
		if _, err := fmt.Fprintln(cmd.Writer, RefLine(token, refs[i].ID, known[refs[i].ID], md)); err != nil {
			return err
		}
	}

	return nil
}

func RefLine(token, id string, k ledger.TaskRef, md bool) string {
	switch {
	case id == "":
		return token
	case k.Title == "", md && k.URL == "":
		return id
	case md:
		return MdLink(id, k.Title, k.URL)
	}

	return id + " (" + k.Title + ")"
}
