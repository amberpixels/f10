package atlas

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
)

func checkout(name string) Checkout {
	return Checkout{Main: "/code/" + name, Name: name, ID: "github.com/acme/" + name}
}

func TestResolveRefusesABrokenFile(t *testing.T) {
	t.Parallel()

	scope := []Checkout{checkout("gears"), checkout("widgets")}
	twin := Checkout{Main: "/other/gears", Name: "gears", ID: "github.com/fork/gears"}

	for name, tc := range map[string]struct {
		decl  Declared
		scope []Checkout
		roots []string
		want  string
	}{
		"unknown status": {
			decl: Declared{Projects: map[string]Entry{"gears": {Status: "dormant"}}},
			want: "projects.gears: status",
		},
		"alias on a public entry": {
			decl: Declared{Projects: map[string]Entry{"gears": {Public: true, Alias: "g"}}},
			want: "projects.gears: an alias on a public entry",
		},
		"malformed alias": {
			decl: Declared{Projects: map[string]Entry{"gears": {Alias: "Client App"}}},
			want: "projects.gears: alias",
		},
		"duplicate alias": {
			decl: Declared{Projects: map[string]Entry{"gears": {Alias: "x"}, "widgets": {Alias: "x"}}},
			want: "projects.gears and projects.widgets",
		},
		"link without relation": {
			decl: Declared{Projects: map[string]Entry{"gears": {Links: []Link{{To: "widgets"}}}}},
			want: "projects.gears: links[0]",
		},
		"key matching two checkouts": {
			decl:  Declared{Projects: map[string]Entry{"gears": {Public: true}}},
			scope: []Checkout{checkout("gears"), twin},
			want:  "github.com/acme/gears and github.com/fork/gears",
		},
		"two keys naming one checkout": {
			decl: Declared{Projects: map[string]Entry{"gears": {}, "github.com/acme/gears": {}}},
			want: "projects.gears and projects.github.com/acme/gears",
		},
		"two public checkouts with one id": {
			decl:  Declared{Projects: map[string]Entry{"gears": {Public: true}, "gears-copy": {Public: true}}},
			scope: []Checkout{checkout("gears"), {Main: "/other/gears-copy", Name: "gears-copy", ID: "github.com/acme/gears"}},
			want:  "/code/gears and /other/gears-copy are both exported as github.com/acme/gears",
		},
		"typo under the roots": {
			decl:  Declared{Projects: map[string]Entry{"gaers": {Public: true}}},
			roots: []string{"/code/gears", "/code/widgets"},
			want:  "projects.gaers: no checkout under F10_ROOTS",
		},
		"link typo under the roots": {
			decl:  Declared{Projects: map[string]Entry{"gears": {Links: []Link{{To: "widgts", Relation: "for"}}}}},
			roots: []string{"/code/gears", "/code/widgets"},
			want:  "projects.gears: links[0]",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := scope
			if tc.scope != nil {
				in = tc.scope
			}

			_, err := Resolve(t.Context(), tc.decl, in, tc.roots)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// A file written for --all serves a run over one checkout: an entry out of
// scope is skipped when it exists under the roots, or when no roots are set.
func TestResolveSkipsEntriesOutOfScope(t *testing.T) {
	t.Parallel()

	decl := Declared{Projects: map[string]Entry{
		"gears":       {Public: true, Status: "stable"},
		"widgets":     {Public: true, Links: []Link{{To: "gears", Relation: "showcase for"}}},
		"local/notes": {Alias: "notes"},
	}}
	scope := []Checkout{checkout("gears")}

	for _, roots := range [][]string{nil, {"/code/gears", "/code/widgets", "/code/notes"}} {
		got, err := Resolve(t.Context(), decl, scope, roots)
		if err != nil {
			t.Fatalf("roots %v: %v", roots, err)
		}

		if len(got) != 1 || got["/code/gears"].Status != "stable" {
			t.Errorf("roots %v: got %v, want gears alone", roots, got)
		}
	}
}

// An id-form key is matched against a root's remote, read only when no
// basename matched.
func TestResolveMatchesARootByID(t *testing.T) {
	t.Parallel()

	r := gitxtest.Clone(t, "jsonish")
	r.Git("remote", "set-url", "origin", "git@github.com:acme/json.git")

	decl := Declared{Projects: map[string]Entry{"github.com/acme/json": {Public: true}}}
	if _, err := Resolve(t.Context(), decl, []Checkout{checkout("gears")}, []string{r.Dir}); err != nil {
		t.Error(err)
	}
}

func TestResolveAcceptsTheFixtures(t *testing.T) {
	t.Parallel()

	paths, _ := filepath.Glob("testdata/*.declared.md")
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		_, rest, _ := strings.Cut(string(raw), "---\n")
		front, _, _ := strings.Cut(rest, "\n---")

		var decl Declared

		dec := yaml.NewDecoder(strings.NewReader(front))
		dec.KnownFields(true)

		if err := dec.Decode(&decl); err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		var scope []Checkout
		for key := range decl.Projects {
			if !strings.Contains(key, "/") {
				scope = append(scope, checkout(key))
			}
		}

		if _, err := Resolve(t.Context(), decl, scope, nil); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}
