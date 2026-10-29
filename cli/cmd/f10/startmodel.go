package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// claudeSettingsPath is the user's claude settings file, under
// CLAUDE_CONFIG_DIR when set.
func claudeSettingsPath() string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}

		dir = filepath.Join(home, ".claude")
	}

	return filepath.Join(dir, "settings.json")
}

// claudeDefaultModel is the "model" key of the settings file at path, or
// "" when the file, the key or the path is missing. It only names the
// fallback in a warning, so nothing it fails on is an error.
func claudeDefaultModel(path string) string {
	if path == "" {
		return ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var settings struct {
		Model string `json:"model"`
	}

	if json.Unmarshal(data, &settings) != nil {
		return ""
	}

	return strings.TrimSpace(settings.Model)
}

// taskFileDirs are the storage root's dirs an agent writes task files to.
// Instructions and the driver stay out: a worktree's agent never edits
// what every checkout's next run loads.
var taskFileDirs = []string{"plans", "reviews", "verdicts", "spikes", "lore", "images"}

// agentArgs is the agent's command line: the profile's flags through its
// kind, and each task-file dir of the storage root when that root sits
// outside the worktree, so writing plans and findings there asks no
// permission.
func agentArgs(p agentctl.Profile, plansDir, worktree string) []string {
	args := p.Args()
	if !outsideWorktree(plansDir, worktree) {
		return args
	}

	for _, d := range taskFileDirs {
		args = append(args, "--add-dir", filepath.Join(filepath.Dir(plansDir), d))
	}

	return args
}

// recordedProfile is the profile a branch records, from f10-agent, else
// the model alone an older branch recorded under f10-model; the zero
// profile when it records neither.
func recordedProfile(ctx context.Context, main, branch string) (agentctl.Profile, error) {
	for _, key := range []string{cfgAgent, cfgModel} {
		v, err := gitx.Config(ctx, main, "branch."+branch+"."+key)
		if err != nil {
			return agentctl.Profile{}, fmt.Errorf("reading the recorded agent profile: %w", err)
		}

		if v != "" {
			p, _, _ := agentctl.ParseRecorded(v).Resolve()

			return p, nil
		}
	}

	return agentctl.Profile{}, nil
}

// makeStorageDirs creates the task-file dirs agentArgs names, since claude
// may refuse --add-dir on a missing one.
func makeStorageDirs(plansDir string) error {
	if plansDir == "" {
		return nil
	}

	for _, d := range taskFileDirs {
		if err := os.MkdirAll(filepath.Join(filepath.Dir(plansDir), d), 0o750); err != nil {
			return fmt.Errorf("creating the storage dirs: %w", err)
		}
	}

	return nil
}

// outsideWorktree says the storage root of plansDir lies outside worktree.
func outsideWorktree(plansDir, worktree string) bool {
	if plansDir == "" {
		return false
	}

	rel, err := filepath.Rel(worktree, filepath.Dir(plansDir))

	return err != nil || strings.HasPrefix(rel, "..")
}
