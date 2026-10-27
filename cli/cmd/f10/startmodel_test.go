package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeDefaultModel(t *testing.T) {
	dir := t.TempDir()

	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}

		return p
	}

	cases := []struct {
		name, path, want string
	}{
		{"key", write("key.json", `{"model": "opus", "theme": "dark"}`), "opus"},
		{"no key", write("nokey.json", `{"theme": "dark"}`), ""},
		{"not json", write("bad.json", `model: opus`), ""},
		{"no file", filepath.Join(dir, "missing.json"), ""},
		{"no path", "", ""},
	}

	for _, c := range cases {
		if got := claudeDefaultModel(c.path); got != c.want {
			t.Errorf("%s: claudeDefaultModel = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestResolveModel(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"model": "opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		raw, settings, model, line string
	}{
		{raw: "", settings: settings},
		{raw: "fable", settings: settings, model: "fable"},
		{raw: "fabel", settings: settings, model: "fable", line: "--model fabel read as fable"},
		{
			raw:      "xyz",
			settings: settings,
			line:     "!!! --model xyz is not a model name f10 recognizes: the agent starts on opus (your claude default) instead",
		},
		{
			raw:  "xyz",
			line: "!!! --model xyz is not a model name f10 recognizes: the agent starts on claude's default model instead",
		},
	}

	for _, c := range cases {
		m, line := resolveModel(c.raw, c.settings)
		if m != c.model || !strings.Contains(line, c.line) || (c.line == "" && line != "") {
			t.Errorf("resolveModel(%q) = %q, %q; want %q, %q", c.raw, m, line, c.model, c.line)
		}
	}
}

func TestAgentArgs(t *testing.T) {
	dirs := func(root string) string {
		var out []string
		for _, d := range taskFileDirs {
			out = append(out, "--add-dir "+root+"/"+d)
		}

		return strings.Join(out, " ")
	}

	cases := []struct {
		name, model, plansDir, worktree string
		want                            string
	}{
		{
			name: "worktree beside main", plansDir: "/code/repo/.f10/plans", worktree: "/code/repo.GH-1",
			want: dirs("/code/repo/.f10"),
		},
		{
			name: "with a model", model: "opus", plansDir: "/code/repo/.f10/plans", worktree: "/code/repo.GH-1",
			want: "--model opus " + dirs("/code/repo/.f10"),
		},
		{
			name: "out of tree", plansDir: "/home/me/.f10/repo/plans", worktree: "/code/repo.GH-1",
			want: dirs("/home/me/.f10/repo"),
		},
		{name: "main itself", plansDir: "/code/repo/.f10/plans", worktree: "/code/repo"},
		{name: "no shared plans dir", worktree: "/code/repo.GH-1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(agentArgs(c.model, c.plansDir, c.worktree), " "); got != c.want {
				t.Errorf("agentArgs = %q, want %q", got, c.want)
			}

			if strings.Contains(strings.Join(agentArgs(c.model, c.plansDir, c.worktree), " "), "instructions") {
				t.Error("the instructions dir was added")
			}
		})
	}
}
