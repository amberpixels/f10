package agentctl

import (
	"strings"
	"testing"
)

func TestParseProfile(t *testing.T) {
	cases := []struct {
		raw  string
		want Profile
		err  string
	}{
		{raw: "", want: Profile{}},
		{raw: "claude:opus:high", want: Profile{Kind: "claude", Model: "opus", Effort: "high"}},
		{raw: "Claude:opus:HIGH", want: Profile{Kind: "claude", Model: "opus", Effort: "high"}},
		{raw: "opus:medium", want: Profile{Model: "opus", Effort: "medium"}},
		{raw: "sonnet", want: Profile{Model: "sonnet"}},
		{raw: "low", want: Profile{Effort: "low"}},
		{raw: "claude-opus-5-5:max", want: Profile{Model: "claude-opus-5-5", Effort: "max"}},
		{raw: "opus:", err: "empty segment"},
		{raw: ":high", err: "empty segment"},
		{raw: "opencode:opus:high", err: "opencode is not an agent kind (claude)"},
		{raw: "opus:huge", err: "huge is not an effort claude knows (low, medium, high, xhigh, max)"},
		{raw: "a:b:c:d", err: "more than three segments"},
	}

	for _, c := range cases {
		got, err := ParseProfile(c.raw)

		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("ParseProfile(%q) err = %v, want %q", c.raw, err, c.err)
			}
		case err != nil:
			t.Errorf("ParseProfile(%q): %v", c.raw, err)
		case got != c.want:
			t.Errorf("ParseProfile(%q) = %+v, want %+v", c.raw, got, c.want)
		}
	}
}

func TestProfileOver(t *testing.T) {
	under := Profile{Kind: "claude", Model: "opus", Effort: "high"}

	cases := []struct {
		name string
		over Profile
		want Profile
	}{
		{name: "nothing", over: Profile{}, want: under},
		{
			name: "effort alone", over: Profile{Effort: "low"},
			want: Profile{Kind: "claude", Model: "opus", Effort: "low"},
		},
		{
			name: "model alone", over: Profile{Model: "sonnet"},
			want: Profile{Kind: "claude", Model: "sonnet", Effort: "high"},
		},
		{
			name: "all", over: Profile{Kind: "claude", Model: "haiku", Effort: "max"},
			want: Profile{Kind: "claude", Model: "haiku", Effort: "max"},
		},
	}

	for _, c := range cases {
		if got := c.over.Over(under); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}

	if got := (Profile{Effort: "low"}).Over(Profile{}); got != (Profile{Kind: "claude", Effort: "low"}) {
		t.Errorf("over nothing names the default kind: %+v", got)
	}
}

func TestProfileResolve(t *testing.T) {
	cases := []struct {
		raw        string
		want       Profile
		note       string
		recognized bool
	}{
		{raw: "opus:high", want: Profile{Kind: "claude", Model: "opus", Effort: "high"}, recognized: true},
		{raw: "fabel", want: Profile{Kind: "claude", Model: "fable"}, note: "read as fable", recognized: true},
		{
			raw: "opus-5.5:low", want: Profile{Kind: "claude", Model: "claude-opus-5-5", Effort: "low"},
			note: "read as", recognized: true,
		},
		{raw: "xyz:low", want: Profile{Kind: "claude", Effort: "low"}},
		{raw: "high", want: Profile{Kind: "claude", Effort: "high"}, recognized: true},
	}

	for _, c := range cases {
		p, err := ParseProfile(c.raw)
		if err != nil {
			t.Fatal(err)
		}

		got, note, ok := p.Resolve()
		if got != c.want || ok != c.recognized || !strings.Contains(note, c.note) || (c.note == "" && note != "") {
			t.Errorf("%q resolves to %+v, %q, %v; want %+v, %q, %v", c.raw, got, note, ok, c.want, c.note, c.recognized)
		}
	}
}

func TestProfileRenderings(t *testing.T) {
	cases := []struct {
		p                   Profile
		args, str, short    string
		toolModel, toolEffo string
		isDefault           bool
	}{
		{p: Profile{}, args: "", str: "claude::", short: "", isDefault: true},
		{
			p: Profile{Kind: "claude", Model: "opus", Effort: "high"}, args: "--model opus --effort high",
			str: "claude:opus:high", short: "opus:high", toolModel: "opus", toolEffo: "high",
		},
		{
			p: Profile{Model: "claude-opus-5-5"}, args: "--model claude-opus-5-5",
			str: "claude:claude-opus-5-5:", short: "claude-opus-5-5", toolModel: "opus",
		},
		{p: Profile{Effort: "low"}, args: "--effort low", str: "claude::low", short: "low", toolEffo: "low"},
		{
			p: Profile{Model: "claude-mystery-1"}, args: "--model claude-mystery-1",
			str: "claude:claude-mystery-1:", short: "claude-mystery-1",
		},
	}

	for _, c := range cases {
		if got := strings.Join(c.p.Args(), " "); got != c.args {
			t.Errorf("%+v Args = %q, want %q", c.p, got, c.args)
		}

		if got := c.p.String(); got != c.str {
			t.Errorf("%+v String = %q, want %q", c.p, got, c.str)
		}

		if got := c.p.Short(); got != c.short {
			t.Errorf("%+v Short = %q, want %q", c.p, got, c.short)
		}

		if m, e := c.p.Tool(); m != c.toolModel || e != c.toolEffo {
			t.Errorf("%+v Tool = %q, %q; want %q, %q", c.p, m, e, c.toolModel, c.toolEffo)
		}

		if c.p.IsDefault() != c.isDefault {
			t.Errorf("%+v IsDefault = %v", c.p, c.p.IsDefault())
		}

		if back := ParseRecorded(c.p.String()); back != c.p.Over(Profile{}) {
			t.Errorf("%+v does not read back from %q: %+v", c.p, c.p.String(), back)
		}
	}

	if got := ParseRecorded("opus"); got != (Profile{Model: "opus"}) {
		t.Errorf("an old model record reads as %+v", got)
	}
}
