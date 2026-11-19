package reflink_test

import (
	"reflect"
	"testing"

	"github.com/amberpixels/f10/cli/internal/reflink"
)

// known resolves FT-1, FT-2 and FT-58; any other id is unknown.
func known(id string) (string, string, bool) {
	switch id {
	case "FT-1":
		return "One [x]", "https://t/1", true
	case "FT-2":
		return "Two", "https://t/2", true
	case "FT-58":
		return "Fifty-eight", "https://t/58", true
	}

	return "", "", false
}

const (
	one = "[FT-1: One \\[x\\]](https://t/1)"
	two = "[FT-2: Two](https://t/2)"
)

func TestMarkdown(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"first mention only", "FT-1 then FT-1 again", one + " then FT-1 again"},
		{"each id once", "FT-1 and FT-2, FT-2", one + " and " + two + ", FT-2"},
		{"any case, declared form", "see ft-2.", "see " + two + "."},
		{"longer number is another id", "FT-58 not FT-5", "[FT-58: Fifty-eight](https://t/58) not FT-5"},
		{"unknown id stays bare", "FT-9 and FT-1", "FT-9 and " + one},
		{"already linked counts as first", "[FT-2: Two](https://t/2), then FT-2", "[FT-2: Two](https://t/2), then FT-2"},
		{"reference link counts", "[FT-2][r] then FT-2\n\n[r]: https://t/2", "[FT-2][r] then FT-2\n\n[r]: https://t/2"},
		{"anchor counts", `<a href="https://t/2">FT-2</a> then FT-2`, `<a href="https://t/2">FT-2</a> then FT-2`},
		{"inline code", "`FT-1` then FT-1", "`FT-1` then " + one},
		{"double backtick code", "``a ` FT-1`` FT-2", "``a ` FT-1`` " + two},
		{"unmatched backtick is literal", "a ` FT-1", "a ` " + one},
		{"fenced block", "```\nFT-1\n```\nFT-1", "```\nFT-1\n```\n" + one},
		{"tilde fence", "~~~go\nFT-1\n~~~\nFT-2", "~~~go\nFT-1\n~~~\n" + two},
		{"unclosed fence runs to the end", "```\nFT-1\nFT-2", "```\nFT-1\nFT-2"},
		{"indented block", "para\n\n    FT-1\n\nFT-1", "para\n\n    FT-1\n\n" + one},
		{"nested list item is prose", "- a\n\n    - FT-1", "- a\n\n    - " + one},
		{"state block", "<!-- state {\"task\":\"FT-1\"} -->\nFT-1", "<!-- state {\"task\":\"FT-1\"} -->\n" + one},
		{"after line", "After: FT-1\nFT-1", "After: FT-1\n" + one},
		{"trailers", "Task: FT-1\nStep: implement\nFT-2", "Task: FT-1\nStep: implement\n" + two},
		{"closing keyword", "Closes FT-1\nPart of #3, FT-2", "Closes FT-1\nPart of #3, FT-2"},
		{"prose opening with a keyword", "Fix the bug FT-1", "Fix the bug " + one},
		{"url", "https://x/issues/FT-1 and FT-1", "https://x/issues/FT-1 and " + one},
		{"path", "see .f10/plans/FT-1.md and FT-1", "see .f10/plans/FT-1.md and " + one},
		{"file name", "FT-1.md", "FT-1.md"},
		{"branch", "FT-1/link-it and FT-1-2 then FT-1", "FT-1/link-it and FT-1-2 then " + one},
		{"bundle", "FT-1+FT-2", "FT-1+FT-2"},
		{"inside a word", "LEFT-1 xFT-1", "LEFT-1 xFT-1"},
		{"end of sentence", "Fixed in FT-1.", "Fixed in " + one + "."},
		{"parenthesised", "(FT-1)", "(" + one + ")"},
		{"reference definition", "See [the base][b].\n\n[b]: https://t/1 \"FT-1\"", "See [the base][b].\n\n[b]: https://t/1 \"FT-1\""},
		{"bracketed label", "See [FT-1].\n\n[FT-1]: https://t/1\n\nFT-2", "See [FT-1].\n\n[FT-1]: https://t/1\n\n" + two},
		{"image alt text", "![FT-1 before](img/before.png)\n\nShips FT-1.", "![FT-1 before](img/before.png)\n\nShips " + one + "."},
		{"title after a colon", "FT-2: two, then", two + ", then"},
		{"title in parentheses", "FT-2 (Two) ships", two + " ships"},
		{"another text after a colon", "FT-2: done", two + ": done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := reflink.Markdown(tc.in, "FT", known); got != tc.want {
				t.Errorf("Markdown(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// The mentions name each first bare one with its line, linked or not, and
// None only reports.
func TestMarkdownMentions(t *testing.T) {
	in := "intro\n`FT-1`\nFT-2 and FT-9\nFT-1, FT-2\n"

	_, got := reflink.Markdown(in, "FT", known)

	want := []reflink.Mention{{"FT-2", 3, true}, {"FT-9", 3, false}, {"FT-1", 4, true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mentions = %+v, want %+v", got, want)
	}

	out, got := reflink.Markdown(in, "FT", reflink.None)
	if out != in || len(got) != 3 || got[0].Linked {
		t.Errorf("None = %q, %+v; want the text unchanged and three bare mentions", out, got)
	}
}

func TestNoPrefixLinksNothing(t *testing.T) {
	if got, ms := reflink.Markdown("FT-1 #2", "", known); got != "FT-1 #2" || ms != nil {
		t.Errorf("no prefix = %q, %v", got, ms)
	}
}

func TestHTML(t *testing.T) {
	in := `<html><head><title>FT-1 report</title></head><body>` +
		`<h1 data-id="FT-1">FT-1: One [x] shipped</h1><code>FT-2</code><pre>FT-2</pre>` +
		`<a href="https://t/2">FT-2</a><p>FT-2 and FT-1</p><!-- FT-58 --><script>x="FT-58"</script></body></html>`
	want := `<html><head><title>FT-1 report</title></head><body>` +
		`<h1 data-id="FT-1"><a href="https://t/1">FT-1: One [x]</a> shipped</h1><code>FT-2</code><pre>FT-2</pre>` +
		`<a href="https://t/2">FT-2</a><p>FT-2 and FT-1</p><!-- FT-58 --><script>x="FT-58"</script></body></html>`

	if got, _ := reflink.HTML(in, "FT", known); got != want {
		t.Errorf("HTML\n got %q\nwant %q", got, want)
	}

	if got := reflink.HTMLLink("FT-3", `a <b> & "c"`, "https://t/3?a=1&b=2"); got !=
		`<a href="https://t/3?a=1&amp;b=2">FT-3: a &lt;b&gt; &amp; &#34;c&#34;</a>` {
		t.Errorf("HTMLLink = %q", got)
	}
}
