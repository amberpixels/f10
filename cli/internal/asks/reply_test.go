package asks_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/asks"
)

func TestResolveReply(t *testing.T) {
	t.Parallel()

	one := asks.ShownQuestions(asks.Ask{Text: "1. Hold the merge? [hold | merge now]"})
	two := asks.ShownQuestions(asks.Ask{Text: "1. Reuse the plan? [reuse | re-plan] 2. Deploy? [yes | no]"})
	mixed := asks.ShownQuestions(asks.Ask{Text: "1. Reuse the plan? [reuse | re-plan] 2. Which port?"})
	recovered := asks.ShownQuestions(asks.Ask{Text: "1. Merge? Options: 1) wait 2) merge now (recommended)"})

	cases := []struct {
		name  string
		qs    []asks.Shown
		reply string
		want  string
		ok    bool
	}{
		{"letter", one, "b", "1. merge now", true},
		{"number", one, "2", "1. merge now", true},
		{"option text", one, " Merge Now ", "1. merge now", true},
		{"numbered lone", one, "1. a", "1. hold", true},
		{"prose", one, "hold it until Friday", "", false},
		{"numbered", two, "1. b 2. b", "1. re-plan 2. no", true},
		{"tokens", two, "b a", "1. re-plan 2. yes", true},
		{"commas", two, "b,b", "1. re-plan 2. no", true},
		{"numbered partial", two, "2. b", "1. reuse 2. no", true},
		{"too few tokens", two, "b", "", false},
		{"bad letter", two, "1. z", "", false},
		{"free question", mixed, "1. a 2. 8080", "", false},
		// a recovered option goes as its text: the stored question has none
		{"recovered", recovered, "b", "1. wait", true},
		{"recovered default", recovered, "a", "1. merge now", true},
		{"empty", one, "  ", "", false},
	}

	for _, c := range cases {
		got, ok := asks.ResolveReply(c.qs, c.reply)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: ResolveReply(%q) = %q, %v; want %q, %v", c.name, c.reply, got, ok, c.want, c.ok)
		}
	}
}
