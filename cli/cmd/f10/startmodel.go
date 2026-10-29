package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/model"
)

// resolveModel turns --model as typed into what the agent starts on, and a
// line to print about it. A name that is no model falls back to claude's
// default rather than refuse: a bad model would only fail at the first prompt.
func resolveModel(raw, settings string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", ""
	}

	value, note, ok := model.Resolve(raw)
	if ok {
		return value, note
	}

	fallback := "claude's default model"
	if d := claudeDefaultModel(settings); d != "" {
		fallback = d + " (your claude default)"
	}

	warning := "!!! --model " + strings.TrimSpace(raw) +
		" is not a model name f10 recognizes: the agent starts on " + fallback + " instead"

	return "", lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")).Render(warning)
}

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

// modelArgs is the agent's command line for a resolved model.
func modelArgs(m string) []string {
	if m == "" {
		return nil
	}

	return []string{"--model", m}
}

// taskFileDirs are the storage root's dirs an agent writes task files to.
// Instructions and the driver stay out: a worktree's agent never edits
// what every checkout's next run loads.
var taskFileDirs = []string{"plans", "reviews", "verdicts", "spikes", "lore", "images"}

// agentArgs is the agent's command line: the model, and each task-file dir
// of the storage root when that root sits outside the worktree, so writing
// plans and findings there asks no permission.
func agentArgs(m, plansDir, worktree string) []string {
	args := modelArgs(m)
	if !outsideWorktree(plansDir, worktree) {
		return args
	}

	for _, d := range taskFileDirs {
		args = append(args, "--add-dir", filepath.Join(filepath.Dir(plansDir), d))
	}

	return args
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
