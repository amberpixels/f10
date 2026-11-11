package atlas

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

// An ecosystem scopes a name, so a Go module never matches an npm package
// that happens to share it.
type ecosystem string

const (
	ecoGo  ecosystem = "go"
	ecoNPM ecosystem = "npm"
	ecoGem ecosystem = "gem"
)

// The manifest values an edge names, as the schema's enum spells them.
const (
	manifestGoMod   = "go.mod"
	manifestPackage = "package.json"
	manifestGemspec = "gemspec"
	manifestGemfile = "Gemfile"
)

type dependency struct {
	eco      ecosystem
	name     string
	manifest string
	dev      bool
}

// A replacement is a go.mod replace; local means newPath is a directory.
type replacement struct {
	old     string
	newPath string
	local   bool
}

// manifests is what the root manifests say: the names the project declares,
// what it depends on, and its go.mod replaces.
type manifests struct {
	names    map[ecosystem][]string
	deps     []dependency
	replaces []replacement
}

func (m *manifests) declare(eco ecosystem, name string) {
	if name == "" {
		return
	}

	if m.names == nil {
		m.names = map[ecosystem][]string{}
	}

	m.names[eco] = append(m.names[eco], name)
}

// readManifests reads the manifests at root only, the files the language is
// inferred from. A malformed one is an error, and the others are still read.
func readManifests(root string) (manifests, error) {
	var m manifests

	errs := []error{readGoMod(root, &m), readPackageJSON(root, &m), readGemspecs(root, &m), readGemfile(root, &m)}

	return m, errors.Join(errs...)
}

// readFile reads a missing file as no data and no error.
func readFile(root, name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	return data, nil
}

func readGoMod(root string, m *manifests) error {
	data, err := readFile(root, manifestGoMod)
	if data == nil {
		return err
	}

	f, err := modfile.Parse(manifestGoMod, data, nil)
	if err != nil {
		return fmt.Errorf("reading %s: %w", manifestGoMod, err)
	}

	if f.Module != nil {
		m.declare(ecoGo, f.Module.Mod.Path)
	}

	for _, r := range f.Require {
		// the direct requirement that pulls an indirect one in is its own edge
		if r.Indirect {
			continue
		}

		path := r.Mod.Path
		// go.mod cannot tell a module both imported and run as a tool from a
		// tool alone, so any tool package marks it dev
		dev := slices.ContainsFunc(f.Tool, func(t *modfile.Tool) bool {
			return t.Path == path || strings.HasPrefix(t.Path, path+"/")
		})
		m.deps = append(m.deps, dependency{eco: ecoGo, name: path, manifest: manifestGoMod, dev: dev})
	}

	for _, r := range f.Replace {
		m.replaces = append(m.replaces, replacement{old: r.Old.Path, newPath: r.New.Path, local: r.New.Version == ""})
	}

	return nil
}

func readPackageJSON(root string, m *manifests) error {
	data, err := readFile(root, manifestPackage)
	if data == nil {
		return err
	}

	var pkg struct {
		Name                 string            `json:"name"`
		Dependencies         map[string]string `json:"dependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
	}

	if err := json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("reading %s: %w", manifestPackage, err)
	}

	m.declare(ecoNPM, pkg.Name)

	for _, deps := range []map[string]string{pkg.Dependencies, pkg.PeerDependencies, pkg.OptionalDependencies} {
		for name := range deps {
			m.deps = append(m.deps, dependency{eco: ecoNPM, name: name, manifest: manifestPackage})
		}
	}

	for name := range pkg.DevDependencies {
		m.deps = append(m.deps, dependency{eco: ecoNPM, name: name, manifest: manifestPackage, dev: true})
	}

	return nil
}

// Ruby manifests are code, so they are read by line, not evaluated: the
// common literal forms are covered, computed names are not.
var (
	gemspecName = regexp.MustCompile(`\.name\s*=\s*["']([^"']+)["']`)
	gemspecDep  = regexp.MustCompile(`\.add_(runtime_|development_)?dependency\s*\(?\s*["']([^"']+)["']`)
	gemfileGem  = regexp.MustCompile(`^gem\s*\(?\s*["']([^"']+)["'](.*)$`)
	gemfileGrp  = regexp.MustCompile(`^group\s*\(?(.*?)\)?\s+do\b`)
	inlineGroup = regexp.MustCompile(`\bgroups?:\s*(\[[^\]]*\]|:\w+|["']\w+["'])`)
	groupName   = regexp.MustCompile(`:(\w+)|["'](\w+)["']`)
	opensBlock  = regexp.MustCompile(`(\bdo(\s*\|[^|]*\|)?|^(if|unless|case|begin|while|until)\b.*)$`)
	closesBlock = regexp.MustCompile(`^end\b`)
)

func readGemspecs(root string, m *manifests) error {
	specs, err := filepath.Glob(filepath.Join(root, "*.gemspec"))
	if err != nil {
		return fmt.Errorf("finding the gemspecs: %w", err)
	}

	for _, spec := range specs {
		data, err := readFile(root, filepath.Base(spec))
		if err != nil {
			return err
		}

		for line := range strings.Lines(string(data)) {
			if g := gemspecName.FindStringSubmatch(line); g != nil {
				m.declare(ecoGem, g[1])
			}

			if g := gemspecDep.FindStringSubmatch(line); g != nil {
				m.deps = append(m.deps, dependency{
					eco: ecoGem, name: g[2], manifest: manifestGemspec, dev: g[1] == "development_",
				})
			}
		}
	}

	return nil
}

// readGemfile tracks block depth by do/end, so a block nested in a
// development group stays in the group.
func readGemfile(root string, m *manifests) error {
	data, err := readFile(root, manifestGemfile)
	if data == nil {
		return err
	}

	var devStack []bool // one entry per open block: whether it is development-only

	inDev := func() bool { return len(devStack) > 0 && devStack[len(devStack)-1] }

	for raw := range strings.Lines(string(data)) {
		line := strings.TrimSpace(raw)

		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case closesBlock.MatchString(line):
			if len(devStack) > 0 {
				devStack = devStack[:len(devStack)-1]
			}
		case gemfileGrp.MatchString(line):
			groups := gemfileGrp.FindStringSubmatch(line)[1]
			devStack = append(devStack, inDev() || devOnly(groups))
		case gemfileGem.MatchString(line):
			g := gemfileGem.FindStringSubmatch(line)
			dev := inDev()

			if opt := inlineGroup.FindStringSubmatch(g[2]); opt != nil {
				dev = dev || devOnly(opt[1])
			}

			m.deps = append(m.deps, dependency{eco: ecoGem, name: g[1], manifest: manifestGemfile, dev: dev})
		case opensBlock.MatchString(line):
			devStack = append(devStack, inDev())
		}
	}

	return nil
}

// devOnly is a group list naming only development and test.
func devOnly(groups string) bool {
	found := groupName.FindAllStringSubmatch(groups, -1)
	if len(found) == 0 {
		return false
	}

	for _, g := range found {
		if name := g[1] + g[2]; name != "development" && name != "test" {
			return false
		}
	}

	return true
}
