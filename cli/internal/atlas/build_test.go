package atlas

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
)

var buildNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func buildOne(t *testing.T, c Checkout, e Entry) Node {
	t.Helper()

	doc := Build(t.Context(), map[string]Entry{c.Main: e}, []Checkout{c}, buildNow)
	if len(doc.Nodes) != 1 {
		t.Fatalf("nodes = %v, want one", doc.Nodes)
	}

	fits(t, doc)

	return doc.Nodes[0]
}

// fits walks the marshalled document against the schema, so the output
// cannot drift from docs/atlas.schema.json.
func fits(t *testing.T, doc Document) []byte {
	t.Helper()

	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range validate(t, readSchema(t), "Atlas", decode(t, raw)) {
		t.Error(e)
	}

	return raw
}

func TestBuildReadsTheHistory(t *testing.T) {
	t.Parallel()

	r := gitxtest.Init(t, filepath.Join(t.TempDir(), "gears"), "main")
	r.Commit("2026-08-03T09:14:00Z", "go.mod", "module github.com/acme/gears\n", "start")
	r.Commit("2026-08-20T10:00:00Z", "a", "1", "more")
	r.Commit("2026-10-28T17:40:00Z", "b", "2", "later")
	r.At("2026-10-28T17:40:00Z", "tag", "v0.2.0")
	r.At("2026-10-28T17:40:00Z", "tag", "v0.3.0")
	r.At("2026-10-28T17:40:00Z", "tag", "-a", "-m", "old", "v0.1.0", "HEAD~2")

	n := buildOne(t, Checkout{Main: r.Dir, Name: "gears", Local: true},
		Entry{Public: true, Role: "Go library", Status: "stable", Tagline: "Helpers."})

	if n.ID != "local/gears" || n.Language != "go" || n.Status != "stable" || n.Error != "" {
		t.Errorf("node = %+v", n)
	}

	if n.FirstCommit != "2026-08-03T09:14:00Z" || n.LastCommit != "2026-10-28T17:40:00Z" {
		t.Errorf("dates = %s .. %s", n.FirstCommit, n.LastCommit)
	}

	want := []Month{{"2026-08", 2}, {"2026-09", 0}, {"2026-10", 1}, {"2026-11", 0}}
	if !slices.Equal(n.CommitsPerMonth, want) {
		t.Errorf("months = %v, want %v", n.CommitsPerMonth, want)
	}

	// two tags on the tip go by name; the annotated one is older
	if n.LatestTag == nil || *n.LatestTag != (Tag{Name: "v0.3.0", At: "2026-10-28T17:40:00Z"}) {
		t.Errorf("tag = %+v", n.LatestTag)
	}
}

func TestBuildAnnotatedTagAndNoTag(t *testing.T) {
	t.Parallel()

	r := gitxtest.Init(t, filepath.Join(t.TempDir(), "stamp"), "main")
	r.Commit("2026-11-02T08:00:00Z", "Gemfile", "source 'x'\n", "start")

	if n := buildOne(t, Checkout{Main: r.Dir, Name: "stamp", Local: true}, Entry{Public: true}); n.LatestTag != nil ||
		n.Language != "ruby" {
		t.Errorf("node = %+v, want ruby with no tag", n)
	}

	r.At("2026-11-05T08:00:00Z", "tag", "-a", "-m", "release", "v1.0.0")

	n := buildOne(t, Checkout{Main: r.Dir, Name: "stamp", Local: true}, Entry{Public: true})
	if n.LatestTag == nil || n.LatestTag.At != "2026-11-02T08:00:00Z" {
		t.Errorf("tag = %+v, want the tagged commit's date", n.LatestTag)
	}
}

// A main checkout on a feature branch, with no origin HEAD, still reads main.
func TestBuildReadsMainOverAFeatureBranch(t *testing.T) {
	t.Parallel()

	r := gitxtest.Init(t, filepath.Join(t.TempDir(), "fork"), "main")
	r.Commit("2026-10-01T08:00:00Z", "a", "1", "start")
	r.Git("switch", "-q", "-c", "feature")
	r.Commit("2026-11-01T08:00:00Z", "b", "2", "feature work")

	if n := buildOne(t, Checkout{Main: r.Dir, Name: "fork", Local: true}, Entry{Public: true}); n.LastCommit !=
		"2026-10-01T08:00:00Z" {
		t.Errorf("last = %s, want main's tip", n.LastCommit)
	}
}

// A failed read keeps the declared fields and names no path.
func TestBuildReportsAFailedReadWithoutThePath(t *testing.T) {
	t.Parallel()

	r := gitxtest.Init(t, filepath.Join(t.TempDir(), "empty"), "main")

	n := buildOne(t, Checkout{Main: r.Dir, Name: "empty", ID: "github.com/acme/empty"},
		Entry{Public: true, Status: "parked", Tagline: "Nothing yet."})

	if n.Error == "" || strings.Contains(n.Error, r.Dir) || n.Status != "parked" || n.Tagline != "Nothing yet." {
		t.Errorf("node = %+v, want an error without %s and the declared fields kept", n, r.Dir)
	}
}

// A private project is neither printed nor read, alias or not.
func TestBuildDropsPrivateProjects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pub := gitxtest.Init(t, filepath.Join(root, "gears"), "main")
	pub.Commit("2026-11-01T08:00:00Z", "a", "1", "start")

	secret := gitxtest.Init(t, filepath.Join(root, "secret-client"), "main")
	secret.Commit("2026-11-01T08:00:00Z", "a", "1", "start")

	scope := []Checkout{
		{Main: pub.Dir, Name: "gears", Local: true},
		{Main: secret.Dir, Name: "secret-client", ID: "github.com/acme/secret-client"},
		{Main: filepath.Join(root, "unread"), Name: "unread", Local: true},
	}
	entries := map[string]Entry{pub.Dir: {Public: true}, secret.Dir: {Alias: "client-app"}}

	doc := Build(t.Context(), entries, scope, buildNow)
	raw := string(fits(t, doc))

	for _, leak := range []string{"secret-client", secret.Dir, "client-app", "unread"} {
		if strings.Contains(raw, leak) {
			t.Errorf("the document names %q: %s", leak, raw)
		}
	}

	if !strings.Contains(raw, `"edges":[]`) {
		t.Errorf("edges not an empty list: %s", raw)
	}
}

func TestBuildSortsByName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	entries := map[string]Entry{}

	var scope []Checkout

	for _, name := range []string{"widgets", "gears"} {
		r := gitxtest.Init(t, filepath.Join(root, name), "main")
		r.Commit("2026-11-01T08:00:00Z", "a", "1", "start")
		scope = append(scope, Checkout{Main: r.Dir, Name: name, Local: true})
		entries[r.Dir] = Entry{Public: true}
	}

	doc := Build(t.Context(), entries, scope, buildNow)
	if len(doc.Nodes) != 2 || doc.Nodes[0].Name != "gears" {
		t.Errorf("nodes = %+v, want gears first", doc.Nodes)
	}
}
