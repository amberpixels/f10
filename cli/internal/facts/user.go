package facts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/frontmatter"
)

// The user's own files are the first layer under every project's, so a key
// a project sets wins over the user's, and the user's over the built-ins.
const (
	UserAgentsFile = "agents.md" // the user's default agent profiles; f10 only reads it
	UserPacesFile  = "paces.md"  // the user's paces and default pace; f10 only reads it
)

type userAgents struct {
	Agents map[string]string `yaml:"agents"`
}

type userPaces struct {
	Paces   map[string]Pace `yaml:"paces"`
	Pace    string          `yaml:"pace"`
	Ceiling any             `yaml:"ceiling"` // refused: a cap is the project's to set
}

// mergeUser lays the user's files in dir under every project layer. A file
// with a problem adds nothing, since a run must not go on a half-read map;
// "" is no user layer.
func (l *layered) mergeUser(dir string) []Problem {
	if dir == "" {
		return nil
	}

	var problems []Problem

	fail := func(format string, args ...any) {
		problems = append(problems, Problem{Error: true, Message: fmt.Sprintf(format, args...)})
	}

	var agents userAgents
	if path, ok, err := ReadUserFile(dir, UserAgentsFile, &agents); err != nil {
		fail("%v", err)
	} else if ok {
		if err := checkUserAgents(agents.Agents); err != nil {
			fail("%s: %v", path, err)
		} else {
			l.mergeUserLayer(Declared{Agents: agents.Agents}, path)
		}
	}

	var paces userPaces
	if path, ok, err := ReadUserFile(dir, UserPacesFile, &paces); err != nil {
		fail("%v", err)
	} else if ok {
		if bad := checkUserPaces(paces); len(bad) > 0 {
			fail("%s: %s", path, strings.Join(bad, "; "))
		} else {
			l.mergeUserLayer(Declared{Paces: paces.Paces, Pace: paces.Pace}, path)
		}
	}

	return problems
}

func (l *layered) mergeUserLayer(d Declared, path string) {
	if l.user == nil {
		l.user = map[string]string{}
	}

	l.user[path] = path
	l.merge(d, path)
}

// ReadUserFile decodes the frontmatter of the user's file in dir into cfg,
// refusing an unknown key as project.md does; ok is false for a missing
// file or one with no frontmatter, which leaves cfg as it was.
func ReadUserFile(dir, file string, cfg any) (string, bool, error) {
	path := filepath.Join(dir, file)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, false, nil
	}

	if err != nil {
		return path, false, err
	}

	block, _, ok := frontmatter.Split(data)
	if !ok {
		return path, false, nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(block))
	dec.KnownFields(true)

	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return path, false, fmt.Errorf("%s: %w", path, err)
	}

	return path, true, nil
}

// checkUserAgents is the first key or profile the vocabulary refuses. The
// user's file serves every project, so a key may name a step no flow has.
func checkUserAgents(agents map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(agents)) {
		if err := CheckAgentKey(key, nil); err != nil {
			return err
		}

		if _, err := agentctl.ParseProfile(agents[key]); err != nil {
			return fmt.Errorf("agents.%s: %w", key, err)
		}
	}

	return nil
}

func checkUserPaces(cfg userPaces) []string {
	var bad []string
	if cfg.Ceiling != nil {
		bad = append(bad, "ceiling: a ceiling is the project's, declared in project.md; here it would just be a pace")
	}

	for _, name := range slices.Sorted(maps.Keys(cfg.Paces)) {
		bad = append(bad, CheckPace(name, cfg.Paces[name])...)
	}

	return bad
}
