package verdict

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want File
		idea bool
	}{
		{
			"ship run with a decision",
			`task: FT-76
subject: plan FT-76
verdict: proceed with changes
reason: two changes fold in
run: ship before pr
blind: yes
decision: continue

## Changes

1. Scope the prior rows.

## Argument

verdict: stop would be wrong here.
`,
			File{
				Task: "FT-76", Subject: "plan FT-76", Verdict: "proceed with changes", Reason: "two changes fold in",
				Run: "ship before pr", Blind: true, Decision: "continue",
			},
			false,
		},
		{
			"idea",
			"task:\nsubject: idea rate limit the public API\nverdict: stop\nreason: the gateway does it\nrun: standalone\n",
			File{Subject: "idea rate limit the public API", Verdict: "stop", Reason: "the gateway does it", Run: "standalone"},
			true,
		},
		{
			"each verdict: rethink",
			"subject: pr 12\nverdict: rethink\n",
			File{Subject: "pr 12", Verdict: "rethink"},
			false,
		},
		{
			"each verdict: proceed",
			"subject: range a..b\nverdict: proceed\n",
			File{Subject: "range a..b", Verdict: "proceed"},
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Parse(tc.text)
			if err != nil {
				t.Fatal(err)
			}

			if *f != tc.want {
				t.Errorf("got %+v\nwant %+v", *f, tc.want)
			}

			if f.Idea() != tc.idea {
				t.Errorf("Idea() = %v", f.Idea())
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want error
	}{
		{"no verdict", "task: FT-1\nsubject: task FT-1\n", ErrNoVerdict},
		{"verdict only in the body", "task: FT-1\n\n## Argument\n\nverdict: proceed\n", ErrNoVerdict},
		{"unknown verdict", "task: FT-1\nverdict: green\n", ErrBadVerdict},
		{"banner word, not the verdict", "task: FT-1\nverdict: PROCEED\n", ErrBadVerdict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.text); !errors.Is(err, tc.want) {
				t.Errorf("err %v, want %v", err, tc.want)
			}
		})
	}
}
