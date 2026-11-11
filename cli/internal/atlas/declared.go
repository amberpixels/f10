// Package atlas derives the Atlas JSON (docs/atlas.md): what each project
// is and how it moves, from the declared file and each checkout's own git
// history and manifest, with no host call.
package atlas

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// DeclaredFile is ~/.f10/atlas.md, the user's own words about each project.
const DeclaredFile = "atlas.md"

var statuses = []string{"active", "stable", "parked", "archived"}

// an alias becomes the id anon/<alias>, so it stays a safe path segment
var aliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Declared is the declared file's frontmatter; unknown keys are refused by
// the reader, as for every user file.
type Declared struct {
	Projects map[string]Entry `yaml:"projects"`
}

type Entry struct {
	Role    string `yaml:"role"`
	Status  string `yaml:"status"`
	Tagline string `yaml:"tagline"`
	Public  bool   `yaml:"public"`
	Alias   string `yaml:"alias"`
	Links   []Link `yaml:"links"`
}

type Link struct {
	To       string `yaml:"to"`
	Relation string `yaml:"relation"`
}

// A Checkout is one project in scope: its main checkout, never a linked
// worktree.
type Checkout struct {
	Main  string
	Name  string // the main checkout's basename
	ID    string // the normalised remote; unused when Local
	Local bool
}

// NodeID is the id edges name the project by; a local one carries its name,
// never its path.
func (c Checkout) NodeID() string {
	if c.Local {
		return "local/" + c.Name
	}

	return c.ID
}

func (c Checkout) matches(key string) bool { return key == c.Name || key == c.NodeID() }

// Resolve checks the declared file's rules, links and aliases included so a
// broken file fails on its first read, and maps each checkout to its entry.
// roots (nil without F10_ROOTS) turns a key matching nothing into a typo.
func Resolve(ctx context.Context, decl Declared, scope []Checkout, roots []string) (map[string]Entry, error) {
	keys := slices.Sorted(maps.Keys(decl.Projects))
	aliases := map[string]string{}

	for _, key := range keys {
		e := decl.Projects[key]

		switch {
		case e.Status != "" && !slices.Contains(statuses, e.Status):
			return nil, fmt.Errorf(
				"projects.%s: status %q is not one of %s",
				key,
				e.Status,
				strings.Join(statuses, ", "),
			)
		case e.Alias != "" && e.Public:
			return nil, fmt.Errorf("projects.%s: an alias on a public entry both names the project and hides it", key)
		case e.Alias != "" && !aliasPattern.MatchString(e.Alias):
			return nil, fmt.Errorf("projects.%s: alias %q is not lowercase letters, digits and hyphens", key, e.Alias)
		case e.Alias != "" && aliases[e.Alias] != "":
			return nil, fmt.Errorf(
				"projects.%s and projects.%s: both declare the alias %q",
				aliases[e.Alias],
				key,
				e.Alias,
			)
		}

		if e.Alias != "" {
			aliases[e.Alias] = key
		}

		for i, l := range e.Links {
			if strings.TrimSpace(l.To) == "" || strings.TrimSpace(l.Relation) == "" {
				return nil, fmt.Errorf("projects.%s: links[%d] needs both to and relation", key, i)
			}
		}
	}

	known := rootIndex(ctx, roots)
	byKey := map[string]string{} // main checkout to the key that claimed it
	out := map[string]Entry{}

	for _, key := range keys {
		var found []Checkout

		for _, c := range scope {
			if c.matches(key) {
				found = append(found, c)
			}
		}

		switch {
		case len(found) > 1:
			return nil, fmt.Errorf("projects.%s: matches %s and %s; key it by id to tell them apart",
				key, found[0].NodeID(), found[1].NodeID())
		case len(found) == 1 && byKey[found[0].Main] != "":
			return nil, fmt.Errorf(
				"projects.%s and projects.%s: both name %s",
				byKey[found[0].Main],
				key,
				found[0].NodeID(),
			)
		case len(found) == 1:
			byKey[found[0].Main] = key
			out[found[0].Main] = decl.Projects[key]
		case known != nil && !known(key):
			return nil, fmt.Errorf("projects.%s: no checkout under F10_ROOTS has this name or id", key)
		}

		for i, l := range decl.Projects[key].Links {
			if known != nil && !inScope(scope, l.To) && !known(l.To) {
				return nil, fmt.Errorf("projects.%s: links[%d]: no checkout under F10_ROOTS is %q", key, i, l.To)
			}
		}
	}

	// an edge names a node by id, so two exported checkouts with one id
	// could not be told apart
	exported := map[string]string{}

	for _, c := range scope {
		if !out[c.Main].Public {
			continue
		}

		if other := exported[c.NodeID()]; other != "" {
			return nil, fmt.Errorf("%s and %s are both exported as %s: rename one checkout or give it a remote",
				other, c.Main, c.NodeID())
		}

		exported[c.NodeID()] = c.Main
	}

	return out, nil
}

func inScope(scope []Checkout, key string) bool {
	return slices.ContainsFunc(scope, func(c Checkout) bool { return c.matches(key) })
}

// rootIndex answers by basename first and reads a root's remote only for an
// id-form key still unmatched, so a one-checkout run pays no git call per
// root in the common case.
func rootIndex(ctx context.Context, roots []string) func(string) bool {
	if roots == nil {
		return nil
	}

	var ids map[string]bool

	return func(key string) bool {
		name := strings.TrimPrefix(key, "local/")
		if slices.ContainsFunc(roots, func(r string) bool { return filepath.Base(r) == name }) {
			return true
		}

		if !strings.Contains(key, "/") {
			return false
		}

		if ids == nil {
			ids = map[string]bool{}

			for _, r := range roots {
				if p := ledger.Where(ctx, r); !p.ProjectLocal {
					ids[p.Project] = true
				}
			}
		}

		return ids[key]
	}
}
