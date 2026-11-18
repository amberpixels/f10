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

// A new branch whose name a landed request used takes the next numbered name
// that no ref and no landed request has, with a note. With no lookup, or a
// name nothing landed on, the name stays.
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

// A local branch at a merged request's head with nothing past it is
// refused, because reusing it finishes the task at once. Origin's copy of
// one gives way to a new name. A branch with work past it, or whose request
// closed without a merge, is reused.
func TestAvoidLandedFoundBranch(t *testing.T) {
	merged := forge.PR{URL: "https://x/pull/576", Status: forge.Merged, HeadSHA: "aaa"}
	heads := map[string]forge.PR{"GH-1/slug": merged}

	t.Run("local leftover", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script("git rev-parse --verify --quiet refs/heads/GH-1/slug", "aaa")

		var asked []string
		in := StartInput{Main: "/code/repo", Landed: landedOn(heads, &asked)}

		_, _, _, err := avoidLanded(t.Context(), in, "GH-1/slug", stack.FoundBranch{Name: "GH-1/slug"})
		if err == nil || !strings.Contains(err.Error(), merged.URL) || !strings.Contains(err.Error(), "which merged") {
			t.Errorf("err = %v, want the leftover refused, naming the PR", err)
		}
	})

	t.Run("closed without a merge", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script("git rev-parse --verify --quiet refs/heads/GH-1/slug", "aaa")

		closed := forge.PR{URL: "https://x/pull/90", Status: forge.Closed, HeadSHA: "aaa"}

		var asked []string
		in := StartInput{Main: "/code/repo", Landed: landedOn(map[string]forge.PR{"GH-1/slug": closed}, &asked)}

		got, existing, _, err := avoidLanded(t.Context(), in, "GH-1/slug", stack.FoundBranch{Name: "GH-1/slug"})
		if err != nil || got != "GH-1/slug" || existing.Name != "GH-1/slug" {
			t.Errorf("avoidLanded = %q, %+v, %v; want the only copy of unmerged work reused", got, existing, err)
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
