package ref

import "testing"

func TestBundleTitle(t *testing.T) {
	bundle := func(ids ...string) Ref { return Ref{ID: ids[0], Number: numberOf(ids[0]), Bundle: ids} }

	cases := []struct {
		name  string
		title string
		r     Ref
		want  string
	}{
		{"a task alone keeps its title", "ABC-1: Lead", Ref{ID: "ABC-1"}, "ABC-1: Lead"},
		{"one rider", "ABC-1: Lead", bundle("ABC-1", "ABC-2"), "ABC-1 (+ABC-2): Lead"},
		{"two riders", "ABC-1: Lead", bundle("ABC-1", "ABC-2", "ABC-3"), "ABC-1 (+ABC-2, ABC-3): Lead"},
		{
			"four riders name two and count the rest", "ABC-1: Lead",
			bundle("ABC-1", "ABC-2", "ABC-3", "ABC-4", "ABC-5"), "ABC-1 (+ABC-2, ABC-3, 2 more): Lead",
		},
		{
			"a tag already there is replaced", "ABC-1 (+ABC-9): Lead",
			bundle("ABC-1", "ABC-2"), "ABC-1 (+ABC-2): Lead",
		},
		{"no lead id prepends it", "Lead", bundle("ABC-1", "ABC-2"), "ABC-1 (+ABC-2): Lead"},
		{"the id matches in any case", "abc-1: Lead", bundle("ABC-1", "ABC-2"), "abc-1 (+ABC-2): Lead"},
		{
			"the lead never matches inside a longer id", "ABC-12 then ABC-1: Lead",
			bundle("ABC-1", "ABC-2"), "ABC-12 then ABC-1 (+ABC-2): Lead",
		},
		{
			"a rider's ref titles by its lead",
			"ABC-1: Lead",
			Ref{ID: "ABC-2", Bundle: []string{"ABC-1", "ABC-2"}},
			"ABC-1 (+ABC-2): Lead",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BundleTitle(tc.title, tc.r); got != tc.want {
				t.Errorf("BundleTitle(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

func TestTrimRiders(t *testing.T) {
	for in, want := range map[string]string{
		" (+ABC-2, ABC-3, 2 more): Lead": ": Lead",
		": Lead":                         ": Lead",
		" (not a tag): Lead":             " (not a tag): Lead",
	} {
		if got := TrimRiders(in); got != want {
			t.Errorf("TrimRiders(%q) = %q, want %q", in, got, want)
		}
	}
}
