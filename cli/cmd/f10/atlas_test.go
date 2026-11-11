package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/atlas"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/storage"
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

// Two checkouts in scope, one declared public: the document holds that one
// alone, and --out writes the same bytes.
func TestAtlasPrintsPublicProjectsOnly(t *testing.T) {
	home := isolateEnv(t)
	root := canonical(t, t.TempDir())
	gears := scopeRepo(t, root, "gears")
	secret := scopeRepo(t, root, "secret")

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

	if len(doc.Nodes) != 1 || doc.Nodes[0].ID != "local/gears" || doc.Nodes[0].Status != "stable" {
		t.Errorf("nodes = %+v, want gears alone", doc.Nodes)
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
	home := isolateEnv(t)
	gears := scopeRepo(t, canonical(t, t.TempDir()), "gears")

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
	isolateEnv(t)
	t.Setenv(projects.RootsEnv, "")

	if _, err := runApp(t, "atlas", "--all").outErr(); err == nil || !strings.Contains(err.Error(), projects.RootsEnv) {
		t.Errorf("--all: err = %v, want %s named", err, projects.RootsEnv)
	}

	if _, err := runApp(t, "-C", t.TempDir(), "atlas").outErr(); err == nil ||
		!strings.Contains(err.Error(), "not a git checkout") {
		t.Errorf("-C a plain dir: err = %v", err)
	}

	gears := scopeRepo(t, canonical(t, t.TempDir()), "gears")
	if _, err := runApp(t, "-C", gears, "atlas", "--out", "x.json").outErr(); err == nil ||
		!strings.Contains(err.Error(), "--json") {
		t.Errorf("--out alone: err = %v", err)
	}
}
