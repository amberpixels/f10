package layout

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// conventions.sh walks a fixed list, not a glob, so a file added to
// conventions/ without a list entry would never load. This pins the two.
func TestConventionsListMatchesDir(t *testing.T) {
	bin := filepath.Dir(scriptPath(t))

	out, err := exec.Command(filepath.Join(bin, "conventions.sh")).Output()
	if err != nil {
		t.Fatalf("conventions.sh: %v", err)
	}

	var carried []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "carries: "); ok {
			carried = strings.Fields(rest)
		}
	}

	if strings.Contains(string(out), "MISSING - ") {
		t.Errorf("conventions.sh names a file that does not exist:\n%s", out)
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
		t.Errorf("conventions.sh carries %v, conventions/ holds %v", listed, onDisk)
	}
}

// Claude Code caps what one hook adds to the context at 10,000 characters, so
// the conventions travel as chunks, one hook entry each. This pins the three
// facts that keep them whole: every chunk fits with room to grow, the chunks
// concatenate to the full text, and hooks.json carries one entry per chunk on
// each injecting event.
func TestConventionsChunksFitTheHooks(t *testing.T) {
	bin := filepath.Dir(scriptPath(t))
	script := filepath.Join(bin, "conventions.sh")

	run := func(args ...string) string {
		t.Helper()

		out, err := exec.Command(script, args...).Output()
		if err != nil {
			t.Fatalf("conventions.sh %v: %v", args, err)
		}

		return string(out)
	}

	count, err := strconv.Atoi(strings.TrimSpace(run("--count")))
	if err != nil || count < 1 {
		t.Fatalf("conventions.sh --count: %q", run("--count"))
	}

	const limit = 9500 // 10,000 minus headroom for growth

	var joined strings.Builder
	for n := 1; n <= count; n++ {
		chunk := run(strconv.Itoa(n))
		if size := len([]rune(chunk)); size > limit {
			t.Errorf("chunk %d is %d characters, over %d: move a file to another chunk", n, size, limit)
		}

		joined.WriteString(chunk)
	}

	if joined.String() != run() {
		t.Error("the chunks concatenated differ from the full output")
	}

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

	for _, event := range []string{"UserPromptExpansion", "PreToolUse", "SessionStart"} {
		var chunks []int
		for _, group := range cfg.Hooks[event] {
			for _, h := range group.Hooks {
				rest, ok := strings.CutPrefix(h.Command, `"${CLAUDE_PLUGIN_ROOT}"/bin/f10-state.sh inject `)
				if !ok {
					continue
				}

				n, err := strconv.Atoi(rest)
				if err != nil {
					t.Errorf("%s: inject entry %q names no chunk", event, h.Command)
				}

				if h.Async {
					t.Errorf("%s: inject %d is async; its context would arrive a turn late", event, n)
				}

				chunks = append(chunks, n)
			}
		}

		slices.Sort(chunks)

		want := make([]int, 0, count)
		for n := 1; n <= count; n++ {
			want = append(want, n)
		}

		if !slices.Equal(chunks, want) {
			t.Errorf("%s: hooks.json injects chunks %v, conventions.sh has %v", event, chunks, want)
		}
	}
}
