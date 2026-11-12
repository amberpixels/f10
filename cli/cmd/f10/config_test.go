package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// The merged agents map shows one row per key, the user's file under the
// project's, each with its own origin.
func TestConfigShowsAgentsPerKey(t *testing.T) {
	repo := paceRepo(t, "flows:\n  default: plan review implement pr\nagents:\n  review: sonnet:low\n", "")
	user := os.Getenv(storage.UserDirEnv)
	if err := os.MkdirAll(user, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(user, facts.UserAgentsFile),
		[]byte("---\nagents:\n  default: opus:high\n  review: opus:max\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var v configcmd.View
	if err := json.Unmarshal([]byte(runApp(t, "-C", repo, "config", "--json").must(t)), &v); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, f := range v.Facts {
		if strings.HasPrefix(f.Key, "agents") {
			got[f.Key] = f.Value + " @ " + f.Origin.String()
		}
	}

	want := map[string]string{
		"agents.default": "opus:high @ declared (user, " + filepath.Join(user, facts.UserAgentsFile) + ")",
		"agents.review":  "sonnet:low @ declared (main, frontmatter)",
	}

	if !maps.Equal(got, want) || len(v.Agents) != 2 {
		t.Errorf("facts %v, agents %v; want %v", got, v.Agents, want)
	}

	bad := []byte("---\nagents:\n  review: opus:huge\n---\n")
	if err := os.WriteFile(filepath.Join(user, facts.UserAgentsFile), bad, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal([]byte(runApp(t, "-C", repo, "config", "--json").must(t)), &v); err != nil {
		t.Fatal(err)
	}

	if !slices.ContainsFunc(v.Problems, func(p facts.Problem) bool {
		return p.Error && strings.Contains(p.Message, "agents.review") && strings.Contains(p.Message, "agents.md")
	}) {
		t.Errorf("a refused user file is a problem: %+v", v.Problems)
	}
}
