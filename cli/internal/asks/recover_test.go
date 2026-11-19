package asks

import (
	"slices"
	"testing"
)

func TestRecover(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, text string
		ok         bool
		want       Recovered
	}{
		{
			name: "options label, recommended item leads",
			text: "Tests are red on PR #61 for a reason from main. Log: https://ci.example/runs/4455. " +
				"What should happen with the red tests? Options: 1) fix it in this PR, " +
				"2) leave it red (recommended: main needs its own fix), 3) rerun once more.",
			ok: true,
			want: Recovered{
				Context: "Tests are red on PR #61 for a reason from main. Log: https://ci.example/runs/4455.",
				Question: Question{N: 1, Text: "What should happen with the red tests?", Options: []string{
					"leave it red", "fix it in this PR", "rerun once more",
				}},
				Note: "recommended: main needs its own fix",
			},
		},
		{
			name: "lettered in parentheses, no recommendation",
			text: "Which way? (a) merge it (b) close it",
			ok:   true,
			want: Recovered{Question: Question{N: 1, Text: "Which way?", Options: []string{"merge it", "close it"}}},
		},
		{
			name: "dotted after a label, no question sentence",
			text: "CI is red. Options: 1. leave it red 2. fix it 3. rerun",
			ok:   true,
			want: Recovered{Question: Question{N: 1, Text: "CI is red.", Options: []string{
				"leave it red", "fix it", "rerun",
			}}},
		},
		{
			name: "trailing or",
			text: "Keep it? a) keep, or b) drop",
			ok:   true,
			want: Recovered{Question: Question{N: 1, Text: "Keep it?", Options: []string{"keep", "drop"}}},
		},
		{
			name: "a list followed by a question is prose",
			text: "Pick 1) the client or 2) the server, then which port?",
		},
		{
			name: "a single item is no list",
			text: "Run step 1) now?",
		},
		{
			name: "numbers mid-prose that never count up",
			text: "Keep v1. 2 clients use it?",
		},
	}

	for _, c := range cases {
		got, ok := Recover(Question{N: 1, Text: c.text})
		if ok != c.ok {
			t.Errorf("%s: Recover ok = %v, want %v (%+v)", c.name, ok, c.ok, got)

			continue
		}

		if ok && (got.Context != c.want.Context || got.Note != c.want.Note || got.Question.N != c.want.Question.N ||
			got.Question.Text != c.want.Question.Text || !slices.Equal(got.Question.Options, c.want.Question.Options)) {
			t.Errorf("%s: Recover =\n%+v\nwant\n%+v", c.name, got, c.want)
		}
	}
}

// A question with bracketed options is already structured.
func TestRecoverKeepsOptions(t *testing.T) {
	t.Parallel()

	if _, ok := Recover(Question{N: 1, Text: "Options: 1) a 2) b", Options: []string{"x", "y"}}); ok {
		t.Error("Recover reinterpreted a question that has options")
	}
}

// Every question Lint warns about is one the walk can lay out.
func TestRecoverAgreesWithLint(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"CI is red on the lint job. Options: 1) leave it red, 2) fix it in this PR, 3) rerun once more",
		"1. What next? a) leave it b) fix it",
		"CI is red. Options: 1. leave it red 2. fix it 3. rerun",
		"Which do you prefer? 1. the client 2. the server",
	} {
		for _, q := range Parse(text) {
			if len(Lint([]Question{q})) == 0 {
				t.Fatalf("Lint no longer warns on %q", text)
			}

			if _, ok := Recover(q); !ok {
				t.Errorf("Lint warns on %q, but Recover finds no list", q.Text)
			}
		}
	}
}

func TestSentences(t *testing.T) {
	t.Parallel()

	got := Sentences("Red at v0.68.0, e.g. lint. Log: https://x.example/a.b. 3 jobs failed! why? done")
	want := []string{"Red at v0.68.0, e.g. lint.", "Log: https://x.example/a.b.", "3 jobs failed! why? done"}

	if !slices.Equal(got, want) {
		t.Errorf("Sentences = %q, want %q", got, want)
	}
}
