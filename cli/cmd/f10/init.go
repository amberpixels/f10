package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/layout"
)

// init writes only the two files that register a project: the project.md
// detection can fill, and the exclude entry that keeps it out of sight. The
// file's existence is the membership rule - no filesystem heuristic tells a
// vendored fork from a checkout someone works in, so the act is explicit.

// excludeLine is the entry stealth needs, written exactly as a repo that
// did it by hand writes it.
const excludeLine = ".f10/"

func initCommand() *cli.Command {
	return &cli.Command{
		Name:  "init",
		Usage: "register this checkout: write .f10/instructions/project.md from what detection found",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "migrate",
				Usage: "move the facts an existing project.md declares in prose into its frontmatter; deletes nothing",
			},
		},
		Action: runInit,
	}
}

func runInit(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	lay := t.lay

	if cmd.Bool("migrate") {
		return runMigrate(cmd.Writer, filepath.Join(lay.StorageRoot, "instructions", "project.md"))
	}

	path, err := destination(lay)
	if err != nil {
		return err
	}

	draft, err := facts.Scaffold(lay, t.pb, t.eff)
	if err != nil {
		return err
	}

	// the exclude entry first, so `.f10/` is already invisible to git by
	// the time anything appears inside it
	var excluded string

	if t.eff.Storage == "in-repo" && t.eff.Visibility == "stealth" {
		if excluded, err = excludeF10(ctx, lay.CheckoutRoot); err != nil {
			return fmt.Errorf("excluding %s: %w", excludeLine, err)
		}
	}

	if err := writeNew(path, draft.Markdown); err != nil {
		return err
	}

	report(cmd.Writer, path, excluded, draft)

	return nil
}

// destination is where a project.md would land, or the reason none can:
// this command creates, and only for a checkout that is a project of its
// own.
func destination(lay *layout.Layout) (string, error) {
	if lay.MainRoot == "" {
		return "", fmt.Errorf("%s is not a git checkout - f10 init registers one", lay.CheckoutRoot)
	}

	if lay.CheckoutRoot != lay.MainRoot {
		return "", fmt.Errorf("%s is a linked worktree, which layers over main rather than being a project "+
			"of its own - run `f10 -C %s init`", lay.CheckoutRoot, lay.MainRoot)
	}

	path := filepath.Join(lay.StorageRoot, "instructions", "project.md")
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

// excludeF10 appends the stealth entry to the repo's info/exclude, and says
// what it found: the ignore entry itself is never committed, which is why
// this file and not .gitignore.
func excludeF10(ctx context.Context, root string) (string, error) {
	named := gitx.Out(ctx, root, "rev-parse", "--git-path", "info/exclude")
	if named == "" {
		return "", errors.New("git could not name info/exclude")
	}

	path := named
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, named)
	}

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == excludeLine {
			return "already in " + path, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	entry := excludeLine + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		entry = "\n" + entry
	}

	if _, err := f.WriteString(entry); err != nil {
		return "", fmt.Errorf("appending to %s: %w", path, err)
	}

	return excludeLine + " in " + path, f.Close()
}

// report prints what now exists, in the label block config and status use.
// The last row is the one fact no probe reaches: a human still owes the
// project its one-liner.
func report(w io.Writer, path, excluded string, d *facts.Draft) {
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)
	limit := termWidth(w)

	headerRow(w, "wrote", path, limit, plain)
	headerRow(w, "sections", strings.Join(d.Sections, ", "), limit, plain)

	if excluded != "" {
		headerRow(w, "excluded", excluded, limit, plain)
	}

	if len(d.Omitted) > 0 {
		headerRow(w, "left out", strings.Join(d.Omitted, ", ")+" - undeclared, so the contract's defaults hold",
			limit, faint)
	}

	headerRow(w, "next", "write the Project line: what this project is, in a sentence", limit, plain)
}

// runMigrate puts a frontmatter block on top of a prose-only project.md and
// says which prose it supersedes. The prose stays: it is the user's text,
// and deleting it is theirs to do once the block reads right.
func runMigrate(w io.Writer, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: nothing to migrate - f10 init writes a new one", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	m, err := facts.Migrate(data, path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	limit := termWidth(w)

	if len(m.Moved) == 0 {
		headerRow(w, "unchanged", path+" declares no fact in prose", limit, lipgloss.NewStyle())

		return nil
	}

	if err := rewrite(path, m.Content, info.Mode().Perm()); err != nil {
		return err
	}

	headerRow(w, "wrote", path, limit, lipgloss.NewStyle())
	headerRow(w, "moved", strings.Join(m.Moved, ", "), limit, lipgloss.NewStyle())

	for i, where := range m.Superseded {
		label := ""
		if i == 0 {
			label = "delete"
		}

		headerRow(w, label, where, limit, lipgloss.NewStyle().Faint(true))
	}

	headerRow(w, "next", "delete that prose, then `f10 config` should show no warning", limit, lipgloss.NewStyle())

	return nil
}

// rewrite replaces an existing file's content, keeping its mode.
func rewrite(path, content string, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return f.Close()
}
