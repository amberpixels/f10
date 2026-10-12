package model

import "testing"

func TestResolve(t *testing.T) {
	tests := []struct {
		raw, value, note string
		ok               bool
	}{
		{raw: "opus", value: "opus", ok: true},
		{raw: "Opus", value: "opus", ok: true},
		{raw: " fable ", value: "fable", ok: true},
		{raw: "fabel", value: "fable", note: "--model fabel read as fable", ok: true},
		{raw: "sonet", value: "sonnet", note: "--model sonet read as sonnet", ok: true},
		{raw: "opus5-5", value: "claude-opus-5-5", note: "--model opus5-5 read as claude-opus-5-5", ok: true},
		{raw: "Opus 5.5", value: "claude-opus-5-5", note: "--model Opus 5.5 read as claude-opus-5-5", ok: true},
		{raw: "opus-5.5", value: "claude-opus-5-5", note: "--model opus-5.5 read as claude-opus-5-5", ok: true},
		{raw: "sonnet5", value: "claude-sonnet-5", note: "--model sonnet5 read as claude-sonnet-5", ok: true},
		{raw: "claude-opus-5-5", value: "claude-opus-5-5", ok: true},
		{raw: "claude-newfamily-1", value: "claude-newfamily-1", ok: true},
		{raw: "Claude-Opus-5-5", value: "Claude-Opus-5-5", ok: true},
		{raw: "opus-9-9", value: "claude-opus-9-9", note: "--model opus-9-9 read as claude-opus-9-9", ok: true},
		{raw: "xyz"},
		{raw: "gpt-4o"},
		{raw: "opus sonnet"},
		{raw: "5.5"},
		{raw: "opus/5"},
		{raw: ""},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			value, note, ok := Resolve(tt.raw)
			if value != tt.value || note != tt.note || ok != tt.ok {
				t.Errorf("Resolve(%q) = %q, %q, %v; want %q, %q, %v",
					tt.raw, value, note, ok, tt.value, tt.note, tt.ok)
			}
		})
	}
}

func TestNearestTie(t *testing.T) {
	saved := Families
	t.Cleanup(func() { Families = saved })

	Families = []string{"abcd", "abce"}

	if f, ok := nearest("abc"); ok {
		t.Errorf("nearest(abc) = %q; want a tie, no match", f)
	}
}

func TestDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"fable", "fable", 0},
		{"fabel", "fable", 1},
		{"sonet", "sonnet", 1},
		{"opsu", "opus", 1},
		{"xyz", "opus", 4},
	}

	for _, tt := range tests {
		if got := distance(tt.a, tt.b); got != tt.want {
			t.Errorf("distance(%q, %q) = %d; want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
