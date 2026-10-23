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

const stateFixtures = "testdata/state"

// Every .md in the fixture directory parses to its .json partner; a
// partner holding `null` means no state.
func TestParseStateFixtures(t *testing.T) {
	bodies, err := filepath.Glob(filepath.Join(stateFixtures, "*.md"))
	if err != nil || len(bodies) == 0 {
		t.Fatalf("fixtures: %v, %d files", err, len(bodies))
	}

	for _, body := range bodies {
		name := strings.TrimSuffix(filepath.Base(body), ".md")

		t.Run(name, func(t *testing.T) {
			in, err := os.ReadFile(body)
			if err != nil {
				t.Fatal(err)
			}

			golden, err := os.ReadFile(strings.TrimSuffix(body, ".md") + ".json")
			if err != nil {
				t.Fatalf("no golden partner: %v", err)
			}

			got, ok := ParseState(string(in))

			if strings.TrimSpace(string(golden)) == "null" {
				if ok {
					t.Errorf("parsed %+v, want no state", got)
				}

				return
			}

			var want State
			if err := json.Unmarshal(golden, &want); err != nil {
				t.Fatal(err)
			}

			if !ok || !reflect.DeepEqual(got, want) {
				t.Errorf("parsed %+v (%v), want %+v", got, ok, want)
			}
		})
	}
}

// RenderState then ParseState is the identity for every fixture state.
func TestRenderStateRoundTrip(t *testing.T) {
	goldens, _ := filepath.Glob(filepath.Join(stateFixtures, "*.json"))

	for _, golden := range goldens {
		data, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}

		if strings.TrimSpace(string(data)) == "null" {
			continue
		}

		var s State
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatal(err)
		}

		out, err := RenderState(s)
		if err != nil {
			t.Fatalf("%s: %v", golden, err)
		}

		if got, ok := ParseState(out); !ok || !reflect.DeepEqual(got, s) {
			t.Errorf("%s: round trip = %+v (%v), want %+v", golden, got, ok, s)
		}
	}
}

func TestRenderStateBytes(t *testing.T) {
	cases := []struct {
		in   State
		want string
	}{
		{State{Task: "GH-12", Step: "pr"}, `<!-- state {"v":1,"task":"GH-12","step":"pr"} -->`},
		{State{Step: "pr"}, `<!-- state {"v":1,"step":"pr"} -->`},
		{
			State{
				Task:   "GH-12",
				Riders: []string{"GH-13"},
				Step:   "resolve",
				Round:  1,
				Open:   2,
				Asks:   1,
				After:  "GH-11",
			},
			`<!-- state {"v":1,"task":"GH-12","riders":["GH-13"],"step":"resolve","round":1,"open":2,"asks":1,"after":"GH-11"} -->`,
		},
	}

	for _, c := range cases {
		got, err := RenderState(c.in)
		if err != nil || got != c.want {
			t.Errorf("RenderState(%+v) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestRenderStateRefuses(t *testing.T) {
	cases := []struct {
		in  State
		err error
	}{
		{State{Step: ""}, ErrBadStep},
		{State{Step: "Pr"}, ErrBadStep},
		{State{Step: "pr", Task: "GH 1"}, ErrBadTask},
		{State{Step: "pr", Riders: []string{"GH-2", "x -->"}}, ErrBadTask},
		{State{Step: "pr", After: "GH 1"}, ErrBadAfter},
		{State{Step: "pr", Open: -1}, ErrBadCount},
	}

	for _, c := range cases {
		if _, err := RenderState(c.in); !errors.Is(err, c.err) {
			t.Errorf("RenderState(%+v) = %v, want %v", c.in, err, c.err)
		}
	}
}

func TestMerge(t *testing.T) {
	prev := State{V: 1, Task: "GH-1", Step: "review", Round: 1, Open: 3, Asks: 1, After: "GH-0"}

	cases := []struct {
		name string
		prev State
		u    Update
		want State
	}{
		{
			"counts carry, the rest is the update's",
			prev,
			Update{Step: "pr", Task: "GH-1"},
			State{V: 1, Task: "GH-1", Step: "pr", Round: 1, Open: 3, Asks: 1},
		},
		{
			"a passed count replaces, zero clears",
			prev,
			Update{Step: "resolve", Task: "GH-1", After: "GH-0", Open: new(0), Asks: new(0)},
			State{V: 1, Task: "GH-1", Step: "resolve", Round: 1, After: "GH-0"},
		},
		{
			"no prior state carries nothing",
			State{},
			Update{Step: "review", Task: "GH-1", Riders: []string{"GH-2"}, Round: new(2)},
			State{V: 1, Task: "GH-1", Riders: []string{"GH-2"}, Step: "review", Round: 2},
		},
	}

	for _, c := range cases {
		if got := Merge(c.prev, c.u); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestPutState(t *testing.T) {
	pr := State{Task: "GH-1", Step: "pr"}
	block := `<!-- state {"v":1,"task":"GH-1","step":"pr"} -->`

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"appended after a blank line",
			"Adds the thing.\n\nCloses #1\n",
			"Adds the thing.\n\nCloses #1\n\n" + block + "\n",
		},
		{"appended to a body with no newline", "Adds the thing.", "Adds the thing.\n\n" + block + "\n"},
		{"an empty body is the block", "", block + "\n"},
		{
			"replaced in place",
			"## Summary\n\n<!-- state {\"v\":1,\"task\":\"GH-1\",\"step\":\"review\",\"open\":2} -->\n\nAdds the thing.\n",
			"## Summary\n\n" + block + "\n\nAdds the thing.\n",
		},
		{
			"two blocks collapse to the first",
			"<!-- state {\"v\":1,\"step\":\"review\"} -->\n\nAdds the thing.\n\n<!-- state {\"v\":1,\"step\":\"pr\"} -->\n",
			block + "\n\nAdds the thing.\n",
		},
		{
			"a malformed block is still the one replaced",
			"Adds the thing.\n\n<!-- state {nope} -->\n",
			"Adds the thing.\n\n" + block + "\n",
		},
		{
			"a fenced example is text, the block goes after it",
			"The shape:\n\n```\n<!-- state {\"v\":1,\"step\":\"review\",\"open\":9} -->\n```\n",
			"The shape:\n\n```\n<!-- state {\"v\":1,\"step\":\"review\",\"open\":9} -->\n```\n\n" + block + "\n",
		},
		{
			"a backtick span is text",
			"Write `<!-- state {\"v\":1,\"step\":\"x\"} -->` by hand.\n",
			"Write `<!-- state {\"v\":1,\"step\":\"x\"} -->` by hand.\n\n" + block + "\n",
		},
	}

	for _, c := range cases {
		got, err := PutState(c.body, pr)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestCarryState(t *testing.T) {
	held := "Old.\n\n<!-- state {\"v\":1,\"step\":\"judge\",\"round\":1} -->\n"
	raw := `<!-- state {"v":1,"step":"judge","round":1} -->`

	cases := []struct {
		name string
		body string
		from string
		want string
	}{
		{"nothing held, nothing added", "New.\n", "Old.\n", "New.\n"},
		{"held block appended as it is", "New.\n", held, "New.\n\n" + raw + "\n"},
		{
			"a draft's own block is replaced",
			"New.\n\n<!-- state {\"v\":1,\"step\":\"pr\"} -->\n", held,
			"New.\n\n" + raw + "\n",
		},
		{
			"a malformed block is carried untouched",
			"New.\n", "Old.\n\n<!-- state {nope} -->\n",
			"New.\n\n<!-- state {nope} -->\n",
		},
		{"a fenced example is not a block", "New.\n", "```\n" + raw + "\n```\n", "New.\n"},
	}

	for _, c := range cases {
		if got := CarryState(c.body, c.from); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRewriteCarriesTheCounts(t *testing.T) {
	body := "Adds the thing.\n\n<!-- state {\"v\":1,\"task\":\"GH-1\",\"step\":\"review\",\"round\":1,\"open\":2} -->\n"

	got, err := Rewrite(body, Update{Step: "judge", Task: "GH-1", After: "GH-0"})
	want := "Adds the thing.\n\n<!-- state {\"v\":1,\"task\":\"GH-1\",\"step\":\"judge\",\"round\":1,\"open\":2,\"after\":\"GH-0\"} -->\n"

	if err != nil || got != want {
		t.Errorf("%q, %v; want %q", got, err, want)
	}

	got, err = Rewrite(got, Update{Step: "resolve", Task: "GH-1", Open: new(0)})
	want = "Adds the thing.\n\n<!-- state {\"v\":1,\"task\":\"GH-1\",\"step\":\"resolve\",\"round\":1} -->\n"

	if err != nil || got != want {
		t.Errorf("%q, %v; want %q", got, err, want)
	}
}

func TestStripState(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"none", "Adds the thing.\n", "Adds the thing.\n"},
		{
			"at the end",
			"Adds the thing.\n\nCloses #1\n\n<!-- state {\"v\":1,\"step\":\"pr\"} -->\n",
			"Adds the thing.\n\nCloses #1\n",
		},
		{
			"mid-body",
			"## Summary\n\n<!-- state {\"v\":1,\"step\":\"pr\"} -->\n\nAdds the thing.\n",
			"## Summary\n\nAdds the thing.\n",
		},
		{"crlf", "Adds the thing.\r\n\r\n<!-- state {\"v\":1,\"step\":\"pr\"} -->\r\n", "Adds the thing.\r\n"},
		{"inline with text", "Adds <!-- state {\"v\":1,\"step\":\"pr\"} --> the thing.\n", "Adds  the thing.\n"},
		{
			"two",
			"<!-- state {\"v\":1,\"step\":\"a\"} -->\n\nAdds.\n\n<!-- state {\"v\":1,\"step\":\"b\"} -->\n",
			"Adds.\n",
		},
		{
			"fenced stays",
			"```\n<!-- state {\"v\":1,\"step\":\"a\"} -->\n```\n\n<!-- state {\"v\":1,\"step\":\"b\"} -->\n",
			"```\n<!-- state {\"v\":1,\"step\":\"a\"} -->\n```\n",
		},
	}

	for _, c := range cases {
		if got := StripState(c.body); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
