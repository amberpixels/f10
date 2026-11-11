package main

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
)

// Dates the git fixtures commit at: before a period, during it, and after.
const (
	before = "2026-10-10T10:00:00Z"
	during = "2026-10-14T10:00:00Z"
	later  = "2026-10-14T12:00:00Z"
)

// gitRepo is the shared kit's repo, a local type so tests here can add
// helpers of their own to it.
type gitRepo struct{ gitxtest.Repo }

// initRepo makes dir a repo on branch, with the default fixture identity.
func initRepo(t *testing.T, dir, branch string) gitRepo {
	t.Helper()

	return gitRepo{gitxtest.Init(t, dir, branch)}
}

// cloneRepo is a clone named name of an empty bare origin whose default
// branch is main; the caller commits and pushes the first history.
func cloneRepo(t *testing.T, name string) gitRepo {
	t.Helper()

	return gitRepo{gitxtest.Clone(t, name)}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	return gitxtest.Run(t, dir, args...)
}
