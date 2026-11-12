package testkit

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
)

// Dates the git fixtures commit at: before a period, during it, and after.
const (
	Before = "2026-10-10T10:00:00Z"
	During = "2026-10-14T10:00:00Z"
	Later  = "2026-10-14T12:00:00Z"
)

// GitRepo is gitxtest's repo with the helpers this module's tests add to it.
type GitRepo struct{ gitxtest.Repo }

// InitRepo makes dir a repo on branch, with the default fixture identity.
func InitRepo(t *testing.T, dir, branch string) GitRepo {
	t.Helper()

	return GitRepo{gitxtest.Init(t, dir, branch)}
}

// cloneRepo is a clone named name of an empty bare origin whose default
// branch is main; the caller commits and pushes the first history.
func cloneRepo(t *testing.T, name string) GitRepo {
	t.Helper()

	return GitRepo{gitxtest.Clone(t, name)}
}

func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	return gitxtest.Run(t, dir, args...)
}
