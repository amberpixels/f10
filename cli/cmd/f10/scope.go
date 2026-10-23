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

// A scope is the set of projects a command answers for at once: standup
// and board read several, through the same flags. Every -C in order, then
// the --group groups in the order given, then with --all every checkout
// under F10_ROOTS; the current checkout with none of them.

// scopeParallel is how many projects a scoped command collects at once;
// the host CLI's calls are most of the time.
const scopeParallel = 4

// A scopeItem is one thing the scope names: a checkout directory, or a
// repository with no checkout on this machine.
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

// id is the project id a checkout of the repository would get.
func (r repoRef) id() string { return r.Host + "/" + r.Path }

func (r repoRef) name() string { return path.Base(r.Path) }

// repo is what forge.Host.Repo takes: the path alone on the CLI's default
// host, the host in front elsewhere.
func (r repoRef) repo() string {
	if r.Host == "github.com" || r.Host == "gitlab.com" {
		return r.Path
	}

	return r.Host + "/" + r.Path
}

// isRepoEntry tells a repository from a path in a group's list: no
// leading / or ~, no glob character, a host with a dot in front.
func isRepoEntry(entry string) bool {
	entry = strings.TrimPrefix(strings.TrimPrefix(entry, "gh:"), "glab:")
	host, _, ok := strings.Cut(entry, "/")

	return ok && !strings.HasPrefix(entry, "/") && !strings.HasPrefix(entry, "~") &&
		!strings.HasPrefix(entry, ".") && !strings.ContainsAny(entry, "*?[") && strings.Contains(host, ".")
}

// parseRepo reads `[gh:|glab:]host/owner/repo`. github.com is gh and
// gitlab.com glab; any other host says which with the prefix. The task id
// prefix is the one given, else what a checkout of that name would derive.
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

// scopeItems is what the flags name, in scope order, before anything is
// bound: -C values resolved to directories, groups expanded from the
// user file given, --all's checkouts, and with --all the board's watched
// repositories. Nothing named at all is the current checkout.
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

// A scopeEntry is one project of the scope, bound: a target for a
// checkout, a repoRef for a repository with no checkout, or the error
// binding it, which the command decides what to do with.
type scopeEntry struct {
	dir    string
	main   string // the main checkout, for a board of a checkout that would not bind
	id     string // its project id, the normalised remote
	target *target
	repo   *repoRef
	err    error
}

// bindScope binds every item to its project, in the order given.
// Checkouts of one repository (a main checkout and its worktrees) see the
// same facts, so the repository is read once, from the first of them
// given. A repository named outright that a checkout anywhere in the
// scope covers is dropped too, since the checkout knows more: the
// checkouts are bound first, so every project id is known before a
// repository is kept.
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

// standupTargets is the checkouts a standup reads. A checkout that fails
// fails the run, named: dropping it would leave its work out of a report
// meant to be whole. A repository with no checkout has no history to read.
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
