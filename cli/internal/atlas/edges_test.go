package atlas

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
)

// One scope covering every edge rule: names matched by what a manifest
// declares, indirect requirements, a fork and a local replace, dev marks, two
// manifests naming one gem, a link beside a dependency, and a private end.
func TestBuildDerivesTheEdges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	entries := map[string]Entry{}

	var scope []Checkout

	add := func(name, id string, e Entry, files map[string]string) {
		r := gitxtest.Init(t, filepath.Join(root, name), "main")
		r.Commit("2026-11-01T08:00:00Z", "a", "1", "start")
		writeFiles(t, r.Dir, files)

		scope = append(scope, Checkout{Main: r.Dir, Name: name, ID: id, Local: id == ""})
		entries[r.Dir] = e
	}

	public := Entry{Public: true}

	add("widgets", "", Entry{Public: true, Links: []Link{{To: "jsonish", Relation: "showcase for"}}},
		map[string]string{"go.mod": `module github.com/acme/widgets

go 1.26

require (
	github.com/acme/json v0.1.0
	github.com/acme/deep v0.1.0 // indirect
	github.com/up/gears v0.1.0
	github.com/acme/kit v0.1.0
	github.com/acme/secret v0.1.0
	github.com/acme/forked v0.1.0
)

replace github.com/acme/forked => ../secret

replace github.com/up/gears => github.com/acme/gears v0.0.1

replace github.com/acme/kit => ../kit
`})
	add("jsonish", "", public, map[string]string{"go.mod": "module github.com/acme/json\n"})
	// replaced by the private checkout, so widgets builds no code of it
	add("forked", "", public, map[string]string{"go.mod": "module github.com/acme/forked\n"})
	add("deep", "", public, map[string]string{"go.mod": "module github.com/acme/deep\n"})
	add("kit", "", public, map[string]string{"go.mod": "module github.com/acme/kit\n"})
	// a fork that kept the upstream module path, beside the upstream itself
	add("gears", "github.com/acme/gears", public, map[string]string{"go.mod": "module github.com/up/gears\n"})
	add("upgears", "github.com/up/gears", public, map[string]string{"go.mod": "module github.com/up/gears\n"})
	add(
		"user",
		"",
		public,
		map[string]string{"go.mod": "module github.com/acme/user\n\nrequire github.com/up/gears v0.1.0\n"},
	)
	add("look", "", public, map[string]string{"package.json": `{"name": "look", "devDependencies": {"@acme/ui": "1"}}`})
	add("ui", "", public, map[string]string{"package.json": `{"name": "@acme/ui"}`})
	add("bright", "", public, map[string]string{
		"bright.gemspec": "Gem::Specification.new do |s|\n  s.name = 'bright'\n  s.add_dependency 'paint'\nend\n",
		"Gemfile":        "gemspec\ngem 'bright', path: '.'\ngem 'paint'\n",
	})
	add(
		"paint",
		"",
		public,
		map[string]string{"paint.gemspec": "Gem::Specification.new do |s|\n  s.name = 'paint'\nend\n"},
	)
	// private, so never read: its malformed manifest fails nothing
	add(
		"secret",
		"github.com/acme/secret",
		Entry{},
		map[string]string{"go.mod": "module github.com/acme/secret\nrequire (\n"},
	)

	doc, _ := Build(t.Context(), entries, scope, buildNow)
	fits(t, doc)

	dep := func(from, to, manifest string, dev bool) Edge {
		return Edge{From: from, To: to, Kind: edgeDepends, Source: "manifest", Manifest: manifest, Dev: dev}
	}
	want := []Edge{
		dep("local/bright", "local/paint", manifestGemfile, false),
		dep("local/bright", "local/paint", manifestGemspec, false),
		dep("local/look", "local/ui", manifestPackage, true),
		dep("local/user", "github.com/up/gears", manifestGoMod, false),
		dep("local/widgets", "github.com/acme/gears", manifestGoMod, false),
		dep("local/widgets", "local/jsonish", manifestGoMod, false),
		{From: "local/widgets", To: "local/jsonish", Kind: edgeLink, Source: "declared", Relation: "showcase for"},
		dep("local/widgets", "local/kit", manifestGoMod, false),
	}

	if !slices.Equal(doc.Edges, want) {
		t.Errorf("edges =\n%+v\nwant\n%+v", doc.Edges, want)
	}

	for _, n := range doc.Nodes {
		if n.Error != "" {
			t.Errorf("%s: error %q", n.Name, n.Error)
		}
	}
}

// Two nodes declaring one name, neither under that id, draw no edge.
func TestBuildLeavesAnAmbiguousNameOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	entries := map[string]Entry{}

	var scope []Checkout

	for name, body := range map[string]string{
		"a":    "module github.com/acme/twin\n",
		"b":    "module github.com/acme/twin\n",
		"user": "module github.com/acme/user\n\nrequire github.com/acme/twin v0.1.0\n",
	} {
		r := gitxtest.Init(t, filepath.Join(root, name), "main")
		r.Commit("2026-11-01T08:00:00Z", "go.mod", body, "start")
		scope = append(scope, Checkout{Main: r.Dir, Name: name, Local: true})
		entries[r.Dir] = Entry{Public: true}
	}

	if doc, _ := Build(t.Context(), entries, scope, buildNow); len(doc.Edges) != 0 {
		t.Errorf("edges = %+v, want none", doc.Edges)
	}
}

func TestCollapseMergesDevAndDropsSelfEdges(t *testing.T) {
	t.Parallel()

	got := collapse([]Edge{
		{From: "a", To: "b", Kind: edgeDepends, Manifest: manifestPackage, Dev: true},
		{From: "a", To: "a", Kind: edgeDepends, Manifest: manifestPackage},
		{From: "a", To: "b", Kind: edgeDepends, Manifest: manifestPackage},
		{From: "a", To: "c", Kind: edgeDepends, Manifest: manifestPackage, Dev: true},
		{From: "a", To: "c", Kind: edgeDepends, Manifest: manifestPackage, Dev: true},
	})

	want := []Edge{
		{From: "a", To: "b", Kind: edgeDepends, Manifest: manifestPackage},
		{From: "a", To: "c", Kind: edgeDepends, Manifest: manifestPackage, Dev: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("collapse = %+v, want %+v", got, want)
	}
}

// Edges with an anonymous end: no manifest or dev mark from one, a go.mod and
// a package.json naming one project as one edge, links both ways, nothing
// between two anonymous nodes, and a broken manifest warned under the alias.
func TestBuildDerivesEdgesWithAnAnonymousEnd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	entries := map[string]Entry{}

	var scope []Checkout

	add := func(name, id string, e Entry, files map[string]string) string {
		r := gitxtest.Init(t, filepath.Join(root, name), "main")
		r.Commit("2026-11-01T08:00:00Z", "a", "1", "start")
		writeFiles(t, r.Dir, files)

		scope = append(scope, Checkout{Main: r.Dir, Name: name, ID: id, Local: id == ""})
		entries[r.Dir] = e

		return r.Dir
	}

	add("gears", "", Entry{Public: true}, map[string]string{
		"go.mod":       "module github.com/acme/gears\n",
		"package.json": `{"name": "gears-js"}`,
	})
	add("widgets", "", Entry{Public: true, Links: []Link{{To: "client-secret", Relation: "plugin for"}}}, nil)
	add("client-secret", "github.com/acme/client-secret", Entry{Alias: "client-app", Links: []Link{
		{To: "gears", Relation: "showcase for"},
		{To: "partner-secret", Relation: "built for"},
	}}, map[string]string{
		"go.mod":       "module github.com/acme/client-secret\n\nrequire github.com/acme/gears v0.1.0\n",
		"package.json": `{"name": "client", "devDependencies": {"gears-js": "1"}}`,
	})
	add("partner-secret", "", Entry{Alias: "partner-co"}, map[string]string{
		"go.mod": "module github.com/acme/partner\n\nrequire github.com/acme/client-secret v0.1.0\n",
	})
	broken := add("broken-secret", "", Entry{Alias: "broken-co", Links: []Link{{To: "gears", Relation: "uses"}}},
		map[string]string{"go.mod": "module github.com/acme/broken\nrequire (\n"})

	doc, warnings := Build(t.Context(), entries, scope, buildNow)
	fits(t, doc)

	want := []Edge{
		{From: "anon/broken-co", To: "local/gears", Kind: edgeLink, Source: "declared", Relation: "uses"},
		{From: "anon/client-app", To: "local/gears", Kind: edgeDepends, Source: "manifest"},
		{From: "anon/client-app", To: "local/gears", Kind: edgeLink, Source: "declared", Relation: "showcase for"},
		{From: "local/widgets", To: "anon/client-app", Kind: edgeLink, Source: "declared", Relation: "plugin for"},
	}
	if !slices.Equal(doc.Edges, want) {
		t.Errorf("edges =\n%+v\nwant\n%+v", doc.Edges, want)
	}

	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "broken-co: ") ||
		strings.Contains(warnings[0], "broken-secret") || strings.Contains(warnings[0], broken) {
		t.Errorf("warnings = %q, want one under the alias naming no checkout", warnings)
	}
}
