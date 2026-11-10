package config

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// init registers a project by writing its project.md: the file's existence is
// the membership rule, since no filesystem heuristic tells a vendored fork
// from a checkout someone works in.

func InitCommand() *cli.Command {
	return &cli.Command{
		Name:   "init",
		Usage:  "register this checkout: write .f10/instructions/project.md from what detection found",
		Action: runInit,
	}
}

func runInit(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	lay := t.Layout

	path, err := destination(lay)
	if err != nil {
		return err
	}

	draft, err := facts.Scaffold(lay, t.Probes, t.Effective)
	if err != nil {
		return err
	}

	// the exclude entry first, so `.f10/` is already invisible to git by
	// the time anything appears inside it
	var excluded string

	if t.Effective.Storage == "in-repo" && t.Effective.Visibility == "stealth" {
		if excluded, err = archive.ExcludeF10(ctx, lay.CheckoutRoot); err != nil {
			return fmt.Errorf("excluding %s: %w", archive.ExcludeLine, err)
		}
	}

	if err := writeNew(path, draft.Markdown); err != nil {
		return err
	}

	report(cmd.Writer, path, excluded, draft)

	return nil
}

// destination is where project.md lands, or why it cannot: init creates, and
// only for a checkout that is a project of its own.
func destination(lay *layout.Layout) (string, error) {
	if lay.MainRoot == "" {
		return "", fmt.Errorf("%s is not a git checkout - f10 init registers one", lay.CheckoutRoot)
	}

	if lay.CheckoutRoot != lay.MainRoot {
		return "", fmt.Errorf("%s is a linked worktree, which layers over main rather than being a project "+
			"of its own - run `f10 -C %s init`", lay.CheckoutRoot, lay.MainRoot)
	}

	path := filepath.Join(lay.StorageRoot.Instructions(), "project.md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists - f10 init creates, never overwrites", path)
	}

	return path, nil
}

// writeNew creates the file and refuses to replace one: O_EXCL answers the
// race the stat above cannot, since creating is the whole contract.
func writeNew(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return f.Close()
}

// report's last row is the one fact no probe reaches: a human still owes the
// project its one-liner.
func report(w io.Writer, path, excluded string, d *facts.Draft) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)
	limit := ui.Width(w)

	scope.LinkedHeaderRow(w, "wrote", path, wroteLink(w, path), limit, plain)
	scope.HeaderRow(w, "sections", strings.Join(d.Sections, ", "), limit, plain)

	if excluded != "" {
		scope.HeaderRow(w, "excluded", excluded, limit, plain)
	}

	if len(d.Omitted) > 0 {
		scope.HeaderRow(w, "left out", strings.Join(d.Omitted, ", ")+" - undeclared, so the contract's defaults hold",
			limit, faint)
	}

	scope.HeaderRow(w, "next", "write the Project line: what this project is, in a sentence", limit, plain)
}

// wroteLink is the file:// url of the written project.md, on a terminal only.
func wroteLink(w io.Writer, path string) string {
	if !render.LinkTerminal(w) {
		return ""
	}

	return render.FileURL(path)
}
