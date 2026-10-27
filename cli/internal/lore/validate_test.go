package lore

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	anchor := anchorAt("a.go", 1, 0, "abcdef1", "")
	decision := Entry{
		Kind: KindDecision, Change: "FT-1", Human: "h@x", At: at("2026-10-20T09:00:00Z"),
		Chosen: "a", Rejected: []Rejected{{Option: "b", Reason: "slower"}}, Body: "why",
	}

	with := func(e Entry, edit func(*Entry)) Entry {
		edit(&e)

		return e
	}

	cases := []struct {
		name  string
		entry Entry
		want  string // a substring of a problem; "" means valid
	}{
		{"intent", intent("FT-1", "2026-10-20T09:00:00Z"), ""},
		{
			"intent with anchors",
			with(intent("FT-1", "2026-10-20T09:00:00Z"), func(e *Entry) { e.Anchors = []Anchor{anchor} }),
			"",
		},
		{
			"no human",
			with(intent("FT-1", "2026-10-20T09:00:00Z"), func(e *Entry) { e.Human = "" }),
			"human is required",
		},
		{
			"no at",
			with(intent("FT-1", "2026-10-20T09:00:00Z"), func(e *Entry) { e.At = at("0001-01-01T00:00:00Z") }),
			"at is required",
		},
		{
			"empty body",
			with(intent("FT-1", "2026-10-20T09:00:00Z"), func(e *Entry) { e.Body = " \n" }),
			"body is empty",
		},
		{"bad kind", with(intent("FT-1", "2026-10-20T09:00:00Z"), func(e *Entry) { e.Kind = "note" }), "kind \"note\""},
		{
			"bad change",
			with(intent("FT-1", "2026-10-20T09:00:00Z"), func(e *Entry) { e.Change = "../x" }),
			"not a usable change id",
		},
		{
			"bad supersedes",
			with(intent("FT-1", "2026-10-20T09:00:00Z"), func(e *Entry) { e.Supersedes = "x" }),
			"is not a ULID",
		},
		{
			"intent with chosen",
			with(intent("FT-1", "2026-10-20T09:00:00Z"), func(e *Entry) { e.Chosen = "a" }),
			"chosen is only for a decision",
		},

		{"decision", decision, ""},
		{"decision without chosen", with(decision, func(e *Entry) { e.Chosen = "" }), "chosen is required"},
		{
			"rejected without reason",
			with(decision, func(e *Entry) { e.Rejected = []Rejected{{Option: "b"}} }),
			"option and reason are required",
		},

		{"open finding", finding("FT-1", "2026-10-20T09:00:00Z", "major", "open", anchor), ""},
		{"fixed finding", finding("FT-1", "2026-10-20T09:00:00Z", "nit", "fixed"), ""},
		{
			"fixed finding without resolution",
			with(finding("FT-1", "2026-10-20T09:00:00Z", "minor", "fixed"), func(e *Entry) { e.Resolution = "" }),
			"resolution is required",
		},
		{"bad severity", finding("FT-1", "2026-10-20T09:00:00Z", "huge", "open"), "severity \"huge\""},
		{"bad status", finding("FT-1", "2026-10-20T09:00:00Z", "minor", "done"), "status \"done\""},
		{
			"finding with outcome",
			with(finding("FT-1", "2026-10-20T09:00:00Z", "minor", "open"), func(e *Entry) { e.Outcome = "stop" }),
			"outcome is only for a verdict",
		},

		{
			"remote finding",
			with(finding("FT-1", "2026-10-20T09:00:00Z", "minor", "open"), func(e *Entry) { e.Source = "remote" }),
			"",
		},
		{
			"bad source",
			with(finding("FT-1", "2026-10-20T09:00:00Z", "minor", "open"), func(e *Entry) { e.Source = "ci" }),
			"source \"ci\"",
		},
		{
			"verdict with source",
			with(verdictEntry("FT-1", "2026-10-20T09:00:00Z", "stop"), func(e *Entry) { e.Source = "local" }),
			"source is only for a finding",
		},

		{"verdict", verdictEntry("FT-1", "2026-10-20T09:00:00Z", "proceed with changes"), ""},
		{
			"plan verdict",
			with(verdictEntry("FT-1", "2026-10-20T09:00:00Z", "proceed"), func(e *Entry) { e.Judged = "plan" }),
			"",
		},
		{
			"task verdict",
			with(verdictEntry("FT-1", "2026-10-20T09:00:00Z", "stop"), func(e *Entry) { e.Judged = "task" }),
			"",
		},
		{
			"bad judged",
			with(verdictEntry("FT-1", "2026-10-20T09:00:00Z", "proceed"), func(e *Entry) { e.Judged = "pr" }),
			"judged \"pr\"",
		},
		{
			"finding with judged",
			with(finding("FT-1", "2026-10-20T09:00:00Z", "minor", "open"), func(e *Entry) { e.Judged = "change" }),
			"judged is only for a verdict",
		},
		{
			"verdict bad outcome",
			verdictEntry("FT-1", "2026-10-20T09:00:00Z", "proceed_with_changes"),
			"outcome \"proceed_with_changes\"",
		},
		{
			"verdict bad gate",
			with(verdictEntry("FT-1", "2026-10-20T09:00:00Z", "stop"), func(e *Entry) { e.Gate = "ci" }),
			"gate \"ci\"",
		},
		{
			"verdict without commit",
			with(verdictEntry("FT-1", "2026-10-20T09:00:00Z", "stop"), func(e *Entry) { e.Commit = "" }),
			"commit \"\" is not a sha",
		},

		{"lesson", lesson("FT-1", "2026-10-20T09:00:00Z", anchor), ""},
		{"lesson without anchor", lesson("FT-1", "2026-10-20T09:00:00Z"), "at least one anchor"},
		{
			"retired without supersedes",
			with(lesson("FT-1", "2026-10-20T09:00:00Z", anchor), func(e *Entry) { e.Retired = true }),
			"retired is only allowed",
		},
		{"retired decision", with(decision, func(e *Entry) { e.Retired = true }), "retired is only for a lesson"},

		{
			"anchor without commit",
			lesson("FT-1", "2026-10-20T09:00:00Z", anchorAt("a.go", 1, 0, "", "")),
			"commit \"\" is not a sha",
		},
		{
			"anchor absolute",
			lesson("FT-1", "2026-10-20T09:00:00Z", anchorAt("/a.go", 1, 0, "abcdef1", "")),
			"not a clean repo-relative path",
		},
		{
			"anchor escapes",
			lesson("FT-1", "2026-10-20T09:00:00Z", anchorAt("../a.go", 1, 0, "abcdef1", "")),
			"not a clean repo-relative path",
		},
		{
			"anchor end before line",
			lesson("FT-1", "2026-10-20T09:00:00Z", anchorAt("a.go", 5, 2, "abcdef1", "")),
			"end_line is before line",
		},
		{
			"anchor line zero",
			lesson("FT-1", "2026-10-20T09:00:00Z", anchorAt("a.go", 0, 0, "abcdef1", "")),
			"line runs from 1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.entry)
			if c.want == "" {
				if err != nil {
					t.Fatalf("invalid: %v", err)
				}

				return
			}

			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}

			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("problems %q lack %q", ve.Problems, c.want)
			}
		})
	}
}
