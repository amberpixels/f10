package atlas

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/probe"
)

// Version is the spec version docs/atlas.md opens with.
const Version = 1

// the reads are local git calls, so more run at once than the host-bound
// board allows
const readParallel = 8

type Document struct {
	Version     int    `json:"version"`
	GeneratedAt string `json:"generated_at"`
	Nodes       []Node `json:"nodes"`
	Edges       []Edge `json:"edges"`
}

// A Node is a public node; every optional field is absent rather than empty.
type Node struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	Name            string  `json:"name"`
	Role            string  `json:"role,omitempty"`
	Status          string  `json:"status,omitempty"`
	Tagline         string  `json:"tagline,omitempty"`
	Language        string  `json:"language,omitempty"`
	FirstCommit     string  `json:"first_commit,omitempty"`
	LastCommit      string  `json:"last_commit,omitempty"`
	CommitsPerMonth []Month `json:"commits_per_month,omitempty"`
	LatestTag       *Tag    `json:"latest_tag,omitempty"`
	Error           string  `json:"error,omitempty"`
}

type Month struct {
	Month   string `json:"month"`
	Commits int    `json:"commits"`
}

type Tag struct {
	Name string `json:"name"`
	At   string `json:"at"`
}

// An Edge is specified but not yet derived, so the list is always empty.
type Edge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

// Build reads every public checkout and assembles the document. A private
// checkout is never read, so nothing of its history reaches the process.
//
//nolint:lostfield // an assembly: readNode carries each entry's fields, and aliases and links print later
func Build(ctx context.Context, entries map[string]Entry, scope []Checkout, now time.Time) Document {
	now = now.UTC().Truncate(time.Second)

	var public []Checkout

	for _, c := range scope {
		if entries[c.Main].Public {
			public = append(public, c)
		}
	}

	nodes := make([]Node, len(public))
	sem := make(chan struct{}, readParallel)

	var wg sync.WaitGroup

	for i, c := range public {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			nodes[i] = readNode(ctx, c, entries[c.Main], now)
		})
	}

	wg.Wait()

	slices.SortFunc(nodes, func(a, b Node) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })

	return Document{Version: Version, GeneratedAt: now.Format(time.RFC3339), Nodes: nodes, Edges: []Edge{}}
}

func readNode(ctx context.Context, c Checkout, e Entry, now time.Time) Node {
	n := Node{
		ID: c.NodeID(), Kind: "public", Name: c.Name,
		Role: e.Role, Status: e.Status, Tagline: e.Tagline,
		Language: language(c.Main),
	}

	if err := readGit(ctx, c.Main, now, &n); err != nil {
		// nothing in the atlas names a path, an error's text included
		n.Error = strings.ReplaceAll(err.Error(), c.Main, c.Name)
	}

	return n
}

// language takes the first stack the project loader infers.
func language(root string) string {
	for _, s := range probe.Stack(root) {
		switch s.Value {
		case "Go":
			return "go"
		case "Ruby", "Ruby gem":
			return "ruby"
		case "JS/TS":
			return "javascript"
		}
	}

	return ""
}

// defaultRef is origin's HEAD, else a local main or master, and HEAD last: a
// pushed rather than cloned repository has no origin HEAD, and a main
// checkout can sit on a feature branch.
func defaultRef(ctx context.Context, dir string) string {
	if def := gitx.Out(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); def != "" {
		return def
	}

	for _, b := range []string{"main", "master"} {
		if sha, err := gitx.Ref(ctx, dir, "refs/heads/"+b); err == nil && sha != "" {
			return b
		}
	}

	return "HEAD"
}

// readGit fills the checkout fields, keeping what it read before a failure.
func readGit(ctx context.Context, dir string, now time.Time, n *Node) error {
	ref := defaultRef(ctx, dir)

	out, err := gitx.Exec(ctx, dir, "log", "--format=%cI", ref)
	if err != nil {
		return fmt.Errorf("reading the history of %s: %w", ref, err)
	}

	if out == "" {
		return fmt.Errorf("%s has no commits", ref)
	}

	var first, last time.Time

	perMonth := map[string]int{}

	for line := range strings.Lines(out) {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(line))
		if err != nil {
			return fmt.Errorf("reading a commit date: %w", err)
		}

		at = at.UTC()
		// minimum and maximum, not root and tip: rebased history puts dates out of order
		if first.IsZero() || at.Before(first) {
			first = at
		}

		if at.After(last) {
			last = at
		}

		perMonth[at.Format("2006-01")]++
	}

	n.FirstCommit, n.LastCommit = first.Format(time.RFC3339), last.Format(time.RFC3339)
	n.CommitsPerMonth = months(first, now, perMonth)

	tag, err := latestTag(ctx, dir, ref)
	if err != nil {
		return err
	}

	n.LatestTag = tag

	return nil
}

// months runs from first's month through now's, so a renderer draws the
// rhythm without filling gaps.
func months(first, now time.Time, counts map[string]int) []Month {
	var out []Month

	m := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	for ; !m.After(end); m = m.AddDate(0, 1, 0) {
		key := m.Format("2006-01")
		out = append(out, Month{Month: key, Commits: counts[key]})
	}

	return out
}

// latestTag reads the tagged commit's date, through the tag object for an
// annotated tag; tags on one commit go by name, the last winning.
func latestTag(ctx context.Context, dir, ref string) (*Tag, error) {
	out, err := gitx.Exec(ctx, dir, "for-each-ref", "--merged="+ref,
		"--format=%(refname:short)%09%(*committerdate:iso-strict)%09%(committerdate:iso-strict)", "refs/tags")
	if err != nil {
		return nil, fmt.Errorf("reading tags: %w", err)
	}

	var best *Tag

	var bestAt time.Time

	for line := range strings.Lines(out) {
		// git's output is trimmed, so the last line's empty date field may be gone
		fields := append(strings.Split(strings.TrimRight(line, "\n"), "\t"), "", "")

		at, err := time.Parse(time.RFC3339, cmp.Or(fields[1], fields[2]))
		if err != nil {
			return nil, errors.New("reading the date of tag " + fields[0])
		}

		at = at.UTC()
		if best == nil || at.After(bestAt) || (at.Equal(bestAt) && fields[0] > best.Name) {
			best, bestAt = &Tag{Name: fields[0], At: at.Format(time.RFC3339)}, at
		}
	}

	return best, nil
}
