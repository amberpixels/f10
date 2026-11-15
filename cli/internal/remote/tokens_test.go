package remote

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A token's secret is shown once and never stored; revoking it refuses the
// next call without a restart.
func TestTokenStoredHashedAndRevocable(t *testing.T) {
	t.Parallel()

	s := NewStore(t.TempDir())

	secret, err := s.Add("phone", []Scope{ScopeRead, ScopeAnswer}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(secret, tokenPrefix) {
		t.Errorf("secret %q lacks the %s prefix", secret, tokenPrefix)
	}

	raw, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), secret) {
		t.Fatal("the tokens file holds the secret in clear")
	}

	if fi, _ := os.Stat(s.Path); fi.Mode().Perm() != 0o600 {
		t.Errorf("tokens file mode = %v, want 0600", fi.Mode().Perm())
	}

	tok, ok := s.Verify(secret)
	if !ok || tok.Name != "phone" || !tok.Has(ScopeAnswer) || tok.Has(ScopeStart) {
		t.Fatalf("Verify = %+v, %v; want phone with read and answer", tok, ok)
	}

	if _, ok := s.Verify(secret + "x"); ok {
		t.Error("a wrong secret verified")
	}

	if _, err := s.Add("phone", []Scope{ScopeRead}, time.Now()); err == nil {
		t.Error("a second token named phone was issued")
	}

	if ok, err := s.Revoke("phone"); !ok || err != nil {
		t.Fatalf("Revoke = %v, %v", ok, err)
	}

	if _, ok := s.Verify(secret); ok {
		t.Error("a revoked token still verifies")
	}

	if ok, _ := s.Revoke("phone"); ok {
		t.Error("revoking a missing token reported one revoked")
	}
}

func TestTokenNameAndScopesChecked(t *testing.T) {
	t.Parallel()

	s := NewStore(t.TempDir())

	if _, err := s.Add("my phone", []Scope{ScopeRead}, time.Now()); err == nil {
		t.Error("a name with a space was accepted")
	}

	if _, err := ParseScopes([]string{"read,admin"}); err == nil || !strings.Contains(err.Error(), "read, answer") {
		t.Errorf("ParseScopes(admin) = %v, want an error listing the scopes", err)
	}

	got, err := ParseScopes([]string{"read, answer", "read", "finish"})
	if err != nil || len(got) != 3 {
		t.Errorf("ParseScopes = %v, %v; want read, answer, finish once each", got, err)
	}
}
