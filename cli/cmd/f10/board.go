package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// The board noun reads the host, the tracker and this machine, each a
// source that can fail alone, and derives nothing: lookaround decides every
// group and stage, and the terminal, --json and --html draw the same rows.
func boardCommand() *cli.Command {
	return &cli.Command{
		Name: "board",
		Usage: "what changed today (else the last working day): started, updated, reviewed, finished, new tasks; " +
			"--state for where every task stands (this project, or several with -C, --group, --all)",
		ArgsUsage: "[today | yesterday | last-workday | this-week | last-week | DATE]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: "since",
				Usage: "the `WINDOW`, as the argument names one, or an RFC 3339 time (until now); " +
					"with --state, where done rows start (default 7 days)",
			},
			&cli.BoolFlag{
				Name:  "state",
				Usage: "where every task stands instead of what changed: in progress with its stage, next, done",
			},
			&cli.BoolFlag{
				Name:  "remote",
				Usage: "read the host and the tracker only, as a viewer without the checkout would",
			},
			&cli.StringSliceFlag{
				Name:  "group",
				Usage: "read the projects of `NAME`, a group in ~/.f10/board.md, instead of this one; repeatable",
			},
			&cli.BoolFlag{
				Name:  "all",
				Usage: "read every checkout under F10_ROOTS, and the repos ~/.f10/board.md watches",
			},
			&cli.BoolFlag{Name: "detail", Usage: "with --all or --group, every project's rows instead of a line each"},
			&cli.StringFlag{Name: "by", Usage: "group rows by `person` across projects"},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit the Board JSON (the schema is docs/board.schema.json); an array over several projects",
			},
			&cli.BoolFlag{
				Name:  "html",
				Usage: "emit one static HTML page: the Board JSON embedded, drawn by an inlined renderer; opens from disk",
			},
			&cli.StringFlag{
				Name:  "out",
				Usage: "write --json or --html to this `PATH` instead of stdout",
			},
		},
		Action: runBoard,
	}
}

const boardPage = 100

// The board's output formats; the empty one is the terminal.
const (
	boardJSON = "json"
	boardHTML = "html"
)

func runBoard(ctx context.Context, cmd *cli.Command) error {
	format, err := boardFormat(cmd)
	if err != nil {
		return err
	}

	now := time.Now()
	stateView := cmd.Bool("state")

	arg := cmd.Args().First()

	switch {
	case cmd.Args().Len() > 1:
		return fmt.Errorf("board takes one window, got %q", strings.Join(cmd.Args().Slice(), " "))
	case cmd.IsSet("since") && arg != "":
		return errors.New("name the window once: as the argument or with --since")
	case cmd.IsSet("since"):
		arg = cmd.String("since")
	}

	span, err := resolveBoardSpan(arg, stateView, now, readWorkdays)
	if err != nil {
		return err
	}

	by := strings.TrimSpace(cmd.String("by"))
	if by != "" && by != "person" {
		return fmt.Errorf("--by %q: rows group by person only", by)
	}

	cfg, cfgPath, err := readBoardConfig()
	if err != nil {
		return err
	}

	people := boardPeople(cfg.People)
	remote := cmd.Bool("remote")

	// one pipeline whatever the scope: this checkout is a scope of one
	var entries []scopeEntry

	if scoped(cmd) {
		items, err := scopeItems(cmd, boardConfigFile, cfg.Repos, cfgPath)
		if err != nil {
			return err
		}

		if entries = bindScope(ctx, items, cmd.Name); len(entries) == 0 {
			return errors.New("the scope names no project: the groups and roots given matched none")
		}
	} else {
		t, err := targetFor(ctx, cmd)
		if err != nil {
			return err
		}

		entries = []scopeEntry{{dir: t.dir, target: t}}
	}

	boards := collectBoards(ctx, entries, cmd.ErrWriter, now, span, stateView, remote, people)

	// several projects (--all, --group, more than one -C) are a JSON array
	// and a line each unless --detail; the page draws one project
	several := cmd.Bool("all") || len(cmd.StringSlice("group")) > 0 || len(checkoutFlags(cmd)) > 1
	summary := (cmd.Bool("all") || len(cmd.StringSlice("group")) > 0) && !cmd.Bool("detail")

	switch {
	case format == boardHTML && several:
		return errors.New("--html draws one project: name one with -C, or none")
	case format != "" && several:
		var buf bytes.Buffer
		if err := encodeJSON(&buf, boards); err != nil {
			return err
		}

		return emitBoard(cmd.Writer, cmd.String("out"), buf.Bytes())
	case format != "":
		data, err := boardBytes(boards[0], format)
		if err != nil {
			return err
		}

		return emitBoard(cmd.Writer, cmd.String("out"), data)
	case by == "person":
		renderByPerson(cmd.Writer, boards, termWidth(cmd.Writer))
	case summary:
		renderSummary(cmd.Writer, boards, now, termWidth(cmd.Writer))
	case !several:
		renderBoard(cmd.Writer, boards[0], termWidth(cmd.Writer))
	default:
		faint := lipgloss.NewStyle().Faint(true).Bold(true)

		for i, b := range boards {
			if i > 0 {
				fmt.Fprintln(cmd.Writer)
			}

			fmt.Fprintln(cmd.Writer, faint.Render("## "+b.Project.Name))
			renderBoard(cmd.Writer, b, termWidth(cmd.Writer))
		}
	}

	return nil
}

// boardFormat settles the format before anything is collected; --out has no
// meaning for the terminal, whose width is the terminal's.
func boardFormat(cmd *cli.Command) (string, error) {
	switch {
	case cmd.Bool("json") && cmd.Bool("html"):
		return "", errors.New("--json and --html are exclusive: pick one")
	case cmd.Bool("json"):
		return boardJSON, nil
	case cmd.Bool("html"):
		return boardHTML, nil
	case cmd.String("out") != "":
		return "", errors.New("--out writes --json or --html: add one")
	default:
		return "", nil
	}
}

func boardBytes(b lookaround.Board, format string) ([]byte, error) {
	var buf bytes.Buffer

	var err error
	if format == boardHTML {
		err = renderBoardHTML(&buf, b)
	} else {
		err = encodeJSON(&buf, b)
	}

	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func emitBoard(w io.Writer, path string, data []byte) error {
	if path == "" {
		_, err := w.Write(data)

		return err
	}

	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a report meant to be attached or hosted
		return fmt.Errorf("writing the board: %w", err)
	}

	return nil
}
