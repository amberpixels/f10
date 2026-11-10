package main

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/stack"
)

// tipFixture is a repo whose main and origin/main point at the commits a
// case names: base, then ahead and behind on two sides of it.
type tipFixture struct {
	repo                gitRepo
	base, local, remote string
}

func newTipFixture(t *testing.T) tipFixture {
	t.Helper()

	r := initRepo(t, t.TempDir(), "main")
	r.commit("2026-01-01T00:00:00Z", "a", "a", "base")
	base := strings.TrimSpace(git(t, r.dir, "rev-parse", "HEAD"))
	r.commit("2026-01-02T00:00:00Z", "b", "b", "local")
	local := strings.TrimSpace(git(t, r.dir, "rev-parse", "HEAD"))
	r.git("checkout", "-q", "-b", "side", base)
	r.commit("2026-01-03T00:00:00Z", "c", "c", "remote")
	remote := strings.TrimSpace(git(t, r.dir, "rev-parse", "HEAD"))
	r.git("checkout", "-q", "main")

	return tipFixture{r, base, local, remote}
}

func (f tipFixture) point(local, remote string) {
	if local == "" {
		f.repo.git("checkout", "-q", "--detach")
		f.repo.git("update-ref", "-d", "refs/heads/main")
	} else {
		f.repo.git("update-ref", "refs/heads/main", local)
	}

	if remote == "" {
		f.repo.git("update-ref", "-d", "refs/remotes/origin/main")
	} else {
		f.repo.git("update-ref", "refs/remotes/origin/main", remote)
	}
}

// The six cases where finish's landing base and catchup's landed target
// read the default branch: they agree except on divergence and a missing
// local branch, where finish refuses.
func TestDefaultTipReaders(t *testing.T) {
	cases := []struct {
		name          string
		local, remote func(tipFixture) string
		land          string // landingBase: "local", "remote" or "error"
		target        string // defaultTargetOf: "main" or "origin/main" or "error"
	}{
		{"equal", tipBase, tipBase, "local", "main"},
		{"origin missing", tipBase, tipNone, "local", "main"},
		{"local missing", tipNone, tipBase, "error", "origin/main"},
		{"local behind", tipBase, tipLocal, "remote", "origin/main"},
		{"local ahead", tipLocal, tipBase, "local", "main"},
		{"diverged", tipLocal, tipRemote, "error", "origin/main"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newTipFixture(t)
			local, remote := c.local(f), c.remote(f)
			f.point(local, remote)

			sha, err := finish.LandingBase(t.Context(), f.repo.dir, "main")

			switch c.land {
			case "error":
				if err == nil {
					t.Errorf("landingBase = %s, want an error", sha)
				}
			case "local":
				if err != nil || sha != local {
					t.Errorf("landingBase = %s, %v; want local %s", sha, err, local)
				}
			case "remote":
				if err != nil || sha != remote {
					t.Errorf("landingBase = %s, %v; want origin %s", sha, err, remote)
				}
			}

			sha, name, err := stack.DefaultTargetOf(t.Context(), f.repo.dir, "main")
			want := map[string]string{"main": local, "origin/main": remote}[c.target]

			if err != nil || name != c.target || sha != want {
				t.Errorf("defaultTargetOf = %s %s, %v; want %s %s", sha, name, err, want, c.target)
			}
		})
	}
}

func tipBase(f tipFixture) string   { return f.base }
func tipLocal(f tipFixture) string  { return f.local }
func tipRemote(f tipFixture) string { return f.remote }
func tipNone(tipFixture) string     { return "" }
