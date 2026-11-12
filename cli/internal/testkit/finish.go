package testkit

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/state"
)

const (
	GhView  = "gh pr view GH-1 --json number,title,state,url,body,author,baseRefName,headRefName,headRefOid,isCrossRepository,mergeStateStatus,mergeCommit,mergedAt,mergedBy,closedAt"
	GhRepo  = "gh repo view --json squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed"
	GhMerge = "gh pr merge GH-1 --squash"
	GhDel   = "gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1"
	GhStack = "gh pr list --base GH-1 --state open --limit 1000 --json number,url,headRefName"
	LsRem   = "git ls-remote --heads origin GH-1"
	// DropRemoteRef is finish dropping the local copy of origin's branch along with the branch
	DropRemoteRef = "git update-ref -d refs/remotes/origin/GH-1"
	CurBr         = "git branch --show-current"
	WsList        = "herdr workspace list"

	// GhChecks is the head checks read, every PR number
	GhChecks = "gh api graphql --paginate -f query=" + forge.HeadChecksQuery + " -F owner={owner} -F name={repo} -F number="
	NoChecks = `{"data":{"repository":{"pullRequest":{"prev":{"nodes":[]},"head":{"nodes":[]}}}}}`

	PrOpen   = `{"state":"OPEN","url":"https://gh/pr/9","number":9,"baseRefName":"main","mergeCommit":null}`
	PrMerged = `{"state":"MERGED","url":"https://gh/pr/9","number":9,"baseRefName":"main","mergeCommit":{"oid":"m3rge"}}`
	AllowAll = `{"squashMergeAllowed":true,"mergeCommitAllowed":true,"rebaseMergeAllowed":true}`
)

func IndexAfter(calls []string, want string, after int) int {
	for i := after + 1; i < len(calls); i++ {
		if calls[i] == want {
			return i
		}
	}

	return -1
}

// SessionTask records id as this session's task, as a plan run's badge does.
func SessionTask(t *testing.T, id string) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")

	if err := (&state.Run{Session: "sess-1", Task: id}).Save(t.Context(), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
}
