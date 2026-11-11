package atlas

import (
	"cmp"
	"path/filepath"
	"slices"
)

const (
	edgeDepends = "depends"
	edgeLink    = "link"
)

// A read is one exported node with what its manifests and entry say.
type read struct {
	checkout Checkout
	node     Node
	m        manifests
	links    []Link
	warning  string // an anonymous node's failed read, already scrubbed
}

// nodeIndex finds the node a dependency, a replace or a link names, among
// exported nodes only: an edge to a dropped project is never drawn.
type nodeIndex struct {
	declared map[ecosystem]map[string][]string // declared name to node ids
	ids      map[string]bool
	byDir    map[string]string
	reads    []read
}

func indexNodes(reads []read) nodeIndex {
	ix := nodeIndex{
		declared: map[ecosystem]map[string][]string{},
		ids:      map[string]bool{},
		byDir:    map[string]string{},
		reads:    reads,
	}

	for _, r := range reads {
		id := r.node.ID
		ix.ids[id] = true
		ix.byDir[physical(r.checkout.Main)] = id

		for eco, names := range r.m.names {
			if ix.declared[eco] == nil {
				ix.declared[eco] = map[string][]string{}
			}

			for _, name := range names {
				ix.declared[eco][name] = append(ix.declared[eco][name], id)
			}
		}
	}

	return ix
}

func physical(dir string) string {
	if p, err := filepath.EvalSymlinks(dir); err == nil {
		return p
	}

	return filepath.Clean(dir)
}

// byName is the node declaring name. A name several nodes declare, such as an
// upstream and a fork that kept its module path, goes to the node whose id is
// the name, else to none, because a guess would draw a wrong edge.
func (ix nodeIndex) byName(eco ecosystem, name string) string {
	ids := ix.declared[eco][name]

	switch {
	case len(ids) == 1:
		return ids[0]
	case slices.Contains(ids, name):
		return name
	}

	return ""
}

// byReplace is the node a replace points at. The id comes before the declared
// name, because a fork usually keeps the upstream module path.
func (ix nodeIndex) byReplace(root string, r replacement) string {
	if r.local {
		dir := r.newPath
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}

		return ix.byDir[physical(dir)]
	}

	if ix.ids[r.newPath] {
		return r.newPath
	}

	return ix.byName(ecoGo, r.newPath)
}

func (ix nodeIndex) byKey(key string) string {
	for _, r := range ix.reads {
		if r.checkout.matches(key) {
			return r.node.ID
		}
	}

	return ""
}

// deriveEdges draws every edge between two exported nodes, in the spec's
// order. One from an anonymous node loses its manifest and dev mark, because
// the manifest's file names the private project's language, and one between
// two anonymous nodes is dropped, because it describes nothing public.
func deriveEdges(reads []read) []Edge {
	ix := indexNodes(reads)
	anon := map[string]bool{}

	for _, r := range reads {
		if r.node.Kind == kindAnonymous {
			anon[r.node.ID] = true
		}
	}

	var all []Edge

	for _, r := range reads {
		all = append(all, ix.manifestEdges(r)...)

		for _, l := range r.links {
			if to := ix.byKey(l.To); to != "" {
				all = append(
					all,
					Edge{From: r.node.ID, To: to, Kind: edgeLink, Source: "declared", Relation: l.Relation},
				)
			}
		}
	}

	kept := all[:0]

	for _, e := range all {
		if anon[e.From] && anon[e.To] {
			continue
		}

		if anon[e.From] {
			e.Manifest, e.Dev = "", false
		}

		kept = append(kept, e)
	}

	return collapse(kept)
}

// manifestEdges sends a replaced requirement to its replacement only, since
// that is the code the project builds: a replacement that is no node drops
// the edge. A replace whose target is a node is drawn even when nothing
// requires it directly.
func (ix nodeIndex) manifestEdges(r read) []Edge {
	from := r.node.ID
	replaced := map[string]string{}

	for _, rep := range r.m.replaces {
		replaced[rep.old] = ix.byReplace(r.checkout.Main, rep)
	}

	var out []Edge

	for _, d := range r.m.deps {
		to := ix.byName(d.eco, d.name)
		if d.eco == ecoGo {
			if rep, ok := replaced[d.name]; ok {
				to = rep

				delete(replaced, d.name)
			}
		}

		if to != "" {
			out = append(
				out,
				Edge{From: from, To: to, Kind: edgeDepends, Source: "manifest", Manifest: d.manifest, Dev: d.dev},
			)
		}
	}

	for _, to := range replaced {
		if to == "" {
			continue
		}

		out = append(out, Edge{From: from, To: to, Kind: edgeDepends, Source: "manifest", Manifest: manifestGoMod})
	}

	return out
}

// collapse drops self-edges and merges duplicates, dev only when every
// mention is, then sorts by from, to, kind, then manifest or relation.
func collapse(all []Edge) []Edge {
	out := []Edge{}
	at := map[[4]string]int{}

	for _, e := range all {
		if e.From == e.To {
			continue
		}

		key := [4]string{e.From, e.To, e.Kind, e.Manifest + e.Relation}
		if i, ok := at[key]; ok {
			out[i].Dev = out[i].Dev && e.Dev

			continue
		}

		at[key] = len(out)
		out = append(out, e)
	}

	slices.SortFunc(out, func(a, b Edge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To), cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Manifest+a.Relation, b.Manifest+b.Relation))
	})

	return out
}
