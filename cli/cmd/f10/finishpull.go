package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// mainPullKey is the lease every finish pulling the main checkout takes, so
// two finishes at once (a drive's, the watcher's, the command) never race on
// origin's refs.
const mainPullKey = "main-pull"

// mainPullWait bounds the wait for another finish's pull.
const mainPullWait = 2 * time.Minute

// pullMain brings the local default branch up to the merge. The merge has
// landed on origin by then, so a pull that fails defers to a later retry
// (retryPendingPull) and comes back as a note naming the cause; it never
// rebases or resets the branch.
func pullMain(ctx context.Context, root, main, def, sha string) (string, bool) {
	note, err := pullDefault(ctx, root, main, def, sha)
	if err == nil {
		clearPendingPull(ctx, root, main, def)

		return note, false
	}

	note = deferredPullNote(err, main, def)

	if root == "" {
		return note + "; nothing records the retry here, so pull it by hand", true
	}

	if err := os.WriteFile(filepath.Join(root, pendingPullFile), []byte(def+" "+sha+"\n"), 0o600); err != nil {
		note += fmt.Sprintf("; recording the retry failed (%v), so pull it by hand", err)
	}

	return note, true
}

// errPullBusy is another finish holding the pull's lease past mainPullWait.
var errPullBusy = errors.New("another finish held the pull")

func pullDefault(ctx context.Context, root, main, def, sha string) (string, error) {
	if root != "" {
		lease, err := drives.Await(ctx, root, mainPullKey, drives.Holder{}, mainPullWait)
		if err != nil {
			return "", fmt.Errorf("%w: %w", errPullBusy, err)
		}
		defer lease.Release()
	}

	var note string

	if gitx.CurrentBranch(ctx, main) == def {
		if err := gitx.PullFF(ctx, main); err != nil {
			return "", err
		}
	} else {
		if err := gitx.FetchInto(ctx, main, def); err != nil {
			return "", err
		}

		note = fmt.Sprintf("%s updated by fetch: the main checkout at %s is on another branch and was left there",
			def, main)
	}

	merged, err := gitx.IsAncestor(ctx, main, sha, def)
	if err != nil {
		return "", fmt.Errorf("could not tell whether merge commit %s is in %s: %w", sha, def, err)
	}

	if !merged {
		return "", fmt.Errorf("merge commit %s is not in %s after pulling", sha, def)
	}

	return note, nil
}

// deferredPullNote names why the pull failed and what settles it.
func deferredPullNote(err error, main, def string) string {
	const retry = "; f10 retries the pull"

	switch {
	case gitx.Diverged(err):
		// the switch is spelled out: the main checkout may be on another branch
		return fmt.Sprintf("%s not pulled: the local %s has commits origin/%s lacks; "+
			"settle it with git -C %s switch %s && git -C %s rebase origin/%s, or push them%s",
			def, def, def, main, def, main, def, retry)
	case gitx.StaleRef(err):
		return fmt.Sprintf("%s not pulled: a stale origin ref is in the way; "+
			"settle it with git -C %s remote prune origin%s", def, main, retry)
	case gitx.RefLocked(err), errors.Is(err, errPullBusy):
		return fmt.Sprintf("%s not pulled: another git held origin's refs%s, "+
			"or run git -C %s pull --ff-only", def, retry, main)
	default:
		return fmt.Sprintf("%s not pulled: %v; settle it with git -C %s pull --ff-only%s", def, err, main, retry)
	}
}

// pendingPullFile, in the storage root every checkout shares, records a
// deferred pull as "<default branch> <merge sha>". Only the newest sha
// matters, since origin's branch holds every earlier merge too.
const pendingPullFile = "pending-pull"

func readPendingPull(root string) (string, string) {
	if root == "" {
		return "", ""
	}

	b, err := os.ReadFile(filepath.Join(root, pendingPullFile))
	if err != nil {
		return "", ""
	}

	def, sha, _ := strings.Cut(strings.TrimSpace(string(b)), " ")

	return def, sha
}

// clearPendingPull drops the record once its merge is in the branch.
func clearPendingPull(ctx context.Context, root, main, def string) {
	branch, sha := readPendingPull(root)
	if branch != def || sha == "" {
		return
	}

	if in, err := gitx.IsAncestor(ctx, main, sha, def); err == nil && in {
		_ = os.Remove(filepath.Join(root, pendingPullFile)) // a retry that finds it gone has nothing to do
	}
}

// retryPendingPull retries a pull a finish deferred. It reports "" with no
// pull pending, else what happened, and whether the pull is still pending.
func retryPendingPull(ctx context.Context, root, main string) (string, bool) {
	def, sha := readPendingPull(root)
	if def == "" || sha == "" {
		return "", false
	}

	note, deferred := pullMain(ctx, root, main, def, sha)
	if deferred {
		return note, true
	}

	return "pulled " + def + " up to " + shortSHA(sha) + ", which a finish had deferred", false
}
