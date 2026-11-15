package mcp

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/remote"
	"github.com/amberpixels/f10/cli/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestResolveScopes(t *testing.T) {
	if _, _, err := resolve([]string{"read"}, "f10_x"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("--scope with --token = %v, want refused", err)
	}

	if _, _, err := resolve(
		[]string{"read,admin"},
		"",
	); err == nil ||
		!strings.Contains(err.Error(), remote.ScopeList()) {
		t.Errorf("an unknown scope = %v, want an error naming the valid ones", err)
	}

	scopes, _, err := resolve(nil, "")
	if err != nil || !slices.Equal(scopes, []remote.Scope{remote.ScopeRead, remote.ScopeAnswer}) {
		t.Errorf("no flag = %v %v, want read and answer", scopes, err)
	}

	scopes, _, err = resolve([]string{"read", "start"}, "")
	if err != nil || !slices.Equal(scopes, []remote.Scope{remote.ScopeRead, remote.ScopeStart}) {
		t.Errorf("--scope read --scope start = %v %v", scopes, err)
	}
}

func TestResolveToken(t *testing.T) {
	dir, err := remote.Dir()
	if err != nil {
		t.Fatal(err)
	}

	store := remote.NewStore(dir)

	secret, err := store.Add("desktop", []remote.Scope{remote.ScopeRead, remote.ScopeSteer}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := resolve(nil, "f10_unknown"); err == nil || !strings.Contains(err.Error(), "unknown or revoked") {
		t.Errorf("an unknown token = %v, want refused", err)
	}

	scopes, check, err := resolve(nil, secret)
	if err != nil || !slices.Equal(scopes, []remote.Scope{remote.ScopeRead, remote.ScopeSteer}) || check() != nil {
		t.Fatalf("a token = %v %v, want its scopes", scopes, err)
	}

	if _, err := store.Revoke("desktop"); err != nil {
		t.Fatal(err)
	}

	if err := check(); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Errorf("a call after the revoke = %v, want refused", err)
	}
}
