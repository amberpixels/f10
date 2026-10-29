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
	"sync"

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

// A target is the project a command answers for; -C only changes which
// checkout it describes, which is the whole of cross-project support.
type target struct {
	dir    string
	lay    *layout.Layout
	pb     *probe.Probes
	eff    *facts.Effective
	scoped bool // -C named another project, which collapses the ref cascade

	project     string // the ledger's project id, read once by refProject
	projectOnce sync.Once
}

func targetFor(ctx context.Context, cmd *cli.Command) (*target, error) {
	dir, scoped, err := rootDir(cmd)
	if err != nil {
		return nil, err
	}

	return targetAt(ctx, dir, scoped, cmd.Name)
}

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

func checkoutFlags(cmd *cli.Command) []string {
	var out []string

	for _, v := range cmd.Root().StringSlice("C") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}

	return out
}

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

// checkoutDir sends a bare name to projects, which is off until the user
// turns it on; a path needs no lookup.
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

// reference looks up a task for this target. Under -C the cascade collapses:
// the branch and the session task answer for *this* checkout, so pointing at
// another project means saying which task.
func (t *target) reference(ctx context.Context, explicit string) (ref.Ref, error) {
	if t.scoped && strings.TrimSpace(explicit) == "" {
		return ref.Ref{}, fmt.Errorf("-C %s needs an explicit task id: the branch and the session task "+
			"answer for the checkout you are standing in", filepath.Base(t.dir))
	}

	return ref.Lookup{Prefix: t.eff.IDPrefix, Dir: t.dir}.Find(ctx, explicit)
}

// host is the CLI the probe settled on; a wrong guess opens the wrong site,
// so "unknown" refuses rather than picking.
func (t *target) host() (host, error) {
	switch t.pb.CLI.Value {
	case "gh", "glab":
		h := newHost(t.pb.CLI.Value, t.dir)
		if t.eff != nil {
			h.titles, h.prefix = t.eff.TitleTemplate, t.eff.IDPrefix
		}

		return h, nil
	default:
		return host{}, errors.New("no host CLI for this checkout: " + t.pb.CLI.Evidence)
	}
}

// driverPaths, in lookup order: this checkout's own, then beside each
// instruction layer, last first, so a worktree finds main's driver where it
// finds main's instructions and one replacing them keeps main's driver out.
func (t *target) driverPaths() []string {
	own := filepath.Join(t.lay.CheckoutRoot, ".f10")
	if t.outOfTree() {
		own = t.lay.StorageRoot
	}

	paths := []string{filepath.Join(own, "driver")}

	for _, layer := range slices.Backward(t.lay.Layers) {
		if p := filepath.Join(filepath.Dir(layer.Dir), "driver"); !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}

	return paths
}

func (t *target) driver() *driver.Driver {
	for _, p := range t.driverPaths() {
		if d := driver.Find(filepath.Dir(p), t.dir); d != nil {
			if t.eff != nil {
				d.Timeout = t.eff.DriverTimeout
			}

			return d
		}
	}

	return nil
}

// verbNeeds names what needs each verb, for the refusal when a driver
// declines it.
var verbNeeds = map[string]string{
	driver.VerbRead:   "`f10 task read`, `f10 start` and `f10 drive` need",
	driver.VerbBranch: "`f10 start` and `f10 drive` need for a branch name past the bare id",
	driver.VerbURL:    "`f10 task open <id>` needs",
	driver.VerbIndex:  "`f10 task open` with no id needs",
	driver.VerbList:   "`f10 task open --print` and the board's next rows need",
	driver.VerbSearch: "`f10 task search` needs",
}

// issues is the host-issues fallback when no driver answers; declined is
// the verb that exited 3. A tracker no host CLI reaches is refused, since
// an unrelated issue or PR on the host shares the number.
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

func (t *target) sharedPlansDir() string {
	return filepath.Join(t.sharedRoot(), "plans")
}

// sharedRoot is the storage root every checkout shares.
func (t *target) sharedRoot() string {
	if t.lay.LegacyStorage && t.lay.MainRoot != "" {
		return filepath.Join(t.lay.MainRoot, ".f10")
	}

	return t.lay.StorageRoot
}

// outOfTree says the storage root is ~/.f10/<project>, outside every checkout.
func (t *target) outOfTree() bool {
	return len(t.lay.Layers) == 1 && t.lay.Layers[0].Label == "out-of-tree"
}

// follow acts on a url: drivers return addresses, f10 is what acts on them.
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

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)

	return ok && term.IsTerminal(int(f.Fd()))
}

// writeDoc pages markdown on a terminal and writes it raw to a pipe: no
// renderer is bundled, since glow, bat or an editor compose for free.
func writeDoc(ctx context.Context, w io.Writer, dir, doc string) error {
	doc = strings.TrimRight(doc, "\n") + "\n"

	pager := strings.Fields(os.Getenv("PAGER"))
	if !isTerminal(w) || len(pager) == 0 {
		_, err := io.WriteString(w, doc)

		return err
	}

	return shell.Pipe(ctx, dir, doc, pager[0], pager[1:]...)
}

// linkFlag is the same flag on every verb that follows an address, so `open`
// keeps one meaning across nouns whose addresses are paths and urls alike.
func linkFlag() cli.Flag {
	return &cli.BoolFlag{Name: "link", Usage: "print the address instead of opening it"}
}

func printFlag() cli.Flag {
	return &cli.BoolFlag{Name: "print", Usage: "write the item, or with none the list's first page, to stdout"}
}
