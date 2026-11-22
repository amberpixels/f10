package asks

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Nerd Font Material Design icons, the only range Claude Code keeps:
// md-chat_question heads a banner, md-inbox marks the inbox's counts and rows.
const (
	AskIcon   = "\U000F1738"
	InboxIcon = "\U000F0687"
)

// BannerWidth is the column the banner wraps at. It cannot read the width
// of the pane or of Claude Code's code block, so it picks one that fits both.
const BannerWidth = 76

// BannerInbox closes the footer: the other way to answer.
const BannerInbox = "/f10:inbox from the main session"

const (
	optionIndent = "  "
	contextLabel = "· "
)

// Banner renders a as plain text, the same questions, letters and default
// the walk draws, so what the user sees is what an answer resolves against.
// It carries no styles, because the hook prints it inside a code fence.
// name labels the task, such as project:task in a list spanning projects.
// Empty is the task id.
func Banner(a Ask, name string) string {
	task := a.Task
	if name != "" {
		task = name
	}

	header := AskIcon + " " + task
	if a.Skill != "" {
		header += " · asked at " + a.Skill
	}

	if !a.AskedAt.IsZero() {
		header += " · " + a.AskedAt.Local().Format("3:04 PM")
	}

	b := &bannerText{}
	b.line(header)
	b.line(strings.Repeat("─", BannerWidth-4))

	qs := ShownQuestions(a)

	b.context(ContextOf(a, qs))

	for i, q := range qs {
		if i > 0 || len(b.lines) > 2 {
			b.line("")
		}

		if i > 0 {
			b.context(Sentences(q.Context))
		}

		lead := ""
		if len(qs) > 1 {
			lead = strconv.Itoa(q.N) + ". "
		}

		switch {
		case q.Text != "":
			b.wrap(q.Text, lead, strings.Repeat(" ", len(lead)))
		case lead != "": // a question that was only its list
			b.line(strings.TrimSpace(lead))
		}

		lead = strings.Repeat(" ", len(lead))

		for j, o := range q.Options {
			words := wordsOf(o)
			if j == 0 {
				marked := wordsOf(Mark(q))
				marked[0].gap = 2
				words = append(words, marked...)
			}

			lead := lead + optionIndent + string(rune('a'+j)) + ") "
			b.wrapWords(words, lead, strings.Repeat(" ", len(lead)))
		}
	}

	b.line("")
	b.wrap(bannerFooter(qs), "", "")

	return strings.Join(b.lines, "\n") + "\n"
}

// bannerFooter tells the user what to type, by the shape of the questions.
// Only a reply in the asking agent's pane answers, wherever the banner shows.
func bannerFooter(qs []Shown) string {
	reply := "Reply"

	switch {
	case len(qs) > 1:
		reply += " " + replyExample(qs)
	case len(qs) == 1 && len(qs[0].Options) > 0:
		reply += " " + choices(len(qs[0].Options))
	}

	return reply + " in the asking pane · " + BannerInbox
}

func allOptions(qs []Shown) bool {
	for _, q := range qs {
		if len(q.Options) == 0 {
			return false
		}
	}

	return true
}

// replyExample is "1. a 2. b": question i picks option i, or the last, and
// a question without options takes "...".
func replyExample(qs []Shown) string {
	parts := make([]string, len(qs))
	for i, q := range qs {
		v := "..."
		if len(q.Options) > 0 {
			v = string(rune('a' + min(i, len(q.Options)-1)))
		}

		parts[i] = strconv.Itoa(q.N) + ". " + v
	}

	return strings.Join(parts, " ")
}

// choices is "a or b", or "a, b or c" for more.
func choices(n int) string {
	letters := make([]string, n)
	for i := range n {
		letters[i] = string(rune('a' + i))
	}

	if n == 1 {
		return letters[0]
	}

	return strings.Join(letters[:n-1], ", ") + " or " + letters[n-1]
}

// bannerText collects the banner's lines.
type bannerText struct{ lines []string }

func (b *bannerText) line(s string) { b.lines = append(b.lines, s) }

// context prints one sentence per line. Labelled sentences become rows
// with their labels in a column, as the walk draws them.
func (b *bannerText) context(sentences []string) {
	col := 0

	for _, s := range sentences {
		if label, _, ok := Labelled(s); ok {
			col = max(col, len(label))
		}
	}

	for _, s := range sentences {
		label, rest, ok := Labelled(s)
		if !ok {
			b.wrap(s, "", "")

			continue
		}

		lead := contextLabel + fmt.Sprintf("%-*s", col, strings.ToLower(label)) + "  "
		b.wrap(rest, lead, strings.Repeat(" ", utf8.RuneCountInString(lead)))
	}
}

// bannerWord is one word and the spaces before it on a line it does not open.
type bannerWord struct {
	text string
	gap  int
}

func wordsOf(text string) []bannerWord {
	var out []bannerWord
	for w := range strings.FieldsSeq(text) {
		out = append(out, bannerWord{text: w, gap: 1})
	}

	return out
}

func (b *bannerText) wrap(text, lead, indent string) { b.wrapWords(wordsOf(text), lead, indent) }

// wrapWords prints words at BannerWidth and never breaks inside a word, so
// a url stays whole. lead opens the first line and indent the rest, so a
// wrapped line keeps its hanging indent. A word wider than a line gets its own.
func (b *bannerText) wrapWords(words []bannerWord, lead, indent string) {
	var line strings.Builder

	line.WriteString(lead)

	cols, open := utf8.RuneCountInString(lead), true

	for _, w := range words {
		width := utf8.RuneCountInString(w.text)

		if !open && cols+w.gap+width > BannerWidth {
			b.line(line.String())
			line.Reset()
			line.WriteString(indent)

			cols, open = utf8.RuneCountInString(indent), true
		}

		if !open {
			line.WriteString(strings.Repeat(" ", w.gap))

			cols += w.gap
		}

		line.WriteString(w.text)

		cols += width
		open = false
	}

	if !open {
		b.line(line.String())
	}
}
