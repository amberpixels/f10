package flow

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	for written, want := range map[string]Entry{
		"implement":                {Step: "implement"},
		"review:local":             {Step: "review", Arg: "local"},
		"review:ci[review-final]":  {Step: "review", Arg: "ci", Label: "review-final"},
		"cleanup:comments,prose":   {Step: "cleanup", Arg: "comments,prose"},
		"deploy:staging[go-live]":  {Step: "deploy", Arg: "staging", Label: "go-live"},
		"pr[polish":                {Step: "pr[polish"},
		"review:local+ci?[review]": {Step: "review", Arg: "local+ci?", Label: "review"},
	} {
		got := Parse(written)
		if got != want {
			t.Errorf("Parse(%q) = %+v, want %+v", written, got, want)
		}

		if want.Label != "" && got.Written() != strings.TrimSpace(written) {
			t.Errorf("Parse(%q).Written() = %q", written, got.Written())
		}
	}

	if got := Parse("  pr[pr-polish] "); got != (Entry{Step: "pr", Label: "pr-polish"}) {
		t.Errorf("padding is trimmed: %+v", got)
	}

	if e := Parse("review:ci[review-final]"); e.String() != "review:ci" {
		t.Errorf("String = %q", e.String())
	}
}

func TestFlowPredicates(t *testing.T) {
	t.Parallel()

	f := Of([]string{"plan", "judge", "implement", "review:local", "judge", "pr[pr-final]"})

	if got := f.Ship(); got[0].Step != "judge" || len(got) != 5 {
		t.Errorf("ship = %v", got)
	}

	if f.JudgeKind(1) != "plan" || f.JudgeKind(4) != "change" {
		t.Error("judge kind is read by position against implement")
	}

	if !f.Plans() || Of([]string{"implement", "pr"}).Plans() || Of([]string{"plan:x", "pr"}).Plans() {
		t.Error("only a bare leading plan plans")
	}

	if f.PublishedBefore(5) || !Of([]string{"implement", "push", "review:ci"}).PublishedBefore(2) {
		t.Error("published is a pr or push before the entry")
	}

	if f.Delivered() != "pr" || Of([]string{"implement", "pr", "deploy:prod"}).Delivered() != "deploy" {
		t.Error("delivered is the last delivering entry")
	}

	if got := f.Labels(); len(got) != 6 || got[5] != "pr-final" || got[0] != "" {
		t.Errorf("labels = %q", got)
	}

	if Of([]string{"plan", "pr"}).Labels() != nil {
		t.Error("a flow with no label has no labels")
	}
}

func TestZipAndJSON(t *testing.T) {
	t.Parallel()

	f := Zip([]string{"implement", "pr", "pr"}, []string{"", "", "pr-polish"})
	if got := strings.Join(f.Written(), " "); got != "implement pr pr[pr-polish]" {
		t.Errorf("zip = %q", got)
	}

	if Zip([]string{"implement", "pr"}, nil)[1].Label != "" {
		t.Error("a missing label is empty")
	}

	b, err := json.Marshal(f)
	if err != nil || string(b) != `["implement","pr","pr[pr-polish]"]` {
		t.Fatalf("json = %s, %v", b, err)
	}

	var back Flow
	if err := json.Unmarshal(b, &back); err != nil || !slices.Equal(back, f) {
		t.Errorf("round trip = %v, %v", back, err)
	}
}

// GenericSteps tracks the plugin's step files, so a label can never take
// a generic step's name once a step is added.
func TestGenericStepsMatchStepFiles(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("../../../steps/*.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("no step files: %v", err)
	}

	want := []string{"finish"}
	for _, f := range files {
		want = append(want, strings.TrimSuffix(filepath.Base(f), ".md"))
	}

	slices.Sort(want)

	if got := slices.Sorted(slices.Values(GenericSteps)); !slices.Equal(got, want) {
		t.Errorf("GenericSteps = %v, want the steps/ files plus finish: %v", got, want)
	}
}
