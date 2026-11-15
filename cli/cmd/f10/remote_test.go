package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A token prints once, lists by name and scopes without its secret, and
// revokes by name.
func TestRemoteTokenLifecycle(t *testing.T) {
	home := testkit.IsolateEnv(t)
	t.Setenv("F10_USER_DIR", filepath.Join(home, ".f10"))

	out, errOut, err := runApp(t, "remote", "token", "add", "phone", "--scope", "read,answer").streams()
	if err != nil {
		t.Fatal(err)
	}

	secret := strings.TrimSpace(out)
	if !strings.HasPrefix(secret, "f10_") || !strings.Contains(errOut, "shown only this once") {
		t.Fatalf("token add printed %q / %q", out, errOut)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".f10", "remote", "tokens.json"))
	if err != nil || strings.Contains(string(raw), secret) {
		t.Fatalf("tokens file: %v; holds the secret: %v", err, strings.Contains(string(raw), secret))
	}

	list := runApp(t, "remote", "token", "list").must(t)
	if !strings.Contains(list, "phone") || !strings.Contains(list, "read,answer") || strings.Contains(list, secret) {
		t.Errorf("token list = %q", list)
	}

	if _, err := runApp(t, "remote", "token", "add", "tablet", "--scope", "admin").outErr(); err == nil ||
		!strings.Contains(err.Error(), "read, answer, steer, start, finish") {
		t.Errorf("an unknown scope = %v, want the valid ones named", err)
	}

	if got := runApp(t, "remote", "token", "revoke", "phone").must(t); !strings.Contains(got, "revoked") {
		t.Errorf("revoke = %q", got)
	}

	if got := runApp(t, "remote", "token", "list").must(t); !strings.Contains(got, "no device tokens") {
		t.Errorf("list after revoke = %q", got)
	}
}
