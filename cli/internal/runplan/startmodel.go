package runplan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
)

// ClaudeSettingsPath is the user's claude settings file, under
// CLAUDE_CONFIG_DIR when set.
func ClaudeSettingsPath() string {
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

// ClaudeDefaultModel is the "model" key of the settings file at path, or
// "" when the file, the key or the path is missing. It only names the
// fallback in a warning, so nothing it fails on is an error.
func ClaudeDefaultModel(path string) string {
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

// RecordedProfile is the profile a branch records under f10-agent, the
// zero profile when it records none.
func RecordedProfile(ctx context.Context, main, branch string) (agentctl.Profile, error) {
	v, err := branchmeta.Get(ctx, main, branch, branchmeta.Agent)
	if err != nil {
		return agentctl.Profile{}, fmt.Errorf("reading the recorded agent profile: %w", err)
	}

	if v == "" {
		return agentctl.Profile{}, nil
	}

	p, _, _ := agentctl.ParseRecorded(v).Resolve()

	return p, nil
}
