package main

import (
	"bytes"
	"encoding/json"
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

	"github.com/amberpixels/f10/cli/internal/frontmatter"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// A standup's period and a board's scope are the person's, not a checkout's,
// so these files live in ~/.f10/, with an extension no project directory
// there carries.
const (
	standupStateFile  = "standup.json" // the last reported standup; f10 writes it
	standupConfigFile = "standup.md"   // the user's standup settings; f10 only reads it
	boardConfigFile   = "board.md"     // the user's board settings; f10 only reads it
)

func userF10Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding your home directory: %w", err)
	}

	return filepath.Join(home, ".f10"), nil
}

// readLastStandup is nil when none was reported; a file that does not read
// is an error, since dropping it would quietly move the period.
func readLastStandup() (*standup.Last, error) {
	dir, err := userF10Dir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, standupStateFile)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var last standup.Last
	if err := json.Unmarshal(data, &last); err != nil {
		return nil, fmt.Errorf("%s does not read as a reported standup (delete it to start over): %w", path, err)
	}

	if last.At.IsZero() || last.Since.IsZero() {
		return nil, fmt.Errorf("%s has no at or since (delete it to start over)", path)
	}

	return &last, nil
}

// writeLastStandup replaces the stored standup through a rename, so a
// reader never sees half a file.
func writeLastStandup(last standup.Last) error {
	dir, err := userF10Dir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	data, err := json.MarshalIndent(last, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".standup-*.json")
	if err != nil {
		return err
	}

	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), filepath.Join(dir, standupStateFile))
}

// standupConfig is the frontmatter of ~/.f10/standup.md. Unknown keys are
// refused, as project.md's are; the prose below the block is the skill's.
type standupConfig struct {
	Workdays []string            `yaml:"workdays"`
	Groups   map[string][]string `yaml:"groups"` // a name to the paths and globs of its checkouts
}

type boardConfig struct {
	Groups map[string][]string `yaml:"groups"`
	Repos  []repoEntry         `yaml:"repos"`
	People []person            `yaml:"people"`
}

type person struct {
	Name    string   `yaml:"name"`
	Tracker string   `yaml:"tracker"`
	Host    string   `yaml:"host"`
	Emails  []string `yaml:"emails"`
}

// A repoEntry is a string, `github.com/acme/widgets`, or a map adding the
// task id `prefix`, for a name that derives the wrong one.
type repoEntry struct {
	Repo   string `yaml:"repo"`
	Prefix string `yaml:"prefix"`
}

func (r *repoEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		r.Repo = node.Value

		return nil
	}

	type plain repoEntry

	return node.Decode((*plain)(r))
}

// readUserConfig leaves cfg as it was for a missing file or one with no
// frontmatter. Each caller reads only the key it needs, so a broken file
// fails only a run that uses it.
func readUserConfig(file string, cfg any) (string, error) {
	dir, err := userF10Dir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(dir, file)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}

	if err != nil {
		return path, err
	}

	block, _, ok := frontmatter.Split(data)
	if !ok {
		return path, nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(block))
	dec.KnownFields(true)

	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return path, fmt.Errorf("%s: %w", path, err)
	}

	return path, nil
}

func readStandupConfig() (standupConfig, string, error) {
	var cfg standupConfig

	path, err := readUserConfig(standupConfigFile, &cfg)

	return cfg, path, err
}

// readBoardConfig is ~/.f10/board.md, with the people every run reads
// checked; a repository entry is read only by the --all run that uses it.
func readBoardConfig() (boardConfig, string, error) {
	var cfg boardConfig

	path, err := readUserConfig(boardConfigFile, &cfg)
	if err != nil {
		return cfg, path, err
	}

	for i, p := range cfg.People {
		if strings.TrimSpace(p.Name) == "" {
			return cfg, path, fmt.Errorf("%s: people[%d] has no name", path, i)
		}
	}

	return cfg, path, nil
}

var weekdays = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// readWorkdays is Monday to Friday when standup.md is missing or does not say.
func readWorkdays() (standup.Workdays, error) {
	cfg, path, err := readStandupConfig()
	if err != nil {
		return standup.Workdays{}, err
	}

	if cfg.Workdays == nil {
		return standup.DefaultWorkdays(), nil
	}

	var w standup.Workdays

	for _, name := range cfg.Workdays {
		d, ok := weekdays[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return standup.Workdays{}, fmt.Errorf(
				"%s: workdays: %q is not a day (mon, tue, wed, thu, fri, sat, sun)",
				path,
				name,
			)
		}

		w[d] = true
	}

	if w == (standup.Workdays{}) {
		return standup.Workdays{}, fmt.Errorf("%s: workdays names no day", path)
	}

	return w, nil
}

// readGroups: a glob keeps the checkouts it matches and skips the rest; a
// plain path that is not a checkout is an error, since the user named it.
// The user declared each entry, so this is not the walk F10_ROOTS fences.
func readGroups(file string, names []string) ([]scopeItem, error) {
	groups, path, err := userGroups(file)
	if err != nil {
		return nil, err
	}

	var items []scopeItem

	for _, name := range names {
		entries, ok := groups[name]
		if !ok {
			declared := slices.Sorted(maps.Keys(groups))
			if len(declared) == 0 {
				return nil, fmt.Errorf("no group %q: %s declares no groups (see docs/cli.md)", name, path)
			}

			return nil, fmt.Errorf("no group %q in %s (declared: %s)", name, path, strings.Join(declared, ", "))
		}

		for _, entry := range entries {
			found, err := groupEntry(entry)
			if err != nil {
				return nil, fmt.Errorf("%s: group %s: %w", path, name, err)
			}

			items = append(items, found...)
		}
	}

	return items, nil
}

// userGroups is the groups one user file declares, read with that file's
// own keys so an unknown one is still refused.
func userGroups(file string) (map[string][]string, string, error) {
	if file == boardConfigFile {
		cfg, path, err := readBoardConfig()

		return cfg.Groups, path, err
	}

	cfg, path, err := readStandupConfig()

	return cfg.Groups, path, err
}

func groupEntry(entry string) ([]scopeItem, error) {
	entry = strings.TrimSpace(entry)

	if isRepoEntry(entry) {
		r, err := parseRepo(entry, "")
		if err != nil {
			return nil, err
		}

		return []scopeItem{{repo: &r}}, nil
	}

	p, err := expandHome(entry)
	if err != nil {
		return nil, err
	}

	if !filepath.IsAbs(p) {
		return nil, fmt.Errorf("%q is not an absolute path (start it with / or ~/)", entry)
	}

	if !strings.ContainsAny(p, "*?[") {
		if !projects.IsCheckout(p) {
			return nil, fmt.Errorf("%s is not a git checkout", p)
		}

		return []scopeItem{{dir: p}}, nil
	}

	matches, err := filepath.Glob(p)
	if err != nil {
		return nil, fmt.Errorf("bad glob %q: %w", entry, err)
	}

	var out []scopeItem

	for _, m := range matches {
		if projects.IsCheckout(m) {
			out = append(out, scopeItem{dir: m})
		}
	}

	return out, nil
}
