package forge

import "testing"

// One request read serves both CLIs: a fork's branch is told apart on each,
// and the landed commit prefers gh's merge commit, then glab's squash.
func TestPRReadsBothCLIs(t *testing.T) {
	script(t, map[string]string{
		"gh pr view 3 --json " + prFields: `{"number":3,"state":"MERGED","url":"https://github.com/o/r/pull/3",` +
			`"headRefName":"x","isCrossRepository":true,"mergeCommit":{"oid":"abc"},"mergedBy":{"login":"alice"}}`,
		"glab mr view 7 -F json": `{"iid":7,"state":"merged","web_url":"https://gl/mr/7","source_branch":"b",` +
			`"source_project_id":2,"target_project_id":1,"merge_commit_sha":"mmm","squash_commit_sha":"sss"}`,
		"glab mr view 8 -F json": `{"iid":8,"state":"opened","source_project_id":1,"target_project_id":1}`,
	})

	gh, err := Host{Name: "gh"}.PR(t.Context(), "3")
	if err != nil || !gh.Fork || gh.Landed != "abc" || gh.Status != Merged || gh.MergedBy != "alice" || gh.Head != "x" {
		t.Errorf("gh PR = %+v, %v", gh, err)
	}

	gl, err := Host{Name: "glab"}.PR(t.Context(), "7")
	if err != nil || !gl.Fork || gl.Landed != "sss" || gl.Number != 7 || gl.Head != "b" {
		t.Errorf("glab MR = %+v, %v", gl, err)
	}

	same, err := Host{Name: "glab"}.PR(t.Context(), "8")
	if err != nil || same.Fork || same.Status != Open {
		t.Errorf("an MR from its own project = %+v, %v", same, err)
	}
}

// A request's number reads the same from its host and from its url alone,
// so a GitLab series is closed with !12, as review round wrote it.
func TestRequestRefFollowsTheHost(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		host, url, want string
	}{
		{"gh", "https://github.com/o/r/pull/12", "#12"},
		{"glab", "https://gitlab.com/o/r/-/merge_requests/12", "!12"},
	} {
		if got := (Host{Name: tc.host}).Ref(12); got != tc.want {
			t.Errorf("%s Ref(12) = %q, want %q", tc.host, got, tc.want)
		}

		if got := PullNumber(tc.url); got != tc.want {
			t.Errorf("PullNumber(%s) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

// An empty author or login is unknown, never mine or someone else's, so
// finish, pr view, trace and the janitor read it alike.
func TestOwnerOfAnEmptyAuthorIsUnknown(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		author, login string
		want          Owner
	}{
		{"Alice", "alice", OwnerMine},
		{"bob", "alice", OwnerOther},
		{"", "alice", OwnerUnknown},
		{"alice", "", OwnerUnknown},
	} {
		if got := OwnerOf(tc.author, tc.login); got != tc.want {
			t.Errorf("OwnerOf(%q, %q) = %v, want %v", tc.author, tc.login, got, tc.want)
		}
	}
}
