package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/model"
)

// resolveModel turns --model as typed into what the agent starts on, and
// the line to print about it, if any. A name that is no model falls back
// to claude's own default rather than refuse: the caller wanted the task
// started, and a bad model would only fail at the agent's first prompt.
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

// agentArgs is the agent's command line: the model, and the storage root
// when it sits outside the worktree, so writing plans and findings there
// asks no permission. plansDir is the shared plans dir, "" for a worktree
// keeping its own .f10.
func agentArgs(m, plansDir, worktree string) []string {
	args := modelArgs(m)
	if plansDir == "" {
		return args
	}

	storage := filepath.Dir(plansDir)
	if rel, err := filepath.Rel(worktree, storage); err == nil && !strings.HasPrefix(rel, "..") {
		return args
	}

	return append(args, "--add-dir", storage)
}
