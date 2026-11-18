package start

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// landedOn answers the lookup from a map of head branch to landed request.
func landedOn(heads map[string]forge.PR, asked *[]string) LandedLookup {
	return func(_ context.Context, branch string) (forge.PR, bool, error) {
		*asked = append(*asked, branch)
		p, ok := heads[branch]

		return p, ok, nil
	}
}

// A new branch whose name a landed request used as its head takes the next
// numbered name no ref and no landed request has, with a note; no lookup,
// or a name nothing landed on, keeps the name.
func TestAvoidLandedNumbersANewBranch(t *testing.T) {
	merged := forge.PR{URL: "https://x/pull/576", Status: forge.Merged, HeadSHA: "aaa"}

	for _, tc := range []struct {
		name  string
		heads map[string]forge.PR
		taken []string // numbered names a local ref already has
		want  string
		note  bool
	}{
		{name: "nothing landed", want: "GH-1/slug"},
		{name: "the name landed", heads: map[string]forge.PR{"GH-1/slug": merged}, want: "GH-1/slug-2", note: true},
		{
			name:  "two names landed",
			heads: map[string]forge.PR{"GH-1/slug": merged, "GH-1/slug-2": merged},
			want:  "GH-1/slug-3", note: true,
		},
		{
			name:  "a numbered name taken by a ref",
			heads: map[string]forge.PR{"GH-1/slug": merged},
			taken: []string{"GH-1/slug-2"},
			want:  "GH-1/slug-3", note: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := testkit.NewFakes(t)

			for _, n := range []string{"GH-1/slug-2", "GH-1/slug-3"} {
				if slices.Contains(tc.taken, n) {
					f.Script("git rev-parse --verify --quiet refs/heads/"+n, "bbb")
				} else {
					f.Fail("git rev-parse --verify --quiet refs/heads/"+n, testkit.Missing)
				}

				f.Fail("git rev-parse --verify --quiet refs/remotes/origin/"+n, testkit.Missing)
			}

			var asked []string
			in := StartInput{Main: "/code/repo", Landed: landedOn(tc.heads, &asked)}

			got, existing, note, err := avoidLanded(t.Context(), in, "GH-1/slug", stack.FoundBranch{})
			if err != nil || got != tc.want || existing.Name != "" || (note != "") != tc.note {
				t.Errorf(
					"avoidLanded = %q, %+v, %q, %v; want %q, a note %v",
					got,
					existing,
					note,
					err,
					tc.want,
					tc.note,
				)
			}

			if tc.note && !strings.Contains(note, merged.URL) {
				t.Errorf("the note %q does not name %s", note, merged.URL)
			}
		})
	}

	t.Run("no lookup", func(t *testing.T) {
		got, _, note, err := avoidLanded(t.Context(), StartInput{}, "GH-1/slug", stack.FoundBranch{})
		if err != nil || got != "GH-1/slug" || note != "" {
			t.Errorf("avoidLanded = %q, %q, %v", got, note, err)
		}
	})
}

// A found branch that is a landed request's head with nothing past it is
// refused when local, since reusing it finishes the task at once, and left
// for a new name when only origin has it; one with work past it is reused.
func TestAvoidLandedFoundBranch(t *testing.T) {
	merged := forge.PR{URL: "https://x/pull/576", Status: forge.Merged, HeadSHA: "aaa"}
	heads := map[string]forge.PR{"GH-1/slug": merged}

	t.Run("local leftover", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script("git rev-parse --verify --quiet refs/heads/GH-1/slug", "aaa")

		var asked []string
		in := StartInput{Main: "/code/repo", Landed: landedOn(heads, &asked)}

		_, _, _, err := avoidLanded(t.Context(), in, "GH-1/slug", stack.FoundBranch{Name: "GH-1/slug"})
		if err == nil || !strings.Contains(err.Error(), merged.URL) || !strings.Contains(err.Error(), "git branch -D") {
			t.Errorf("err = %v, want the leftover refused, naming the PR", err)
		}
	})

	t.Run("work past the landed head", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script("git rev-parse --verify --quiet refs/heads/GH-1/slug", "ccc")
		f.Fail("git merge-base --is-ancestor ccc aaa", testkit.Missing)

		var asked []string
		in := StartInput{Main: "/code/repo", Landed: landedOn(heads, &asked)}

		got, existing, note, err := avoidLanded(t.Context(), in, "GH-1/slug", stack.FoundBranch{Name: "GH-1/slug"})
		if err != nil || got != "GH-1/slug" || existing.Name != "GH-1/slug" || note != "" {
			t.Errorf("avoidLanded = %q, %+v, %q, %v; want the branch reused", got, existing, note, err)
		}
	})

	t.Run("origin's stale copy", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script("git rev-parse --verify --quiet refs/remotes/origin/GH-1/slug", "aaa")
		f.Fail("git rev-parse --verify --quiet refs/heads/GH-1/slug-2", testkit.Missing)
		f.Fail("git rev-parse --verify --quiet refs/remotes/origin/GH-1/slug-2", testkit.Missing)

		var asked []string
		in := StartInput{Main: "/code/repo", Landed: landedOn(heads, &asked)}

		got, existing, note, err := avoidLanded(t.Context(), in, "GH-1/slug",
			stack.FoundBranch{Name: "GH-1/slug", Remote: true})
		if err != nil || got != "GH-1/slug-2" || existing.Name != "" || note == "" {
			t.Errorf("avoidLanded = %q, %+v, %q, %v; want a new GH-1/slug-2", got, existing, note, err)
		}
	})
}
