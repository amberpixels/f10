package taskid

import "testing"

// Each reader's answer on the edges where the three prefixes differ.
func TestReaders(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		token                string
		parse, word, declare bool
	}{
		{"FT-12", true, true, false},
		{"ft-12", true, true, false},
		{"K8S-12", true, true, false},
		{"k8s-12", true, false, false},
		{"x86-64", true, false, false},
		{"X-1", true, true, false},
		{"#12", true, false, false},
		{"12", true, false, false},
		{"FT-", false, false, false},
	} {
		if _, _, ok := Parse(c.token); ok != c.parse {
			t.Errorf("Parse(%q) = %v, want %v", c.token, ok, c.parse)
		}

		if got := IsWord(c.token); got != c.word {
			t.Errorf("IsWord(%q) = %v, want %v", c.token, got, c.word)
		}
	}

	for prefix, want := range map[string]bool{"FT": true, "K8S": true, "X": false, "ft": false, "ABCDEFGHIJK": false, "1A": false} {
		if got := ValidPrefix(prefix); got != want {
			t.Errorf("ValidPrefix(%q) = %v, want %v", prefix, got, want)
		}
	}

	if p, n, _ := Parse("ft-12"); p != "ft" || n != "12" {
		t.Errorf("Parse(ft-12) = %q %q", p, n)
	}
}

// A request number reads the same from gh's #n, glab's !n and a bare n.
func TestRequestNumber(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]int{
		"#12": 12, "!12": 12, "12": 12, "#7": 7,
		"none": 0, "": 0, "#": 0, "#!12": 0, "FT-12": 0,
	} {
		if got := RequestNumber(text); got != want {
			t.Errorf("RequestNumber(%q) = %d, want %d", text, got, want)
		}
	}
}
