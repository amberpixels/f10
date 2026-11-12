package standup_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	standupcmd "github.com/amberpixels/f10/cli/internal/command/standup"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestParseSince(t *testing.T) {
	if got, err := standupcmd.ParseSince(
		"2026-10-14T09:30:00+03:00",
	); err != nil ||
		!got.Equal(time.Date(2026, 10, 14, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("rfc3339 = %v, %v", got, err)
	}

	if got, err := standupcmd.ParseSince(
		"2026-10-14",
	); err != nil ||
		got != time.Date(2026, 10, 14, 0, 0, 0, 0, time.Local) {
		t.Errorf("date = %v, %v, want local midnight", got, err)
	}

	for _, bad := range []string{"2d", "yesterday", "", "2026-10-14 09:30", "1760000000"} {
		if _, err := standupcmd.ParseSince(bad); err == nil {
			t.Errorf("parseSince(%q) took it; the period's forms are not --since's", bad)
		}
	}
}

func TestRenderStandup(t *testing.T) {
	merged := testkit.StandupSince.Add(5 * time.Hour)
	r := standup.Standup{
		Since:  testkit.StandupSince,
		Period: standup.PeriodLastStandup,
		Days:   []string{"2026-10-09", "2026-10-12"},
		Projects: []standup.Project{{
			Name:  "f10",
			Repos: []standup.Repo{{Name: "f10"}},
			Tasks: []standup.Task{{ID: "FT-1", State: standup.StateInReview, PRs: []standup.PR{
				{Number: 1, MergedAt: &merged, Commits: []standup.Commit{{SHA: "a"}}}, {Number: 2, BasePR: 1},
			}}},
			Other: standup.Other{Commits: []standup.Commit{{SHA: "x"}}},
			Reviewed: []standup.ReviewedPR{
				{
					Number:  9,
					Title:   "theirs",
					Author:  "bob",
					Actions: []standup.Action{{Kind: "reviewed"}, {Kind: "merged"}},
				},
			},
		}},
	}

	var out bytes.Buffer
	if err := standupcmd.RenderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"(last standup)", "Fri 10-09, Mon 10-12", "FT-1", "in review", "#1 -> #2", "1 commit(s)", "#9 theirs (bob): reviewed, merged"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}

// Upkeep-only tasks leave the per-task lines for one line of their own.
func TestRenderStandupFoldsUpkeep(t *testing.T) {
	r := standup.Standup{Since: testkit.StandupSince, Projects: []standup.Project{{Tasks: []standup.Task{
		{ID: "FT-1", State: standup.StateInReview, Class: standup.ClassUpkeep},
		{ID: "FT-2", State: standup.StateInProgress, Class: standup.ClassWork},
		{ID: "FT-3", State: standup.StateInReview, Class: standup.ClassUpkeep},
	}}}}

	var out bytes.Buffer
	if err := standupcmd.RenderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "kept up to date  FT-1, FT-3\n") || strings.Contains(got, "FT-1      ") ||
		!strings.Contains(got, "FT-2") {
		t.Errorf("render:\n%s", got)
	}
}

// A merge made on the default branch merges into it: unpushed, its second
// parent is origin's tip, and it must still not read as a base merge.
// A CI read that fails, as for a parent the host never saw, leaves the
// commit's CI unknown instead of failing the report; commits from before
// the period are never asked about.
// Several projects each get a heading; a project over several repositories
// names each PR with its repository.
func TestRenderStandupProjects(t *testing.T) {
	r := standup.Standup{Since: testkit.StandupSince, Projects: []standup.Project{
		{
			Name:  "ACME",
			Repos: []standup.Repo{{Name: "svc-a"}, {Name: "svc-b"}},
			Tasks: []standup.Task{{ID: "ACME-1", State: standup.StateInReview, PRs: []standup.PR{
				{Repo: "svc-a", Number: 12}, {Repo: "svc-b", Number: 40}, {Repo: "svc-b", Number: 41, BasePR: 40},
			}}},
		},
		{
			Name:  "r3",
			Repos: []standup.Repo{{Name: "r3"}},
			Tasks: []standup.Task{
				{ID: "R3-2", State: standup.StateShipped, PRs: []standup.PR{{Repo: "r3", Number: 5}}},
			},
		},
	}}

	var out bytes.Buffer
	if err := standupcmd.RenderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"== ACME (svc-a, svc-b)\n", "svc-a#12, svc-b#40 -> svc-b#41", "== r3\n", "R3-2       shipped      #5"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}

// An arrow means a PR sits on the one before it: siblings on one root and
// unrelated PRs in other are listed, not chained.
func TestRenderStandupChainsOnlyStackedPRs(t *testing.T) {
	r := standup.Standup{Since: testkit.StandupSince, Projects: []standup.Project{{
		Tasks: []standup.Task{{ID: "FT-1", State: standup.StateInReview, PRs: []standup.PR{
			{Number: 1}, {Number: 2, BasePR: 1}, {Number: 3, BasePR: 1},
		}}},
		Other: standup.Other{PRs: []standup.PR{{Number: 5}, {Number: 9, BasePR: 3}}},
	}}}

	var out bytes.Buffer
	if err := standupcmd.RenderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"#1 -> #2, #3", "other      #5, #9"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}

// A GitLab repository's merge requests read !n, alone or beside a GitHub
// repository's #n in one project.
func TestRenderStandupWritesGitLabRequests(t *testing.T) {
	glab, gh := "glab", "gh"
	r := standup.Standup{Since: testkit.StandupSince, Projects: []standup.Project{
		{
			Name:  "ACME",
			Repos: []standup.Repo{{Name: "svc-a", Host: &glab}, {Name: "svc-b", Host: &gh}},
			Tasks: []standup.Task{{ID: "ACME-1", State: standup.StateInReview, PRs: []standup.PR{
				{Repo: "svc-a", Number: 12}, {Repo: "svc-b", Number: 40},
			}}},
		},
		{
			Name:     "r3",
			Repos:    []standup.Repo{{Name: "r3", Host: &glab}},
			Tasks:    []standup.Task{{ID: "R3-2", State: standup.StateShipped, PRs: []standup.PR{{Number: 5}}}},
			Reviewed: []standup.ReviewedPR{{Number: 9, Title: "theirs", Author: "bob"}},
		},
	}}

	var out bytes.Buffer
	if err := standupcmd.RenderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"svc-a!12, svc-b#40", "R3-2       shipped      !5", "!9 theirs (bob)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}
