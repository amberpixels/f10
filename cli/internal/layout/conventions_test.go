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

// f10-rules.sh walks a fixed list, not a glob, so a file added to
// conventions/ without a list entry would never load. This pins the two.
func TestConventionsListMatchesDir(t *testing.T) {
	bin := filepath.Dir(scriptPath(t))

	out, err := exec.Command(filepath.Join(bin, "f10-rules.sh")).Output()
	if err != nil {
		t.Fatalf("f10-rules.sh: %v", err)
	}

	var carried []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "carries: "); ok {
			carried = strings.Fields(rest)
		}
	}

	if strings.Contains(string(out), "MISSING - ") {
		t.Errorf("f10-rules.sh names a file that does not exist:\n%s", out)
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
		t.Errorf("f10-rules.sh carries %v, conventions/ holds %v", listed, onDisk)
	}
}

// f10-inject.sh tells the model to run f10-rules.sh: on a session's first f10
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
