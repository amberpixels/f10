package asks

import "testing"

func TestBanner(t *testing.T) {
	t.Parallel()

	rule := bannerRule + "\n"
	foot := rule + " " + BannerFooter + "\n"

	cases := []struct {
		name  string
		ask   Ask
		label string
		want  string
	}{
		{
			name: "options",
			ask: Ask{
				Task:  "FT-12",
				Skill: "resolve",
				Text:  "1. Reuse the saved plan? [reuse | re-plan] 2. Deploy to staging? [yes | no]",
			},
			want: rule + " " + AskIcon + " ASK · FT-12 · at resolve\n" + rule +
				" 1. Reuse the saved plan?\n    > reuse | re-plan\n" +
				" 2. Deploy to staging?\n    > yes | no\n" + foot,
		},
		{
			name:  "free text, no skill, named",
			ask:   Ask{Task: "FT-3", Text: "Which port should it listen on?"},
			label: "app:FT-3",
			want: rule + " " + AskIcon + " ASK · app:FT-3\n" + rule +
				" 1. Which port should it listen on?\n" + foot,
		},
	}

	for _, c := range cases {
		if got := Banner(c.ask, c.label); got != c.want {
			t.Errorf("%s:\n%s\nwant:\n%s", c.name, got, c.want)
		}
	}
}
