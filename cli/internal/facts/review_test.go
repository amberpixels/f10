package facts

import (
	"slices"
	"testing"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
)

func TestParseReview(t *testing.T) {
	cases := []struct {
		name string
		body string
		want Review
	}{
		{
			name: "the four facts as init writes them",
			body: "- **Reviewer** - bot `claude[bot]`\n" +
				"- **Arrives as** - issue comment\n" +
				"- **Trigger** - automatic, on open and on push\n" +
				"- **Done, handled** - posted, reaction `eyes`",
			want: Review{
				Reviewer: "claude[bot]", ReviewerKind: ReviewerBot, Arrives: ArrivesComment,
				Trigger: TriggerAutomatic, TriggerHow: "on open and on push",
				Done: DonePosted, Handled: HandledReaction, HandledText: "eyes",
			},
		},
		{
			name: "a human on inline reviews, threads resolved, a marker for done",
			body: "Reviews are code owner reviews.\n\n" +
				"- **Reviewer**: human `octocat`\n" +
				"- **Arrives as**: inline review\n" +
				"- **Trigger**: manual, request a review from octocat\n" +
				"- **Done, handled**: marker \"LGTM\", thread resolved\n\n" +
				"- **Categories** - accessibility, source of truth `docs/a11y.md`",
			want: Review{
				Reviewer: "octocat", ReviewerKind: ReviewerHuman, Arrives: ArrivesInline,
				Trigger: TriggerManual, TriggerHow: "request a review from octocat",
				Done: DoneMarker, DoneText: "LGTM", Handled: HandledThread,
			},
		},
		{
			name: "a check run, concluded, with both triggers and a checkbox",
			body: "- **Reviewer** - check `Claude Review`\n" +
				"- **Arrives as** - check run\n" +
				"- **Trigger** - automatic on push; manual, comment `@claude`\n" +
				"- **Done, handled** - concluded, checkbox",
			want: Review{
				Reviewer: "Claude Review", ReviewerKind: ReviewerCheck, Arrives: ArrivesCheck,
				Trigger: "automatic, manual", TriggerHow: "comment `@claude`",
				Done: DoneConcluded, Handled: HandledCheckbox,
			},
		},
		{
			name: "the local-only section declares no remote reviewer",
			body: "- **Categories** - UI, source of truth `docs/gallery.md`\n- **Never flag** - generated files",
			want: Review{},
		},
		{
			name: "a line outside the vocabulary leaves its fact empty",
			body: "- **Reviewer** - whoever is around\n- **Arrives as** - by carrier pigeon\n" +
				"- **Trigger** - automatic\n- **Done, handled** - posted, reaction",
			want: Review{Trigger: TriggerAutomatic, Done: DonePosted, Handled: HandledReaction, HandledText: "eyes"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseReview(c.body); got != c.want {
				t.Errorf("parseReview:\n got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestReviewMissing(t *testing.T) {
	r := Review{Reviewer: "x", ReviewerKind: ReviewerBot, Trigger: TriggerAutomatic}
	if got := r.Missing(); !slices.Equal(got, []string{"Arrives as", "Done", "handled"}) {
		t.Errorf("Missing = %v", got)
	}

	if (Review{}).Remote() {
		t.Error("the zero Review claims a remote reviewer")
	}
}

// With no Review declared, a detected workflow surfaces as a detected field
// whose value parses to the same four facts; a declared section wins over
// it, and one declaring only the local half declares no remote reviewer.
func TestBuildReviewFallsBackToTheWorkflow(t *testing.T) {
	pb := &probe.Probes{
		CLI:    probe.Signal{Value: "gh", Evidence: "well-known host github.com"},
		Review: &probe.Review{Reviewer: "claude[bot]", Trigger: "automatic", Workflow: ".github/workflows/review.yml"},
	}

	undeclared := Build(&layout.Layout{Project: "demo", StorageRoot: "/repo/.f10"}, pb)

	f := fieldByName(t, undeclared, "Review")
	if f.Origin != OriginDetected || f.Source != ".github/workflows/review.yml" || !f.Prose {
		t.Errorf("Review field = %+v, want detected prose from the workflow", f)
	}

	if !undeclared.Review.Remote() || undeclared.Review.Reviewer != "claude[bot]" ||
		undeclared.Review.Handled != HandledReaction {
		t.Errorf("review facts from detection = %+v", undeclared.Review)
	}

	declared := Build(&layout.Layout{
		Project:     "demo",
		StorageRoot: "/repo/.f10",
		Layers: []layout.Layer{
			{Label: "main", Dir: dirWithProjectMD(t, "## Review\n\n- **Never flag** - vendored code\n")},
		},
	}, pb)

	if declared.Review.Remote() {
		t.Errorf("a declared local-only section still claims the workflow's reviewer: %+v", declared.Review)
	}

	none := Build(&layout.Layout{Project: "demo", StorageRoot: "/repo/.f10"},
		&probe.Probes{CLI: probe.Signal{Value: "gh", Evidence: "well-known host github.com"}})

	if f := fieldByName(t, none, "Review"); f.Origin != OriginAbsent {
		t.Errorf("Review without a workflow = %+v, want absent", f)
	}
}
