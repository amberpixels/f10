package pr

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// prChecks is what --json prints: the request and its head's checks.
type prChecks struct {
	forge.HeadChecks

	PR string `json:"pr"`
}

// runPRChecks always asks the host: a result belongs to one head, so no
// copy is kept to read instead.
func runPRChecks(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := reviews.ResolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	if s.LookupErr != nil {
		return s.LookupErr
	}

	if s.Number == 0 {
		return fmt.Errorf("no open PR for %s", s.Branch)
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	p, err := h.PR(ctx, strconv.Itoa(s.Number))
	if err != nil {
		return err
	}

	hc, err := h.HeadChecks(ctx, p)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		err = ui.JSON(cmd.Writer, prChecks{PR: p.URL, HeadChecks: hc})
	} else {
		rows := ChecksFacts(p.URL, hc)
		err = render.WriteFacts(cmd.Writer, rows, render.LabelWidth(rows), ui.Width(cmd.Writer))
	}

	return errors.Join(err, checksExit(hc.State))
}

// ChecksFacts is the block pr checks prints: the request, its head, the
// state and the set counted, then a row per failed job with its log.
func ChecksFacts(url string, hc forge.HeadChecks) []render.Fact {
	rows := []render.Fact{{Label: "pr", Value: url, URL: true}, {Label: "head", Value: hc.SHA}}

	switch {
	case hc.State == "":
		rows = append(rows, render.Fact{Label: "checks", Value: "none"})
	case hc.Required:
		rows = append(rows, render.Fact{Label: "checks", Value: hc.State + " (required)"})
	default:
		rows = append(rows, render.Fact{Label: "checks", Value: hc.State + " (all)"})
	}

	for _, c := range hc.Failed {
		rows = append(rows, render.Fact{Label: "failed " + c.Name, Value: c.URL, URL: c.URL != ""})
	}

	if names := forge.Names(hc.Running); names != "" {
		rows = append(rows, render.Fact{Label: "running", Value: names})
	}

	return rows
}

func checksExit(state string) error {
	switch state {
	case forge.Red:
		return cli.Exit("", 1)
	case forge.Pending:
		return cli.Exit("", ui.ExitPending)
	}

	return nil
}
