// Package target is the project a command answers for: the checkout, its
// layered instructions, the probes and the effective facts, resolved once.
package target

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// A Target is the project a command answers for; -C only changes which
// checkout it describes, which is the whole of cross-project support.
type Target struct {
	Dir       string
	Layout    *layout.Layout
	Probes    *probe.Probes
	Effective *facts.Effective
	scoped    bool // -C named another project, which collapses the ref cascade

	project     string // the ledger's project id, read once by RefProject
	projectOnce sync.Once
}

// Reference looks up a task for this target. Under -C the cascade collapses:
// the branch and the session task answer for *this* checkout, so pointing at
// another project means saying which task.
func (t *Target) Reference(ctx context.Context, explicit string) (ref.Ref, error) {
	if t.scoped && strings.TrimSpace(explicit) == "" {
		return ref.Ref{}, fmt.Errorf("-C %s needs an explicit task id: the branch and the session task "+
			"answer for the checkout you are standing in", filepath.Base(t.Dir))
	}

	return ref.Lookup{Prefix: t.Effective.IDPrefix, Dir: t.Dir}.Find(ctx, explicit)
}

// IDOf turns a task as typed (`117`, a url) into its id, a token that names
// no task kept as typed. Lookups may start git and every finish row asks, so
// each token is looked up once, safely across concurrent readers.
func (t *Target) IDOf(ctx context.Context) func(string) string {
	var (
		mu  sync.Mutex
		ids = map[string]string{}
	)

	return func(token string) string {
		mu.Lock()
		defer mu.Unlock()

		if id, ok := ids[token]; ok {
			return id
		}

		id := token
		bare, _ := ref.SplitSuffix(token) // a suffixed worktree finishes its task
		if r, err := t.Reference(ctx, bare); err == nil {
			id = r.ID
		}

		ids[token] = id

		return id
	}
}

// DriverPaths, in lookup order: this checkout's own, then beside each
// instruction layer, last first, so a worktree finds main's driver where it
// finds main's instructions and one replacing them keeps main's driver out.
func (t *Target) DriverPaths() []string {
	own := storage.InRepo(t.Layout.CheckoutRoot)
	if t.OutOfTree() {
		own = t.Layout.StorageRoot
	}

	paths := []string{own.Driver()}

	for _, layer := range slices.Backward(t.Layout.Layers) {
		if p := filepath.Join(filepath.Dir(layer.Dir), "driver"); !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}

	return paths
}

func (t *Target) Driver() *driver.Driver {
	for _, p := range t.DriverPaths() {
		if d := driver.Find(filepath.Dir(p), t.Dir); d != nil {
			if t.Effective != nil {
				d.Timeout = t.Effective.DriverTimeout
			}

			return d
		}
	}

	return nil
}

func (t *Target) HostIssues() bool {
	kind := t.Effective.TrackerKind

	return kind == "" || kind.HostIssues() || !t.TrackerDeclared()
}

// HostCloses reports that the host's issues are the tracker, so a merge's
// closing keyword closes the task; a driver's tracker is never closed that way.
func (t *Target) HostCloses() bool { return t.HostIssues() && t.Driver() == nil }

func (t *Target) TrackerDeclared() bool {
	for _, f := range t.Effective.Facts {
		if f.Key == "tracker.kind" {
			return f.Origin.Declared()
		}
	}

	return false
}

// OutOfTree says the storage root is ~/.f10/<project>, outside every checkout.
func (t *Target) OutOfTree() bool {
	return len(t.Layout.Layers) == 1 && t.Layout.Layers[0].Label == "out-of-tree"
}

// RefProject is the ledger's project id for this target, read once: status
// and drive write refs for every task they show.
func (t *Target) RefProject(ctx context.Context) string {
	t.projectOnce.Do(func() { t.project = ProjectOf(ctx, t.Dir) })

	return t.project
}

func For(ctx context.Context, cmd *cli.Command) (*Target, error) {
	dir, scoped, err := RootDir(cmd)
	if err != nil {
		return nil, err
	}

	return At(ctx, dir, scoped, cmd.Name)
}

func At(ctx context.Context, dir string, scoped bool, cmdName string) (*Target, error) {
	lay := layout.Locate(ctx, dir)
	pb := probe.Run(ctx, lay)
	// no home to read is no user layer, not a failed command
	userDir, _ := storage.UserFilesDir()
	eff := facts.Build(lay, pb, userDir)

	// a fact declared wrong stops every command that acts on facts; config,
	// init and doctor are how it is seen and fixed, so they run anyway
	if errs := eff.ErrorProblems(); len(errs) > 0 && cmdName != "config" && cmdName != "init" && cmdName != "doctor" {
		return nil, errors.New(LabeledErrors(errs) + "\n(f10 config shows every fact and where it came from)")
	}

	return &Target{Dir: dir, Layout: lay, Probes: pb, Effective: eff, scoped: scoped}, nil
}

func CheckoutFlags(cmd *cli.Command) []string {
	var out []string

	for _, v := range cmd.Root().StringSlice("C") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}

	return out
}

func RootDir(cmd *cli.Command) (string, bool, error) {
	sels := CheckoutFlags(cmd)

	switch len(sels) {
	case 0:
		cwd, err := os.Getwd()
		if err != nil {
			return "", false, fmt.Errorf("getwd: %w", err)
		}

		return cwd, false, nil
	case 1:
		dir, err := CheckoutDir(sels[0])

		return dir, true, err
	default:
		return "", false, fmt.Errorf("-C names one checkout (got %d); only standup and board take several", len(sels))
	}
}

// CheckoutDir sends a bare name to projects, which is off until the user
// turns it on; a path needs no lookup.
func CheckoutDir(sel string) (string, error) {
	if projects.IsName(sel) {
		return projects.Find(sel)
	}

	sel, err := ExpandHome(sel)
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

func ExpandHome(path string) (string, error) {
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

// LabeledErrors marks each of the project's problems with project.md; a
// problem in the user's ~/.f10 files names its own file already.
func LabeledErrors(problems []facts.Problem) string {
	msgs := make([]string, 0, len(problems))

	for _, p := range problems {
		if p.File == "" {
			msgs = append(msgs, "project.md: "+p.Message)
		} else {
			msgs = append(msgs, p.Message)
		}
	}

	return strings.Join(msgs, "\n")
}

// ProjectOf is the ledger's project id for dir. A var, so tests that fake
// git leave the task refs alone.
var ProjectOf = func(ctx context.Context, dir string) string { return ledger.Where(ctx, dir).Project }

// LoreRoot is where the project's lore lives: the checkout's committed
// .lore, else the storage root's, which every checkout shares.
func (t *Target) LoreRoot() string {
	if t.Effective.Lore == "committed" {
		return filepath.Join(t.Layout.CheckoutRoot, ".lore")
	}

	return t.Layout.StorageRoot.Lore()
}
