package layout

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// f10-load-conventions.sh walks a fixed list, not a glob, so a file added to
// conventions/ without a list entry would never load. This pins the two.
func TestConventionsListMatchesDir(t *testing.T) {
	bin := filepath.Dir(scriptPath(t))

	out, err := exec.Command(filepath.Join(bin, "f10-load-conventions.sh")).Output()
	if err != nil {
		t.Fatalf("f10-load-conventions.sh: %v", err)
	}

	var carried []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "carries: "); ok {
			carried = strings.Fields(rest)
		}
	}

	if strings.Contains(string(out), "MISSING - ") {
		t.Errorf("f10-load-conventions.sh names a file that does not exist:\n%s", out)
	}

	files, err := filepath.Glob(filepath.Join(bin, "..", "conventions", "*.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var onDisk []string
	for _, f := range files {
		onDisk = append(onDisk, strings.TrimSuffix(filepath.Base(f), ".md"))
	}

	slices.Sort(onDisk)
	listed := slices.Sorted(slices.Values(carried))

	if !slices.Equal(listed, onDisk) {
		t.Errorf("f10-load-conventions.sh carries %v, conventions/ holds %v", listed, onDisk)
	}
}

// f10-inject.sh tells the model to run f10-load-conventions.sh: on a session's first f10
// skill, typed or model-invoked, and after compaction. This pins its wiring:
// one synchronous entry on each of those events, since an async one would
// arrive a turn late.
func TestInjectHooks(t *testing.T) {
	bin := filepath.Dir(scriptPath(t))

	raw, err := os.ReadFile(filepath.Join(bin, "..", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("hooks.json: %v", err)
	}

	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}

	const want = `"${CLAUDE_PLUGIN_ROOT}"/bin/f10-inject.sh`

	for _, event := range []string{"UserPromptExpansion", "PreToolUse", "SessionStart"} {
		n := 0
		for _, group := range cfg.Hooks[event] {
			for _, h := range group.Hooks {
				if h.Command != want {
					continue
				}

				n++
				if h.Async {
					t.Errorf("%s: f10-inject.sh is async; its context would arrive a turn late", event)
				}
			}
		}

		if n != 1 {
			t.Errorf("%s: %d f10-inject.sh entries, want 1", event, n)
		}
	}
}

// Every skill that runs a step reads the conventions, so it must be in both
// f10-inject.sh's list and its UserPromptExpansion matcher. This pins the two
// to skills/, so a skill added in one place only fails here.
func TestInjectSkillsMatchDir(t *testing.T) {
	bin := filepath.Dir(scriptPath(t))

	script, err := os.ReadFile(filepath.Join(bin, "f10-inject.sh"))
	if err != nil {
		t.Fatalf("f10-inject.sh: %v", err)
	}

	var listed []string
	for line := range strings.SplitSeq(string(script), "\n") {
		if rest, ok := strings.CutPrefix(line, `skills="`); ok {
			listed = strings.Fields(strings.TrimSuffix(rest, `"`))
		}
	}

	raw, err := os.ReadFile(filepath.Join(bin, "..", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("hooks.json: %v", err)
	}

	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}

	var matched []string
	for _, group := range cfg.Hooks["UserPromptExpansion"] {
		for _, h := range group.Hooks {
			if strings.HasSuffix(h.Command, "/bin/f10-inject.sh") {
				inner := strings.TrimSuffix(strings.TrimPrefix(group.Matcher, "f10:("), ")")
				matched = strings.Split(inner, "|")
			}
		}
	}

	dirs, err := filepath.Glob(filepath.Join(bin, "..", "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	// these run the f10 binary and no step
	binaryOnly := []string{"asks", "board", "drive", "finish", "read", "start", "status"}

	var onDisk []string
	for _, d := range dirs {
		if name := filepath.Base(filepath.Dir(d)); !slices.Contains(binaryOnly, name) {
			onDisk = append(onDisk, name)
		}
	}

	slices.Sort(listed)
	slices.Sort(matched)
	slices.Sort(onDisk)

	if !slices.Equal(listed, onDisk) {
		t.Errorf("f10-inject.sh lists %v, skills/ holds %v", listed, onDisk)
	}

	if !slices.Equal(matched, onDisk) {
		t.Errorf("hooks.json matches %v, skills/ holds %v", matched, onDisk)
	}
}
