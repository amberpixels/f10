package lore

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseAnchor(t *testing.T) {
	cases := []struct {
		in   string
		want Anchor
		err  bool
	}{
		{in: "a/b.go:12", want: Anchor{Path: "a/b.go", Line: 12}},
		{in: "a/b.go:12-30", want: Anchor{Path: "a/b.go", Line: 12, EndLine: 30}},
		{in: "a/b.go:12@abc1234", want: Anchor{Path: "a/b.go", Line: 12, Commit: "abc1234"}},
		{
			in:   "a/b.go:12-30@abc1234#Allow",
			want: Anchor{Path: "a/b.go", Line: 12, EndLine: 30, Commit: "abc1234", Symbol: "Allow"},
		},
		{in: "./a/b.go:3#(*T).M", want: Anchor{Path: "a/b.go", Line: 3, Symbol: "(*T).M"}},
		{in: "a/b.go", err: true},
		{in: "a/b.go:0", err: true},
		{in: "a/b.go:30-12", err: true},
		{in: "a/b.go:x", err: true},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseAnchor(c.in)
			if (err != nil) != c.err {
				t.Fatalf("err = %v, want error %v", err, c.err)
			}

			if !c.err && got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}

			if !c.err && c.want.Commit != "" && got.String() != strings.TrimPrefix(c.in, "./") {
				t.Errorf("String() = %q, want %q", got.String(), c.in)
			}
		})
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	e := Entry{
		ID: "01M5CCX950248H248H248H248H", Kind: KindDecision, Change: "FT-1", Human: "dev@example.com",
		Agent: &Agent{Model: "m", Via: "claude-code"}, At: at("2026-10-20T10:15:00Z"),
		Anchors:  []Anchor{anchorAt("api/limit.go", 12, 30, "a1b2c3d", "Allow")},
		Chosen:   "token bucket",
		Rejected: []Rejected{{Option: "fixed window", Reason: "edge bursts"}},
		Body:     "A bucket per key.\n\nSecond paragraph.",
	}

	data, err := Marshal(e)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Parse(e.FileName(), data)
	if err != nil {
		t.Fatal(err)
	}

	got.file = ""
	if !reflect.DeepEqual(got, e) {
		t.Errorf("round trip:\n got  %+v\n want %+v\n%s", got, e, data)
	}
}

func TestParseRejects(t *testing.T) {
	const good = "---\nkind: intent\nchange: FT-1\nhuman: a@b.c\nat: 2026-10-20T09:00:00Z\n---\n\nbody\n"

	cases := []struct {
		name, file, data string
	}{
		{"not an entry name", "notes.md", good},
		{"no ulid", "FT-1-intent.md", good},
		{"no frontmatter", "01M5C8KYM0248H248H248H248H-intent.md", "just text\n"},
		{
			"unknown field",
			"01M5C8KYM0248H248H248H248H-intent.md",
			strings.Replace(good, "human:", "owner: x\nhuman:", 1),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.file, []byte(c.data)); err == nil {
				t.Error("parsed")
			}
		})
	}

	if _, err := Parse("FT-1.md", []byte(good)); !errors.Is(err, ErrBadFileName) {
		t.Errorf("err = %v, want ErrBadFileName", err)
	}
}
