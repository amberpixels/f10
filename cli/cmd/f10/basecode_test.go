package main

import "testing"

// A base whose only commit is a merge of its own base's newer head, as the
// catchup guard before implement leaves one, has no code: drive 6 released
// two dependents on such a merge.
func TestBaseHasCodeIgnoresMerges(t *testing.T) {
	r := newCatchupRepo(t)

	// FT-1/a is the base's base; FT-2/b stacks on it and merges its next commit
	r.at(before, "checkout", "-q", "-b", "FT-2/b", "FT-1/a")
	r.at(before, "config", "branch.FT-2/b."+cfgAfterBranch, "FT-1/a")
	r.at(before, "checkout", "-q", "FT-1/a")
	r.commit(during, "a2.go", "package main // a2\n", "a2")
	r.at(during, "checkout", "-q", "FT-2/b")
	r.at(during, "merge", "-q", "--no-ff", "--no-edit", "FT-1/a")

	if baseHasCode(t.Context(), r.dir, "FT-2/b", "main") {
		t.Error("a merge of its own base counted as code")
	}

	r.commit(during, "b.go", "package main // b2\n", "b")

	if !baseHasCode(t.Context(), r.dir, "FT-2/b", "main") {
		t.Error("a commit of its own did not count")
	}
}
