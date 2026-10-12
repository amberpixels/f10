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
