package launch_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestHoldsPrompt(t *testing.T) {
	cases := map[string]struct {
		screen string
		want   bool
	}{
		"the prompt in the box": {testkit.ScreenWith("❯ " + testkit.SentText), true},
		"the prompt wrapped":    {testkit.ScreenWith("❯ /f10:ship GH-1 resume\n  at pr (entry 4) --driven"), true},
		"an empty box":          {testkit.ScreenWith("❯ "), false},
		"the user's own text":   {testkit.ScreenWith("❯ what about the tests"), false},
		"the user's own paste":  {testkit.ScreenWith("❯ [Pasted text #1 +12 lines]"), false},
		"no box on screen":      {"❯ " + testkit.SentText + "\n", false},
		"the prompt above the box": {
			"❯ " + testkit.SentText + "\n" + testkit.BoxRule + "\n❯ \n" + testkit.BoxRule + "\n",
			false,
		},
	}

	for name, c := range cases {
		if got := launch.HoldsPrompt(c.screen, testkit.SentText); got != c.want {
			t.Errorf("%s: holdsPrompt = %v, want %v", name, got, c.want)
		}
	}

	if !launch.HoldsPrompt(testkit.ScreenWith("❯ [Pasted text #1 +2 lines]"), "first\nsecond") {
		t.Error("a multi-line prompt pasted as a block reads as submitted")
	}
}

// A typed prompt still in the input box gets Enter once; one that went
// through gets none.
func TestPromptCheckedResendsEnterOnce(t *testing.T) {
	for name, c := range map[string]struct {
		screens []string
		enters  int
		note    bool
	}{
		"held":      {[]string{testkit.ScreenWith("❯ " + testkit.SentText)}, 1, true},
		"submitted": {[]string{testkit.ScreenWith("❯ ")}, 0, false},
	} {
		m := testkit.UseAgents(t)
		m.Started = []string{testkit.AgentOf("GH-1")}
		m.Screens = c.screens

		note, err := launch.PromptChecked(t.Context(), testkit.FwdWT, "GH-1", "", testkit.SentText)
		if err != nil {
			t.Fatal(err)
		}

		if len(m.Enters) != c.enters || (note != "") != c.note {
			t.Errorf("%s: %d Enters, note %q", name, len(m.Enters), note)
		}
	}
}
