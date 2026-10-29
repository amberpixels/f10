package main

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
)

// A scope is the projects a command answers for at once: every -C in order,
// then the --group groups, then with --all every checkout under F10_ROOTS;
// the current checkout with none of them.

// scopeParallel: the host CLI's calls are most of a scoped command's time.
const scopeParallel = 4

type scopeItem struct {
	dir  string
	repo *repoRef
}

// A repoRef is a repository named outright, as `host/owner/repo`.
type repoRef struct {
	CLI    string // gh or glab
	Host   string // github.com
	Path   string // owner/repo, or group/sub/project
	Prefix string // the task id prefix its ids are read with
}

func (r repoRef) id() string { return r.Host + "/" + r.Path }

func (r repoRef) name() string { return path.Base(r.Path) }

// repo is what forge.Host.Repo takes: the host in front only off the CLI's
// default host.
func (r repoRef) repo() string {
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

// parseRepo reads `[gh:|glab:]host/owner/repo`; a host other than github.com
// or gitlab.com must say its CLI with the prefix.
func parseRepo(entry, prefix string) (repoRef, error) {
	entry = strings.TrimSpace(entry)

	var r repoRef

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

	r.Prefix = cmp.Or(strings.ToUpper(strings.TrimSpace(prefix)), facts.ProjectPrefix(r.name()))

	return r, nil
}

// scopeItems is what the flags name, in scope order, before anything is bound.
func scopeItems(cmd *cli.Command, groupsFile string, repos []repoEntry, reposPath string) ([]scopeItem, error) {
	sels, groups := checkoutFlags(cmd), cmd.StringSlice("group")

	var items []scopeItem

	for _, sel := range sels {
		dir, err := checkoutDir(sel)
		if err != nil {
			return nil, err
		}

		items = append(items, scopeItem{dir: dir})
	}

	if len(groups) > 0 {
		found, err := readGroups(groupsFile, groups)
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
			items = append(items, scopeItem{dir: dir})
		}

		for _, r := range repos {
			ref, err := parseRepo(r.Repo, r.Prefix)
			if err != nil {
				return nil, fmt.Errorf("%s: repos: %w", reposPath, err)
			}

			items = append(items, scopeItem{repo: &ref})
		}
	}

	return items, nil
}

// scoped reports whether the flags name a scope at all.
func scoped(cmd *cli.Command) bool {
	return len(checkoutFlags(cmd))+len(cmd.StringSlice("group")) > 0 || cmd.Bool("all")
}

// A scopeEntry is one bound project, or the error binding it, for the command
// to decide on.
type scopeEntry struct {
	dir    string
	main   string // the main checkout, for a board of a checkout that would not bind
	id     string // its project id, the normalised remote
	target *target
	repo   *repoRef
	err    error
}

// bindScope reads one repository once across its main checkout and worktrees,
// and drops a repository named outright that a checkout covers, since the
// checkout knows more; checkouts bind first so every id is known by then.
func bindScope(ctx context.Context, items []scopeItem, cmdName string) []scopeEntry {
	entries := make([]scopeEntry, len(items))
	keep := make([]bool, len(items))

	roots, ids := map[string]bool{}, map[string]bool{}

	for i, it := range items {
		if it.repo != nil {
			continue
		}

		lay := layout.Locate(ctx, it.dir)

		main := cmp.Or(lay.MainRoot, lay.CheckoutRoot)
		if roots[main] {
			continue
		}

		roots[main] = true

		e := scopeEntry{dir: it.dir, main: main, id: ledger.Where(ctx, main).Project}
		ids[e.id] = true

		if e.target, e.err = targetAt(ctx, it.dir, true, cmdName); e.err != nil {
			e.err = fmt.Errorf("%s: %w", it.dir, e.err)
		}

		entries[i], keep[i] = e, true
	}

	for i, it := range items {
		if it.repo == nil || ids[it.repo.id()] {
			continue
		}

		ids[it.repo.id()] = true
		entries[i], keep[i] = scopeEntry{repo: it.repo}, true
	}

	var out []scopeEntry

	for i, e := range entries {
		if keep[i] {
			out = append(out, e)
		}
	}

	return out
}

// standupTargets fails on a checkout that fails, named: dropping it would
// leave its work out of a report meant to be whole.
func standupTargets(ctx context.Context, cmd *cli.Command) ([]*target, error) {
	if !scoped(cmd) {
		t, err := targetFor(ctx, cmd)
		if err != nil {
			return nil, err
		}

		return []*target{t}, nil
	}

	items, err := scopeItems(cmd, standupConfigFile, nil, "")
	if err != nil {
		return nil, err
	}

	var targets []*target

	for _, e := range bindScope(ctx, items, cmd.Name) {
		switch {
		case e.repo != nil:
			return nil, fmt.Errorf("%s: a repository with no checkout has no git history to read; f10 board reads it",
				e.repo.id())
		case e.err != nil:
			return nil, e.err
		}

		targets = append(targets, e.target)
	}

	if len(targets) == 0 {
		return nil, errors.New("the scope names no checkout: the groups and roots given matched none")
	}

	return targets, nil
}
