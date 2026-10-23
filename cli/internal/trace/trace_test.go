package trace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixtures = "testdata/trailers"

// Every .msg in the fixture directory parses to its .json partner; a .msg
// without one fails, since the pair is the spec's conformance suite.
func TestParseFixtures(t *testing.T) {
	msgs, err := filepath.Glob(filepath.Join(fixtures, "*.msg"))
	if err != nil || len(msgs) == 0 {
		t.Fatalf("fixtures: %v, %d files", err, len(msgs))
	}

	for _, msg := range msgs {
		name := strings.TrimSuffix(filepath.Base(msg), ".msg")

		t.Run(name, func(t *testing.T) {
			in, err := os.ReadFile(msg)
			if err != nil {
				t.Fatal(err)
			}

			golden, err := os.ReadFile(strings.TrimSuffix(msg, ".msg") + ".json")
			if err != nil {
				t.Fatalf("no golden partner: %v", err)
			}

			var want Parsed
			if err := json.Unmarshal(golden, &want); err != nil {
				t.Fatal(err)
			}

			if want.Review == nil && len(want.Reviews) > 0 {
				t.Fatal("golden lists reviews without a last review")
			}

			got := Parse(string(in))
			if !reflect.DeepEqual(got, want) {
				g, _ := json.Marshal(got)
				t.Errorf("parsed\n%s\nwant\n%s", g, strings.TrimSpace(string(golden)))
			}
		})
	}
}

// Render then Parse is the identity for every fixture value that has a
// single step, which is what one commit carries.
func TestRenderRoundTrip(t *testing.T) {
	goldens, _ := filepath.Glob(filepath.Join(fixtures, "*.json"))

	for _, golden := range goldens {
		data, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}

		var p Parsed
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}

		if p.Zero() || len(p.Steps) > 1 {
			continue
		}

		out, err := Render(p.Trailers)
		if err != nil {
			t.Fatalf("%s: %v", golden, err)
		}

		if got := Parse(out).Trailers; !reflect.DeepEqual(got, p.Trailers) {
			t.Errorf("%s: round trip = %+v, want %+v", golden, got, p.Trailers)
		}
	}
}

func TestRenderBytes(t *testing.T) {
	cases := []struct {
		in   Trailers
		want string
	}{
		{Trailers{}, ""},
		{Trailers{Tasks: []string{"GH-12"}, Step: "implement"}, "Task: GH-12\nStep: implement\n"},
		{Trailers{Tasks: []string{"GH-12", "GH-13"}}, "Task: GH-12\nTask: GH-13\n"},
		{Trailers{Step: "catchup"}, "Step: catchup\n"},
		{
			Trailers{Tasks: []string{"GH-12"}, Step: "resolve", Review: &Review{Round: 2, Fixed: 1}},
			"Task: GH-12\nStep: resolve\nReview: round=2 fixed=1 skipped=0\n",
		},
	}

	for _, c := range cases {
		got, err := Render(c.in)
		if err != nil || got != c.want {
			t.Errorf("Render(%+v) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestRenderRefuses(t *testing.T) {
	cases := []struct {
		in  Trailers
		err error
	}{
		{Trailers{Step: "Implement"}, ErrBadStep},
		{Trailers{Step: "two words"}, ErrBadStep},
		{Trailers{Tasks: []string{"GH 12"}}, ErrBadTask},
		{Trailers{Tasks: []string{""}}, ErrBadTask},
		{Trailers{Review: &Review{Round: -1}}, ErrBadReview},
	}

	for _, c := range cases {
		if _, err := Render(c.in); !errors.Is(err, c.err) {
			t.Errorf("Render(%+v) = %v, want %v", c.in, err, c.err)
		}
	}
}

// Nothing that reaches the host names f10: not a fixture, not a rendered line.
func TestNothingNamesF10(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(fixtures, "*"))
	for _, f := range files {
		data, _ := os.ReadFile(f)
		if strings.Contains(strings.ToLower(string(data)), "f10") {
			t.Errorf("%s names f10", f)
		}
	}

	out, _ := Render(Trailers{Tasks: []string{"GH-1"}, Step: "resolve", Review: &Review{Round: 1}})
	if strings.Contains(strings.ToLower(out), "f10") {
		t.Errorf("rendered output names f10: %q", out)
	}
}
