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
			name: "a settled mark beside the four",
			body: "- **Reviewer** - bot `claude[bot]`\n" +
				"- **Arrives as** - issue comment\n" +
				"- **Trigger** - automatic\n" +
				"- **Done, handled** - posted, reaction `eyes`\n" +
				"- **Settled** - marker \"All settled\"",
			want: Review{
				Reviewer: "claude[bot]", ReviewerKind: ReviewerBot, Arrives: ArrivesComment,
				Trigger: TriggerAutomatic, Done: DonePosted, Handled: HandledReaction, HandledText: "eyes",
				Settled: HandledMarker, SettledText: "All settled", settledAs: "marker \"All settled\"",
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
		{
			name: "plain keys read as the bold ones do",
			body: "- Reviewer - bot `claude[bot]`\n" +
				"- Arrives as: issue comment\n" +
				"- *Trigger* - automatic, on open and on push\n" +
				"- `Done, handled` - posted, reaction `eyes`",
			want: Review{
				Reviewer: "claude[bot]", ReviewerKind: ReviewerBot, Arrives: ArrivesComment,
				Trigger: TriggerAutomatic, TriggerHow: "on open and on push",
				Done: DonePosted, Handled: HandledReaction, HandledText: "eyes",
			},
		},
		{
			name: "prose lines are never read as facts",
			body: "Done when the bot replies - posted.\nReviewer: nobody in particular",
			want: Review{},
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

	// settled is optional: absent is not missing, unreadable is
	if got := (Review{Settled: HandledReaction, SettledText: "rocket", settledAs: "reaction `rocket`"}).Missing(); slices.Contains(
		got,
		"settled",
	) {
		t.Errorf("a readable settled reads as missing: %v", got)
	}

	for _, value := range []string{"checkbox", "thread resolved", "a thumbs up from me"} {
		if got := reviewFrom(ReviewDecl{Settled: value}).Missing(); !slices.Contains(got, "settled") {
			t.Errorf("settled %q is not refused: Missing = %v", value, got)
		}
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

func TestReviewManualAction(t *testing.T) {
	cases := []struct {
		name, trigger, how string
		kind, text         string
		ok                 bool
	}{
		{"a comment", TriggerManual, "comment `@claude review` on the PR", ManualComment, "@claude review", true},
		{
			"init's re-review line", TriggerAutomatic + ", " + TriggerManual,
			"on open and on push; manual, comment `@claude` on the PR for a re-review", ManualComment, "@claude", true,
		},
		{"a request in a code span", TriggerManual, "request a review from `@octocat`", ManualRequest, "octocat", true},
		{"a request by prose", TriggerManual, "request a review from the team lead", "", "", false},
		{"automatic only", TriggerAutomatic, "on open and on push", "", "", false},
		{"a comment with no text", TriggerManual, "ping the bot in a comment", "", "", false},
		{"neither", TriggerManual, "ask in Slack", "", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, text, ok := Review{Trigger: c.trigger, TriggerHow: c.how}.ManualAction()
			if kind != c.kind || text != c.text || ok != c.ok {
				t.Errorf("ManualAction = %q, %q, %v; want %q, %q, %v", kind, text, ok, c.kind, c.text, c.ok)
			}
		})
	}
}
