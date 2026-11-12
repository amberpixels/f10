package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/atlas"
	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func writeDeclared(t *testing.T, home, body string) {
	t.Helper()

	dir := storage.UserDir(home)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, atlas.DeclaredFile), []byte("---\n"+body+"---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// One public checkout and one aliased: the document holds gears in full and
// the other under its alias alone, and --out writes the same bytes.
func TestAtlasPrintsPublicAndAnonymousNodes(t *testing.T) {
	home := testkit.IsolateEnv(t)
	root := testkit.Canonical(t, t.TempDir())
	gears := testkit.ScopeRepo(t, root, "gears")
	secret := testkit.ScopeRepo(t, root, "secret")

	writeDeclared(
		t,
		home,
		"projects:\n  gears: {status: stable, tagline: Helpers., public: true}\n  secret: {alias: client}\n",
	)

	out, err := runApp(t, "-C", gears, "-C", secret, "atlas", "--json").outErr()
	if err != nil {
		t.Fatal(err)
	}

	var doc atlas.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if len(doc.Nodes) != 2 || doc.Nodes[0].ID != "local/gears" || doc.Nodes[0].Status != "stable" ||
		!reflect.DeepEqual(doc.Nodes[1], atlas.Node{ID: "anon/client", Kind: "anonymous", Alias: "client"}) {
		t.Errorf("nodes = %+v, want gears then anon/client", doc.Nodes)
	}

	if strings.Contains(out, "secret") || strings.Contains(out, root) {
		t.Errorf("the document names a private project or a path:\n%s", out)
	}

	file := filepath.Join(t.TempDir(), "atlas.json")
	if _, err := runApp(t, "-C", gears, "-C", secret, "atlas", "--json", "--out", file).outErr(); err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	// generated_at may tick between the runs
	if got, want := withoutStamp(string(written)), withoutStamp(out); got != want {
		t.Errorf("--out wrote\n%s\nwant\n%s", got, want)
	}

	text, err := runApp(t, "-C", gears, "atlas").outErr()
	if err != nil {
		t.Fatal(err)
	}

	// name, status, language, last commit, tagline
	if f := strings.Fields(text); len(f) != 5 || f[0] != "gears" || f[1] != "stable" || f[2] != "-" ||
		len(f[3]) != len("2006-01-02") || f[4] != "Helpers." {
		t.Errorf("terminal line = %q", text)
	}

	text, err = runApp(t, "-C", gears, "-C", secret, "atlas").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if rows := strings.Split(strings.TrimSpace(text), "\n"); len(rows) != 2 ||
		!slices.Equal(strings.Fields(rows[1]), []string{"client", "-", "-", "-", "anonymous"}) {
		t.Errorf("terminal output = %q, want the anonymous row last", text)
	}
}

func withoutStamp(doc string) string {
	var lines []string

	for line := range strings.Lines(doc) {
		if !strings.Contains(line, `"generated_at"`) {
			lines = append(lines, line)
		}
	}

	return strings.Join(lines, "")
}

func TestAtlasRefusesABrokenDeclaredFile(t *testing.T) {
	home := testkit.IsolateEnv(t)
	gears := testkit.ScopeRepo(t, testkit.Canonical(t, t.TempDir()), "gears")

	for body, want := range map[string]string{
		"projects:\n  gears: {status: dormant}\n": "projects.gears: status",
		"projects:\n  gears: {colour: red}\n":     "colour",
	} {
		writeDeclared(t, home, body)

		if _, err := runApp(t, "-C", gears, "atlas").outErr(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want it to name %q", body, err, want)
		}
	}
}

func TestAtlasScopeErrors(t *testing.T) {
	testkit.IsolateEnv(t)
	t.Setenv(projects.RootsEnv, "")

	if _, err := runApp(t, "atlas", "--all").outErr(); err == nil || !strings.Contains(err.Error(), projects.RootsEnv) {
		t.Errorf("--all: err = %v, want %s named", err, projects.RootsEnv)
	}

	if _, err := runApp(t, "-C", t.TempDir(), "atlas").outErr(); err == nil ||
		!strings.Contains(err.Error(), "not a git checkout") {
		t.Errorf("-C a plain dir: err = %v", err)
	}

	gears := testkit.ScopeRepo(t, testkit.Canonical(t, t.TempDir()), "gears")
	if _, err := runApp(t, "-C", gears, "atlas", "--out", "x.json").outErr(); err == nil ||
		!strings.Contains(err.Error(), "--json") {
		t.Errorf("--out alone: err = %v", err)
	}
}

// A linked worktree is its main checkout: the node takes the main checkout's
// name, the normalised remote as its id, and origin HEAD's history.
func TestAtlasReadsAWorktreeAsItsMainCheckout(t *testing.T) {
	home := testkit.IsolateEnv(t)
	r := gitxtest.Clone(t, "gears")
	r.Commit("2026-10-01T08:00:00Z", "go.mod", "module github.com/acme/gears\n", "start")
	r.Git("push", "-q", "origin", "main")
	r.Git("remote", "set-head", "origin", "main")
	r.Git("remote", "set-url", "origin", "git@github.com:acme/gears.git")

	wt := filepath.Join(filepath.Dir(r.Dir), "gears.feature")
	r.Git("worktree", "add", "-q", "-b", "feature", wt)
	gitxtest.Repo{T: t, Dir: wt}.Commit("2026-11-01T08:00:00Z", "b", "2", "feature work")

	writeDeclared(t, home, "projects:\n  gears: {public: true}\n")

	out, err := runApp(t, "-C", wt, "atlas", "--json").outErr()
	if err != nil {
		t.Fatal(err)
	}

	var doc atlas.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if len(doc.Nodes) != 1 {
		t.Fatalf("nodes = %+v, want one", doc.Nodes)
	}

	if n := doc.Nodes[0]; n.ID != "github.com/acme/gears" || n.Name != "gears" || n.Language != "go" ||
		n.LastCommit != "2026-10-01T08:00:00Z" {
		t.Errorf("node = %+v, want gears by its remote with main's tip", n)
	}
}

// A requirement, a link, a requirement on an aliased checkout drawn to its
// anonymous node, and one on an unaliased private checkout left out; the
// terminal names both directions.
func TestAtlasPrintsTheEdges(t *testing.T) {
	home := testkit.IsolateEnv(t)
	root := testkit.Canonical(t, t.TempDir())
	widgets := testkit.ScopeRepo(t, root, "widgets")
	jsonish := testkit.ScopeRepo(t, root, "jsonish")
	herd := testkit.ScopeRepo(t, root, "herd")
	secret := testkit.ScopeRepo(t, root, "secret")
	hidden := testkit.ScopeRepo(t, root, "hidden")

	for dir, body := range map[string]string{
		widgets: "module github.com/acme/widgets\n\nrequire (\n\tgithub.com/acme/json v0.1.0\n" +
			"\tgithub.com/acme/secret v0.1.0\n\tgithub.com/acme/hidden v0.1.0\n)\n",
		jsonish: "module github.com/acme/json\n",
		herd:    "module github.com/acme/herd\n",
		secret:  "module github.com/acme/secret\n",
		hidden:  "module github.com/acme/hidden\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writeDeclared(t, home, "projects:\n"+
		"  widgets: {public: true, links: [{to: herd, relation: plugin for}]}\n"+
		"  jsonish: {public: true}\n  herd: {public: true}\n  hidden: {alias: client-app}\n")

	args := []string{"-C", widgets, "-C", jsonish, "-C", herd, "-C", secret, "-C", hidden, "atlas"}

	out, err := runApp(t, append(args, "--json")...).outErr()
	if err != nil {
		t.Fatal(err)
	}

	var doc atlas.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	want := []atlas.Edge{
		{From: "local/widgets", To: "anon/client-app", Kind: "depends", Source: "manifest", Manifest: "go.mod"},
		{From: "local/widgets", To: "local/herd", Kind: "link", Source: "declared", Relation: "plugin for"},
		{From: "local/widgets", To: "local/jsonish", Kind: "depends", Source: "manifest", Manifest: "go.mod"},
	}
	if !slices.Equal(doc.Edges, want) {
		t.Errorf("edges = %+v, want %+v", doc.Edges, want)
	}

	text, err := runApp(t, args...).outErr()
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{
		"  used by: widgets (plugin for)",
		"  used by: widgets\n",
		"  depends on: client-app, herd (plugin for), jsonish\n",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("terminal output lacks %q:\n%s", line, text)
		}
	}

	if strings.Contains(out, "hidden") || strings.Contains(text, "hidden") || strings.Contains(out, "secret") {
		t.Errorf("a private project is named:\n%s\n%s", out, text)
	}
}

// A broken manifest on an aliased checkout is a stderr warning under its
// alias, and --json stays one valid document.
func TestAtlasWarnsOnAnAnonymousNodesBrokenManifest(t *testing.T) {
	home := testkit.IsolateEnv(t)
	root := testkit.Canonical(t, t.TempDir())
	gears := testkit.ScopeRepo(t, root, "gears")
	secret := testkit.ScopeRepo(t, root, "secret")

	if err := os.WriteFile(filepath.Join(secret, "package.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	writeDeclared(t, home, "projects:\n  gears: {public: true}\n  secret: {alias: client}\n")

	out, errOut, err := runApp(t, "-C", gears, "-C", secret, "atlas", "--json").streams()
	if err != nil {
		t.Fatal(err)
	}

	var doc atlas.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Nodes) != 2 {
		t.Fatalf("stdout is not the two-node document (%v):\n%s", err, out)
	}

	if want := "warning: client: a manifest did not read, so its depends edges are left out\n"; errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}
