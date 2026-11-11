package atlas

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func depNames(m manifests) []string {
	var out []string

	for _, d := range m.deps {
		s := d.manifest + ":" + d.name
		if d.dev {
			s += "(dev)"
		}

		out = append(out, s)
	}

	slices.Sort(out)

	return out
}

func TestReadManifests(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		files    map[string]string
		names    map[ecosystem][]string
		deps     []string
		replaces []replacement
	}{
		"go.mod skips indirect, marks tools dev, keeps replaces": {
			files: map[string]string{"go.mod": `module github.com/acme/widgets

go 1.26

require (
	github.com/acme/json v0.1.0
	github.com/acme/lint v0.2.0
	github.com/acme/deep v0.3.0 // indirect
)

tool github.com/acme/lint/cmd/lint

replace github.com/acme/json => ../json

replace github.com/up/gears => github.com/acme/gears v0.0.1
`},
			names: map[ecosystem][]string{ecoGo: {"github.com/acme/widgets"}},
			deps:  []string{"go.mod:github.com/acme/json", "go.mod:github.com/acme/lint(dev)"},
			replaces: []replacement{
				{old: "github.com/acme/json", newPath: "../json", local: true},
				{old: "github.com/up/gears", newPath: "github.com/acme/gears"},
			},
		},
		"package.json splits dev from runtime": {
			files: map[string]string{"package.json": `{"name": "@acme/look",
				"dependencies": {"gears": "1"}, "peerDependencies": {"both": "1"},
				"devDependencies": {"both": "1", "tester": "2"}}`},
			names: map[ecosystem][]string{ecoNPM: {"@acme/look"}},
			deps:  []string{"package.json:both", "package.json:both(dev)", "package.json:gears", "package.json:tester(dev)"},
		},
		"gemspec with any receiver": {
			files: map[string]string{"bright.gemspec": `Gem::Specification.new do |s|
  s.name = "bright"
  s.add_dependency "rails", ">= 7"
  s.add_runtime_dependency("paint")
  s.add_development_dependency 'rspec'
end
`},
			names: map[ecosystem][]string{ecoGem: {"bright"}},
			deps:  []string{"gemspec:paint", "gemspec:rails", "gemspec:rspec(dev)"},
		},
		"Gemfile groups, nested blocks and inline options": {
			files: map[string]string{"Gemfile": `source "https://rubygems.org"
gemspec

gem "rails"
# gem "commented"
group :development, :test do
  platforms :mri do
    gem "debug"
  end
  gem "rspec"
end
group :production, :development do
  gem "puma"
end
gem "pry", group: :development
gem "rake", groups: [:development, :default]
`},
			deps: []string{
				"Gemfile:debug(dev)", "Gemfile:pry(dev)", "Gemfile:puma", "Gemfile:rails", "Gemfile:rake", "Gemfile:rspec(dev)",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFiles(t, dir, tc.files)

			m, err := readManifests(dir)
			if err != nil {
				t.Fatal(err)
			}

			if !mapsEqual(m.names, tc.names) {
				t.Errorf("names = %v, want %v", m.names, tc.names)
			}

			if got := depNames(m); !slices.Equal(got, tc.deps) {
				t.Errorf("deps = %v, want %v", got, tc.deps)
			}

			if !slices.Equal(m.replaces, tc.replaces) {
				t.Errorf("replaces = %+v, want %+v", m.replaces, tc.replaces)
			}
		})
	}
}

func mapsEqual(a, b map[ecosystem][]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}

	return true
}

// A malformed manifest is an error naming the file, and the others still read.
func TestReadManifestsMalformed(t *testing.T) {
	t.Parallel()

	for file, body := range map[string]string{"go.mod": "module\nrequire (\n", "package.json": "{"} {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{file: body, "Gemfile": `gem "rails"` + "\n"})

		m, err := readManifests(dir)
		if err == nil || !strings.Contains(err.Error(), file) {
			t.Errorf("%s: err = %v, want one naming it", file, err)
		}

		if got := depNames(m); !slices.Equal(got, []string{"Gemfile:rails"}) {
			t.Errorf("%s: deps = %v, want the Gemfile still read", file, got)
		}
	}
}
