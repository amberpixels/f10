// Package atlas is the atlas command: the board's scope, the declared file
// and each checkout's git history, printed as the Atlas JSON or a line per
// project. It asks no host and reads no ledger.
package atlas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/atlas"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/ui"
)

func Command() *cli.Command {
	return &cli.Command{
		Name: "atlas",
		Usage: "your public projects with what ~/.f10/atlas.md declares and what their git history says " +
			"(this project, or several with -C, --group, --all)",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:  "group",
				Usage: "read the checkouts of `NAME`, a group in ~/.f10/board.md, instead of this one; repeatable",
			},
			&cli.BoolFlag{Name: "all", Usage: "read every checkout under F10_ROOTS"},
			ui.JSONFlag("emit the Atlas JSON (the schema is docs/atlas.schema.json)"),
			&cli.StringFlag{Name: "out", Usage: "write --json to this `PATH` instead of stdout"},
		},
		Action: run,
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	asJSON := cmd.Bool("json")
	if cmd.String("out") != "" && !asJSON {
		return errors.New("--out writes --json: add it")
	}

	var decl atlas.Declared

	path, err := scope.ReadUserConfig(atlas.DeclaredFile, &decl)
	if err != nil {
		return err
	}

	checkouts, err := scoped(ctx, cmd)
	if err != nil {
		return err
	}

	var roots []string
	if len(projects.Roots()) > 0 {
		if roots, err = projects.All(); err != nil {
			return err
		}
	}

	entries, err := atlas.Resolve(ctx, decl, checkouts, roots)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	doc := atlas.Build(ctx, entries, checkouts, time.Now())

	if !asJSON {
		render(cmd.Writer, doc, path)

		return nil
	}

	var buf bytes.Buffer
	if err := ui.JSON(&buf, doc); err != nil {
		return err
	}

	if out := cmd.String("out"); out != "" {
		if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil { //nolint:gosec // a document meant to be published
			return fmt.Errorf("writing the atlas: %w", err)
		}

		return nil
	}

	_, err = cmd.Writer.Write(buf.Bytes())

	return err
}

// scoped is the board's scope with only its checkouts kept: a repository
// with no checkout has no history to read.
func scoped(ctx context.Context, cmd *cli.Command) ([]atlas.Checkout, error) {
	items, err := scope.ItemsOrHere(cmd, scope.BoardConfigFile)
	if err != nil {
		return nil, err
	}

	var out []atlas.Checkout

	for _, e := range scope.Checkouts(ctx, items) {
		switch {
		case e.Repo != nil:
			continue
		case !projects.IsCheckout(e.Main):
			return nil, fmt.Errorf("%s is not a git checkout", e.Dir)
		}

		out = append(out, atlas.Checkout{Main: e.Main, Name: filepath.Base(e.Main), ID: e.ID, Local: e.Local})
	}

	if len(out) == 0 {
		return nil, errors.New("the scope names no checkout: the groups and roots given matched none")
	}

	return out, nil
}

// render is a line per node: name, status, language, last commit, tagline,
// with a failed read's error where the tagline goes.
func render(w io.Writer, doc atlas.Document, declared string) {
	if len(doc.Nodes) == 0 {
		fmt.Fprintf(w, "no public project in scope: give its entry in %s public: true\n", declared)

		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	for _, n := range doc.Nodes {
		last := "-"
		if len(n.LastCommit) >= len("2006-01-02") {
			last = n.LastCommit[:len("2006-01-02")]
		}

		tail := n.Tagline
		if n.Error != "" {
			tail = "error: " + n.Error
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", n.Name, dash(n.Status), dash(n.Language), last, tail)
	}

	_ = tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}
