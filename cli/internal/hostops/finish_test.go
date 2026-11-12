package hostops_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// glab takes the whole message, so the title goes on top of the body.
func TestMergePullGlabSquashMessage(t *testing.T) {
	f := testkit.NewFakes(t)
	h := hostops.NewHost("glab", t.TempDir())
	view := "glab mr view GH-1 -F json"
	merge := "glab mr merge GH-1 --remove-source-branch --auto-merge=false --yes --squash --squash-message Add the thing\n\nTask: GH-1\n"
	f.Script(merge, "")
	f.Script(view, `{"state":"merged","web_url":"https://gl/mr/9","squash_commit_sha":"sq"}`)

	p, err := h.MergePR(t.Context(), forge.PR{Title: "Add the thing"}, "GH-1", "--squash", "Task: GH-1\n")
	if err != nil || p.Landed != "sq" {
		t.Fatalf("merged = %+v, %v", p, err)
	}

	if !f.Called(merge) {
		t.Errorf("calls:\n%s", strings.Join(f.Calls, "\n"))
	}
}

// gh's squash takes the title as its subject: left to itself, GitHub uses a
// single commit's own subject instead.
func TestMergePullGhSquashSubject(t *testing.T) {
	cases := []struct {
		name  string
		p     forge.PR
		merge string
	}{
		{
			"titled",
			forge.PR{Title: "Add the thing", Number: 9},
			"gh pr merge GH-1 --squash --subject Add the thing (#9) --body Task: GH-1\n",
		},
		{"untitled", forge.PR{Number: 9}, "gh pr merge GH-1 --squash --body Task: GH-1\n"},
	}

	for _, c := range cases {
		f := testkit.NewFakes(t)
		h := hostops.NewHost("gh", t.TempDir())
		f.Script(c.merge, "")
		f.Script(testkit.GhView, testkit.PrMerged)

		if _, err := h.MergePR(t.Context(), c.p, "GH-1", "--squash", "Task: GH-1\n"); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}

		if !f.Called(c.merge) {
			t.Errorf("%s: calls:\n%s", c.name, strings.Join(f.Calls, "\n"))
		}
	}
}

func TestMergeMethod(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		declared string
		repo     string
		want     string
		noted    bool // the method was a default worth saying
	}{
		{"declared squash", "gh", "squash", "", "--squash", false},
		{"declared rebase, any case", "gh", "REBASE", "", "--rebase", false},
		{"declared merge on gitlab is plain", "glab", "merge", "", "", false},
		{
			"one allowed",
			"gh",
			"",
			`{"squashMergeAllowed":false,"mergeCommitAllowed":false,"rebaseMergeAllowed":true}`,
			"--rebase",
			false,
		},
		{"several allowed", "gh", "", testkit.AllowAll, "--squash", true},
		{"gitlab squash always", "glab", "", `{"squash_option":"always"}`, "--squash", false},
		{"gitlab squash off", "glab", "", `{"squash_option":"never"}`, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := testkit.NewFakes(t)
			f.Script(testkit.GhRepo, c.repo)
			f.Script("glab repo view -F json", c.repo)

			got, note, err := hostops.NewHost(c.host, "/repo").MergeMethod(t.Context(), c.declared)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("mergeMethod = %q, want %q", got, c.want)
			}

			if (note != "") != c.noted {
				t.Errorf("note = %q, noted = %v", note, c.noted)
			}

			if c.declared != "" && f.Called(c.host+" repo view") {
				t.Error("the repo was asked although project.md declares the method")
			}
		})
	}
}
