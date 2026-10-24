package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/amberpixels/f10/cli/internal/driver"
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

	return targetAt(ctx, dir, scoped, cmd.Name)
}

// targetAt is the target for one directory, for a command that answers for
// several.
func targetAt(ctx context.Context, dir string, scoped bool, cmdName string) (*target, error) {
	lay := layout.Locate(ctx, dir)
	pb := probe.Run(ctx, lay)
	eff := facts.Build(lay, pb)

	// a fact declared wrong stops every command that acts on facts; config,
	// init and doctor are how it is seen and fixed, so they run anyway
	if errs := eff.Errors(); errs != "" && cmdName != "config" && cmdName != "init" && cmdName != "doctor" {
		return nil, errors.New("project.md: " + errs + "\n(f10 config shows every fact and where it came from)")
	}

	return &target{dir: dir, lay: lay, pb: pb, eff: eff, scoped: scoped}, nil
}

// checkoutFlags is every -C given, trimmed, empty ones dropped.
func checkoutFlags(cmd *cli.Command) []string {
	var out []string

	for _, v := range cmd.Root().StringSlice("C") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}

	return out
}

// rootDir turns -C into a directory, the current one without it.
func rootDir(cmd *cli.Command) (string, bool, error) {
	sels := checkoutFlags(cmd)

	switch len(sels) {
	case 0:
		cwd, err := os.Getwd()
		if err != nil {
			return "", false, fmt.Errorf("getwd: %w", err)
		}

		return cwd, false, nil
	case 1:
		dir, err := checkoutDir(sels[0])

		return dir, true, err
	default:
		return "", false, fmt.Errorf("-C names one checkout (got %d); only standup and board take several", len(sels))
	}
}

// checkoutDir turns one -C value into a directory. A path needs no lookup
// and always works; a bare name goes to projects, which is off until the
// user turns it on.
func checkoutDir(sel string) (string, error) {
	if projects.IsName(sel) {
		return projects.Find(sel)
	}

	sel, err := expandHome(sel)
	if err != nil {
		return "", err
	}

	dir, err := filepath.Abs(sel)
	if err != nil {
		return "", fmt.Errorf("finding %q: %w", sel, err)
	}

	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no such directory: %s", dir)
	}

	return dir, nil
}

// expandHome expands a leading ~/ to the home directory.
func expandHome(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding ~ in %q: %w", path, err)
	}

	return filepath.Join(home, rest), nil
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
		return newHost(t.pb.CLI.Value, t.dir), nil
	default:
		return host{}, errors.New("no host CLI for this checkout: " + t.pb.CLI.Evidence)
	}
}

// driverPaths are where a driver may sit, in lookup order: this checkout's
// storage root, then beside each instruction layer, the last first. A
// worktree finds main's driver where it finds main's instructions, and one
// that replaces main's instructions keeps main's driver out too.
func (t *target) driverPaths() []string {
	paths := []string{filepath.Join(t.lay.StorageRoot, "driver")}

	for _, layer := range slices.Backward(t.lay.Layers) {
		if p := filepath.Join(filepath.Dir(layer.Dir), "driver"); !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}

	return paths
}

// driver is the project's tracker driver, or nil when it has none.
func (t *target) driver() *driver.Driver {
	for _, p := range t.driverPaths() {
		if d := driver.Find(filepath.Dir(p), t.dir); d != nil {
			return d
		}
	}

	return nil
}

// verbNeeds says what needs each driver verb that falls back to host
// issues, for the refusal when a non-host tracker's driver declines it.
var verbNeeds = map[string]string{
	driver.VerbRead:   "`f10 task read`, `f10 start` and `f10 drive` need",
	driver.VerbBranch: "`f10 start` and `f10 drive` need for a branch name past the bare id",
	driver.VerbURL:    "`f10 task open <id>` needs",
	driver.VerbIndex:  "`f10 task open` with no id needs",
	driver.VerbList:   "`f10 task open --print` and the board's next rows need",
	driver.VerbSearch: "`f10 task search` needs",
}

// issues is the host for the issue fallback, used when no driver answers;
// declined is the driver verb that exited 3, or "" when none was asked.
// A project declaring a tracker no host CLI reaches is refused rather than
// read from host issues, where an unrelated issue or PR shares the number.
func (t *target) issues(declined string) (host, error) {
	if t.hostIssues() {
		return t.host()
	}

	if d := t.driver(); d != nil && declined != "" {
		need := ""
		if n, ok := verbNeeds[declined]; ok {
			need = ", which " + n
		}

		return host{}, fmt.Errorf("project.md declares a %s tracker, which has no host-issues fallback, "+
			"and the driver at %s declines `%s`%s; add it per docs/driver-contract.md",
			t.eff.TrackerKind, d.Path, declined, need)
	}

	return host{}, fmt.Errorf("project.md declares a %s tracker, which f10 reads only through a driver, "+
		"and none was found (looked for %s); see docs/driver-contract.md",
		t.eff.TrackerKind, strings.Join(t.driverPaths(), ", "))
}

// hostIssues reports whether the project's tracker is its host's issues:
// nothing declared, or GitHub Issues or GitLab work items declared.
func (t *target) hostIssues() bool {
	kind := t.eff.TrackerKind

	return kind == "" || kind == "GitHub Issues" || kind == "GitLab work items" || !t.trackerDeclared()
}

func (t *target) trackerDeclared() bool {
	for _, f := range t.eff.Facts {
		if f.Key == "tracker.kind" {
			return strings.HasPrefix(f.Origin, "declared")
		}
	}

	return false
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

// follow performs the side effect a url deserves, or prints it when the
// caller asked for the address instead. Drivers return addresses; f10 is
// what acts on them.
func follow(ctx context.Context, w io.Writer, dir, url string, linkOnly bool) error {
	if linkOnly {
		_, err := fmt.Fprintln(w, url)

		return err
	}

	opener := shell.Opener()
	if opener == "" {
		return errors.New("no OS opener (open/xdg-open) on PATH - use --link and open it yourself")
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

// linkFlag is the same flag on every verb that follows an address: print
// it instead, for a pipe. It exists so `open` keeps one meaning across the
// nouns - a plan's address is a path and a task's is a url, and no verb name
// covers both honestly.
func linkFlag() cli.Flag {
	return &cli.BoolFlag{Name: "link", Usage: "print the address instead of opening it"}
}

// printFlag is `open` writing what the address shows into the terminal: the
// item, or the list's first page when no item is named.
func printFlag() cli.Flag {
	return &cli.BoolFlag{Name: "print", Usage: "write the item, or with none the list's first page, to stdout"}
}
