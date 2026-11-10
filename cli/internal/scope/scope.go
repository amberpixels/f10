// Package scope is which checkouts a command answers for: -C and the user's
// groups, repos and people, read from the user config files.
package scope

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/target"
)

// ScopeParallel: the host CLI's calls are most of a scoped command's time.
const ScopeParallel = 4

type scopeItem struct {
	Dir  string
	Repo *RepoRef
}

// A RepoRef is a repository named outright, as `host/owner/repo`.
type RepoRef struct {
	CLI    string // gh or glab
	Host   string // github.com
	Path   string // owner/repo, or group/sub/project
	Prefix string // the task id prefix its ids are read with
}

func (r RepoRef) ID() string { return r.Host + "/" + r.Path }

func (r RepoRef) Name() string { return path.Base(r.Path) }

// Repo is what forge.Host.Repo takes: the host in front only off the CLI's
// default host.
func (r RepoRef) Repo() string {
	if r.Host == "github.com" || r.Host == "gitlab.com" {
		return r.Path
	}

	return r.Host + "/" + r.Path
}

func isRepoEntry(entry string) bool {
	entry = strings.TrimPrefix(strings.TrimPrefix(entry, "gh:"), "glab:")
	host, _, ok := strings.Cut(entry, "/")

	return ok && !strings.HasPrefix(entry, "/") && !strings.HasPrefix(entry, "~") &&
		!strings.HasPrefix(entry, ".") && !strings.ContainsAny(entry, "*?[") && strings.Contains(host, ".")
}

// ParseRepo reads `[gh:|glab:]host/owner/repo`; a host other than github.com
// or gitlab.com must say its CLI with the prefix.
func ParseRepo(entry, prefix string) (RepoRef, error) {
	entry = strings.TrimSpace(entry)

	var r RepoRef

	switch {
	case strings.HasPrefix(entry, "gh:"):
		r.CLI, entry = "gh", strings.TrimPrefix(entry, "gh:")
	case strings.HasPrefix(entry, "glab:"):
		r.CLI, entry = "glab", strings.TrimPrefix(entry, "glab:")
	}

	entry = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(entry, "https://"), "http://"), "/")

	host, rest, ok := strings.Cut(entry, "/")
	if !ok || !strings.Contains(host, ".") || !strings.Contains(rest, "/") || strings.ContainsAny(entry, "*?[ ") {
		return r, fmt.Errorf("%q is not a repository: want host/owner/repo, such as github.com/acme/widgets", entry)
	}

	r.Host, r.Path = strings.ToLower(host), rest

	switch {
	case r.CLI != "":
	case r.Host == "github.com":
		r.CLI = "gh"
	case r.Host == "gitlab.com":
		r.CLI = "glab"
	default:
		return r, fmt.Errorf(
			"%q: %s is neither github.com nor gitlab.com; say which CLI reads it with a gh: or glab: prefix",
			entry,
			r.Host,
		)
	}

	if _, err := url.Parse("https://" + r.Host + "/" + r.Path); err != nil {
		return r, fmt.Errorf("%q: %w", entry, err)
	}

	r.Prefix = cmp.Or(strings.ToUpper(strings.TrimSpace(prefix)), facts.ProjectPrefix(r.Name()))

	return r, nil
}

// ScopeItems is what the flags name, in scope order, before anything is bound.
func ScopeItems(cmd *cli.Command, groupsFile string, repos []repoEntry, reposPath string) ([]scopeItem, error) {
	sels, groups := target.CheckoutFlags(cmd), cmd.StringSlice("group")

	var items []scopeItem

	for _, sel := range sels {
		dir, err := target.CheckoutDir(sel)
		if err != nil {
			return nil, err
		}

		items = append(items, scopeItem{Dir: dir})
	}

	if len(groups) > 0 {
		found, err := ReadGroups(groupsFile, groups)
		if err != nil {
			return nil, err
		}

		items = append(items, found...)
	}

	if cmd.Bool("all") {
		found, err := projects.All()
		if err != nil {
			return nil, err
		}

		for _, dir := range found {
			items = append(items, scopeItem{Dir: dir})
		}

		for _, r := range repos {
			ref, err := ParseRepo(r.Repo, r.Prefix)
			if err != nil {
				return nil, fmt.Errorf("%s: repos: %w", reposPath, err)
			}

			items = append(items, scopeItem{Repo: &ref})
		}
	}

	return items, nil
}

// Scoped reports whether the flags name a scope at all.
func Scoped(cmd *cli.Command) bool {
	return len(target.CheckoutFlags(cmd))+len(cmd.StringSlice("group")) > 0 || cmd.Bool("all")
}

// A ScopeEntry is one bound project, or the error binding it, for the command
// to decide on.
type ScopeEntry struct {
	Dir    string
	Main   string // the main checkout, for a board of a checkout that would not bind
	ID     string // its project id, the normalised remote
	Target *target.Target
	Repo   *RepoRef
	Err    error
}

// BindScope reads one repository once across its main checkout and worktrees,
// and drops a repository named outright that a checkout covers, since the
// checkout knows more; checkouts bind first so every id is known by then.
func BindScope(ctx context.Context, items []scopeItem, cmdName string) []ScopeEntry {
	entries := make([]ScopeEntry, len(items))
	keep := make([]bool, len(items))

	roots, ids := map[string]bool{}, map[string]bool{}

	for i, it := range items {
		if it.Repo != nil {
			continue
		}

		lay := layout.Locate(ctx, it.Dir)

		main := cmp.Or(lay.MainRoot, lay.CheckoutRoot)
		if roots[main] {
			continue
		}

		roots[main] = true

		e := ScopeEntry{Dir: it.Dir, Main: main, ID: ledger.Where(ctx, main).Project}
		ids[e.ID] = true

		if e.Target, e.Err = target.At(ctx, it.Dir, true, cmdName); e.Err != nil {
			e.Err = fmt.Errorf("%s: %w", it.Dir, e.Err)
		}

		entries[i], keep[i] = e, true
	}

	for i, it := range items {
		if it.Repo == nil || ids[it.Repo.ID()] {
			continue
		}

		ids[it.Repo.ID()] = true
		entries[i], keep[i] = ScopeEntry{Repo: it.Repo}, true
	}

	var out []ScopeEntry

	for i, e := range entries {
		if keep[i] {
			out = append(out, e)
		}
	}

	return out
}

// StandupTargets fails on a checkout that fails, named: dropping it would
// leave its work out of a report meant to be whole.
func StandupTargets(ctx context.Context, cmd *cli.Command) ([]*target.Target, error) {
	if !Scoped(cmd) {
		t, err := target.For(ctx, cmd)
		if err != nil {
			return nil, err
		}

		return []*target.Target{t}, nil
	}

	items, err := ScopeItems(cmd, StandupConfigFile, nil, "")
	if err != nil {
		return nil, err
	}

	var targets []*target.Target

	for _, e := range BindScope(ctx, items, cmd.Name) {
		switch {
		case e.Repo != nil:
			return nil, fmt.Errorf("%s: a repository with no checkout has no git history to read; f10 board reads it",
				e.Repo.ID())
		case e.Err != nil:
			return nil, e.Err
		}

		targets = append(targets, e.Target)
	}

	if len(targets) == 0 {
		return nil, errors.New("the scope names no checkout: the groups and roots given matched none")
	}

	return targets, nil
}
