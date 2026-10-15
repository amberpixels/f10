package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/frontmatter"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// A standup's period is the person's, not a checkout's: two scopes run the
// same morning agree. So both files live in ~/.f10/, beside the out-of-tree
// project directories, with an extension no project directory carries.
const (
	standupStateFile  = "standup.json" // the last reported standup; f10 writes it
	standupConfigFile = "standup.md"   // the user's settings; f10 only reads it
)

func userF10Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding your home directory: %w", err)
	}

	return filepath.Join(home, ".f10"), nil
}

// readLastStandup is the stored standup, nil when none was ever reported.
// A file that does not read is an error: dropping it would quietly move
// the period.
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
	Workdays []string `yaml:"workdays"`
}

var weekdays = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// readWorkdays is the working weekdays ~/.f10/standup.md declares, Monday
// to Friday when it is missing or does not say.
func readWorkdays() (standup.Workdays, error) {
	dir, err := userF10Dir()
	if err != nil {
		return standup.Workdays{}, err
	}

	path := filepath.Join(dir, standupConfigFile)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return standup.DefaultWorkdays(), nil
	}

	if err != nil {
		return standup.Workdays{}, err
	}

	block, _, ok := frontmatter.Split(data)
	if !ok {
		return standup.DefaultWorkdays(), nil
	}

	var cfg standupConfig

	dec := yaml.NewDecoder(bytes.NewReader(block))
	dec.KnownFields(true)

	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return standup.Workdays{}, fmt.Errorf("%s: %w", path, err)
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
