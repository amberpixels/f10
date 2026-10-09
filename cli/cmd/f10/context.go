package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// A target is the project a command answers for: the layout, the
// probes, and the effective facts. Every noun needs the same three, and -C
// is the only thing that changes which checkout they describe - Locate
// already takes a directory, so pointing it elsewhere is the whole
// of cross-project support.
type target struct {
	dir    string
	lay    *layout.Layout
	pb     *probe.Probes
	eff    *facts.Effective
	scoped bool // -C named another project, which collapses the ref cascade
}

func targetFor(ctx context.Context, cmd *cli.Command) (*target, error) {
	dir, scoped, err := rootDir(cmd)
	if err != nil {
		return nil, err
	}

	lay := layout.Locate(ctx, dir)
	pb := probe.Run(ctx, lay)

	return &target{dir: dir, lay: lay, pb: pb, eff: facts.Build(lay, pb), scoped: scoped}, nil
}

// rootDir turns -C into a directory. A path needs no lookup and always
// works; a bare name goes to projects, which is off until the user turns it
// on.
func rootDir(cmd *cli.Command) (string, bool, error) {
	sel := strings.TrimSpace(cmd.Root().String("C"))
	if sel == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", false, fmt.Errorf("getwd: %w", err)
		}

		return cwd, false, nil
	}

	if projects.IsName(sel) {
		dir, err := projects.Find(sel)

		return dir, true, err
	}

	if rest, ok := strings.CutPrefix(sel, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, fmt.Errorf("expanding ~ in %q: %w", sel, err)
		}

		sel = filepath.Join(home, rest)
	}

	dir, err := filepath.Abs(sel)
	if err != nil {
		return "", false, fmt.Errorf("finding %q: %w", sel, err)
	}

	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", false, fmt.Errorf("no such directory: %s", dir)
	}

	return dir, true, nil
}

// reference looks up a task reference for this target. Under -C the cascade
// collapses instead of stretching: the branch and the session task are
// answers about *this* checkout and this session, so pointing at another
// project means saying which task.
func (t *target) reference(ctx context.Context, explicit string) (ref.Ref, error) {
	if t.scoped && strings.TrimSpace(explicit) == "" {
		return ref.Ref{}, fmt.Errorf("-C %s needs an explicit task id: the branch and the session task "+
			"answer for the checkout you are standing in", filepath.Base(t.dir))
	}

	return ref.Lookup{Prefix: t.eff.IDPrefix, Dir: t.dir}.Find(ctx, explicit)
}

// host is the tracker/host CLI probe routing settled on, bound to this
// checkout, or an error naming the evidence. A wrong guess opens the wrong
// site, so "unknown" refuses rather than picking.
func (t *target) host() (host, error) {
	switch t.pb.CLI.Value {
	case "gh", "glab":
		return host{name: t.pb.CLI.Value, dir: t.dir}, nil
	default:
		return host{}, errors.New("no host CLI for this checkout: " + t.pb.CLI.Evidence)
	}
}

// sharedPlansDir is the plans dir every worktree shares with out-of-tree
// storage, or "" with in-repo storage, where each worktree keeps its own
// plans under its .f10/plans.
func (t *target) sharedPlansDir() string {
	if t.lay.StorageRoot == filepath.Join(t.lay.CheckoutRoot, ".f10") {
		return ""
	}

	return t.pb.PlansDir
}

// fact is the effective value of one project.md field, or "".
func (t *target) fact(name string) string {
	for _, f := range t.eff.Fields {
		if f.Name == name {
			return f.Value
		}
	}

	return ""
}

// follow performs the side effect a url deserves, or prints it when the
// caller asked for the address instead. Drivers return addresses; f10 is
// what acts on them.
func follow(ctx context.Context, w io.Writer, dir, url string, addressOnly bool) error {
	if addressOnly {
		_, err := fmt.Fprintln(w, url)

		return err
	}

	opener := shell.Opener()
	if opener == "" {
		return errors.New("no OS opener (open/xdg-open) on PATH - use --print and open it yourself")
	}

	return shell.Passthrough(ctx, dir, opener, url)
}

// isTerminal reports whether w is a terminal, which is the one question
// that decides how a document is presented.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)

	return ok && term.IsTerminal(int(f.Fd()))
}

// writeDoc emits a markdown document the way its reader can use it: a
// terminal gets $PAGER when one is set, and everything else (a pipe, a
// redirect, an agent's Bash tool call) gets the markdown raw. No renderer is
// bundled: glow, bat or an editor compose for free, since a pipe is not a
// terminal and so receives exactly the markdown those tools want.
func writeDoc(ctx context.Context, w io.Writer, dir, doc string) error {
	doc = strings.TrimRight(doc, "\n") + "\n"

	pager := strings.Fields(os.Getenv("PAGER"))
	if !isTerminal(w) || len(pager) == 0 {
		_, err := io.WriteString(w, doc)

		return err
	}

	return shell.Pipe(ctx, dir, doc, pager[0], pager[1:]...)
}

// printFlag is the same flag on every verb that follows an address: print
// it instead. It exists so `open` keeps one meaning across the nouns - a
// plan's address is a path and a task's is a url, and no verb name covers
// both honestly.
func printFlag() cli.Flag {
	return &cli.BoolFlag{Name: "print", Usage: "print the address instead of opening it"}
}
