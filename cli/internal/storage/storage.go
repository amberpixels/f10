// Package storage owns the layout of the storage root: where plans,
// findings, verdicts and every other task file live under it.
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// DirName is the directory f10 keeps its files in, in a checkout and in the
// user's home.
const DirName = ".f10"

// The task-file dirs, in the order an agent is granted them.
const (
	plans    = "plans"
	reviews  = "reviews"
	verdicts = "verdicts"
	spikes   = "spikes"
	handoffs = "handoffs"
	lore     = "lore"
	images   = "images"
	demo     = "demo"
)

// taskFileDirs is every dir an agent writes task files to. Instructions and
// the driver stay out: a worktree's agent never edits what every checkout's
// next run loads.
var taskFileDirs = []string{plans, reviews, verdicts, spikes, handoffs, lore, images, demo}

// ArchiveDir is the subdir of plans, findings and handoffs that a finished
// or replaced file moves to.
const ArchiveDir = "archive"

// ArchiveOf is the archive subdir of a task-file dir.
func ArchiveOf(dir string) string { return filepath.Join(dir, ArchiveDir) }

// A Root is a storage root: main's .f10 shared by every checkout, or the
// out-of-tree root under ~/.f10. It is never empty outside tests, so its
// path methods panic on an empty one rather than resolve against the cwd.
type Root string

// InRepo is the storage root inside a checkout.
func InRepo(checkout string) Root { return Root(filepath.Join(checkout, DirName)) }

// OutOfTree is the per-project storage root under the user's home.
func OutOfTree(home, project string) Root { return Root(filepath.Join(UserDir(home), project)) }

// UserDir is the user's own f10 dir, holding the ledger, the daemon and
// every out-of-tree project: UserDirEnv when set, so one setting moves all of
// it, else ~/.f10 under home.
func UserDir(home string) string {
	if dir := userDirSet(); dir != "" {
		return dir
	}

	return filepath.Join(home, DirName)
}

func userDirSet() string { return strings.TrimSpace(os.Getenv(UserDirEnv)) }

// Path is the root itself.
func (r Root) Path() string { return r.join() }

// Plans is the dir holding every live plan.
func (r Root) Plans() string { return r.join(plans) }

// Plan is the task's live plan.
func (r Root) Plan(id string) string { return r.join(plans, id+".md") }

// PlanLive reports the task's live plan; an empty root, in tests, has none.
func (r Root) PlanLive(id string) bool { return r != "" && fsx.Exists(r.Plan(id)) }

// PlanArchived reports an archived plan or spike report for the task with
// no live plan beside it, since a re-plan archives the old plan too.
func (r Root) PlanArchived(id string) bool {
	if r == "" || r.PlanLive(id) {
		return false
	}

	if fsx.Exists(filepath.Join(r.Archive(), id+".md")) {
		return true
	}

	older, _ := filepath.Glob(filepath.Join(r.Archive(), id+".*.md"))

	return len(older) > 0
}

// Archive is where finished and replaced plans move.
func (r Root) Archive() string { return ArchiveOf(r.Plans()) }

// Reviews is the findings dir for key; an empty key is the dir holding
// every key's.
func (r Root) Reviews(key string) string { return r.join(reviews, key) }

// Verdicts is the verdict dir for key; an empty key is the dir holding
// every key's.
func (r Root) Verdicts(key string) string { return r.join(verdicts, key) }

// Spikes is the dir holding spike reports.
func (r Root) Spikes() string { return r.join(spikes) }

// Handoffs is the dir holding handoff files.
func (r Root) Handoffs() string { return r.join(handoffs) }

// Lore is the local lore store.
func (r Root) Lore() string { return r.join(lore) }

// Images is where `task read` downloads the task's images.
func (r Root) Images(id string) string { return r.join(images, id) }

// Demo is the task's demo dir.
func (r Root) Demo(id string) string { return r.join(demo, id) }

// Instructions is the project's instructions layer at this root.
func (r Root) Instructions() string { return r.join("instructions") }

// Driver is the tracker driver kept at this root.
func (r Root) Driver() string { return r.join("driver") }

// TaskFileDirs are the dirs an agent writes task files to.
func (r Root) TaskFileDirs() []string {
	out := make([]string, len(taskFileDirs))
	for i, d := range taskFileDirs {
		out[i] = r.join(d)
	}

	return out
}

// Outside says the root lies outside worktree, symlinks resolved.
func (r Root) Outside(worktree string) bool { return !gitx.Inside(r.Path(), worktree) }

func (r Root) join(elem ...string) string {
	if r == "" {
		panic("storage: empty root")
	}

	return filepath.Join(append([]string{string(r)}, elem...)...)
}

// UserDirEnv names another directory than ~/.f10 for the user's files, so
// a run that must not read them (the eval runner, tests) points it elsewhere.
const UserDirEnv = "F10_USER_DIR"

// UserFilesDir is UserDir for this user, which needs no home when
// UserDirEnv is set.
func UserFilesDir() (string, error) {
	if dir := userDirSet(); dir != "" {
		return dir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding your home directory: %w", err)
	}

	return UserDir(home), nil
}
