package observe

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	issueURL = regexp.MustCompile(`https?://\S+?/(?:-/)?issues/\d+`)
	prURL    = regexp.MustCompile(`https?://\S+?/(?:pull|(?:-/)?merge_requests)/\d+`)
	// `[main 1a2b3c4] subject`, `[main (root-commit) 1a2b3c4] subject`,
	// `[detached HEAD 1a2b3c4] subject`
	commitLine = regexp.MustCompile(`(?m)^\[(.+?) ([0-9a-f]{7,40})\] (.*)$`)
	// a ref git moved: fast-forward, forced or new; rejected (`!`) and
	// up-to-date (`=`) lines moved nothing
	pushedRef = regexp.MustCompile(
		`(?m)^ [ +*] (?:[0-9a-f]+\.{2,3}[0-9a-f]+|\[new (?:branch|tag|reference)\])\s+(\S+ -> \S+)`,
	)
	pushRemote = regexp.MustCompile(`(?m)^To (\S+)`)
	envAssign  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// projectLoaded is the first line f10-load-project.sh prints, so a load that
// failed before it printed anything is no step started.
const projectLoaded = "=== f10 project @"

// FromBash reads one Bash tool call: the command line as the model wrote it
// and what it printed. A creation, commit or push counts only with its
// evidence in the output - the url on stdout, the commit line, a moved ref -
// which covers failures and dry runs without an exit code the payload may
// not carry. Urls are read from stdout alone: `gh pr create` names an
// existing PR on stderr when it refuses. Meta is left for the caller to
// Stamp.
func FromBash(command, stdout, stderr string) []Observation {
	out := stdout + "\n" + stderr

	issues := issueURL.FindAllString(stdout, -1)
	prs := prURL.FindAllString(stdout, -1)
	commits := commitLine.FindAllStringSubmatch(stdout, -1)

	var obs []Observation

	for _, words := range Segments(command) {
		words = stripPrefix(words)
		if len(words) == 0 {
			continue
		}

		switch name := filepath.Base(words[0]); name {
		case "gh", "glab":
			obs = append(obs, hostCreate(name, words[1:], &issues, &prs)...)
		case "git":
			obs = append(obs, gitAction(words[1:], &commits, out)...)
		case "f10-load-project.sh":
			if strings.Contains(stdout, projectLoaded) {
				obs = append(obs, &StepStarted{Steps: positional(words[1:])})
			}
		case "f10":
			if args := skipFlags(words[1:], "-C", "--project"); len(args) > 0 && args[0] == "finish" {
				obs = append(obs, &Finished{Args: args[1:]})
			}
		}
	}

	return obs
}

// hostCreate pairs `gh issue create` and the like with the urls printed, in
// order, so two creates in one call take one url each.
func hostCreate(host string, args []string, issues, prs *[]string) []Observation {
	if len(args) < 2 || args[1] != "create" {
		return nil
	}

	switch {
	case args[0] == "issue" && len(*issues) > 0:
		url := (*issues)[0]
		*issues = (*issues)[1:]

		return []Observation{&IssueCreated{Host: host, URL: url}}
	case (host == "gh" && args[0] == "pr" || host == "glab" && args[0] == "mr") && len(*prs) > 0:
		url := (*prs)[0]
		*prs = (*prs)[1:]

		return []Observation{&PRCreated{Host: host, URL: url}}
	}

	return nil
}

func gitAction(args []string, commits *[][]string, out string) []Observation {
	args = skipFlags(args, "-C", "-c", "--git-dir", "--work-tree", "--namespace")
	if len(args) == 0 {
		return nil
	}

	switch args[0] {
	case "commit":
		if len(*commits) == 0 {
			return nil
		}

		m := (*commits)[0]
		*commits = (*commits)[1:]

		return []Observation{&Committed{
			Branch:  strings.TrimSuffix(m[1], " (root-commit)"),
			SHA:     m[2],
			Subject: m[3],
		}}
	case "push":
		var refs []string
		for _, m := range pushedRef.FindAllStringSubmatch(out, -1) {
			refs = append(refs, m[1])
		}

		if len(refs) == 0 {
			return nil
		}

		p := &Pushed{Refs: refs}
		if m := pushRemote.FindStringSubmatch(out); m != nil {
			p.Remote = m[1]
		}

		return []Observation{p}
	}

	return nil
}

// stripPrefix drops what runs a command without being it: env assignments
// and the wrappers `env`, `command`, `exec`, `time`, `nohup`.
func stripPrefix(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		if !envAssign.MatchString(w) && !slices.Contains([]string{"env", "command", "exec", "time", "nohup"}, w) {
			break
		}

		words = words[1:]
	}

	return words
}

// skipFlags drops leading flags, taking a value after each flag named in
// withValue, up to the subcommand.
func skipFlags(args []string, withValue ...string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if slices.Contains(withValue, args[0]) && len(args) > 1 {
			args = args[2:]
		} else {
			args = args[1:]
		}
	}

	return args
}

func positional(args []string) []string {
	out := []string{}

	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}

	return out
}

// Segments splits a command line into its simple commands, each as words
// with quotes removed: split on `&&`, `||`, `;`, `|`, `&`, newlines and
// parentheses outside quotes. Redirections and their targets are dropped,
// here-document bodies and comments skipped, and `$(...)` kept inside its
// word. It reads, never evaluates: no expansion, no globbing.
func Segments(line string) [][]string {
	t := tokenizer{src: []rune(line)}
	t.run()

	return t.segs
}

type tokenizer struct {
	src      []rune
	i        int
	segs     [][]string
	words    []string
	word     strings.Builder
	inWord   bool
	dropNext bool     // the next word is a redirection target
	heredocs []string // delimiters whose bodies start at the next newline
	dashDocs []bool
}

func (t *tokenizer) peek(off int) rune {
	if t.i+off < len(t.src) {
		return t.src[t.i+off]
	}

	return 0
}

func (t *tokenizer) endWord() {
	if !t.inWord {
		return
	}

	if t.dropNext {
		t.dropNext = false
	} else {
		t.words = append(t.words, t.word.String())
	}

	t.word.Reset()
	t.inWord = false
}

func (t *tokenizer) endSeg() {
	t.endWord()

	if len(t.words) > 0 {
		t.segs = append(t.segs, t.words)
	}

	t.words = nil
	t.dropNext = false
}

func (t *tokenizer) add(r rune) {
	t.word.WriteRune(r)
	t.inWord = true
}

func (t *tokenizer) run() {
	for t.i < len(t.src) {
		c := t.src[t.i]

		switch {
		case c == '\\':
			if next := t.peek(1); next == '\n' {
				t.i += 2 // a line continuation joins the lines
			} else {
				if next != 0 {
					t.add(next)
				}

				t.i += 2
			}
		case c == '\'':
			t.quoted('\'')
		case c == '"':
			t.quoted('"')
		case c == '$' && t.peek(1) == '(':
			t.substitution()
		case c == '#' && !t.inWord:
			for t.i < len(t.src) && t.src[t.i] != '\n' {
				t.i++
			}
		case c == '\n':
			t.endSeg()
			t.i++
			t.skipHeredocs()
		case c == ' ' || c == '\t':
			t.endWord()
			t.i++
		case c == ';' || c == '(' || c == ')':
			t.endSeg()
			t.i++
		case c == '&' || c == '|':
			t.endSeg()
			t.i++

			if t.peek(0) == c {
				t.i++
			}
		case c == '<' || c == '>':
			t.redirection(c)
		default:
			t.add(c)
			t.i++
		}
	}

	t.endSeg()
}

// quoted copies a quoted span into the current word. Double quotes keep
// backslash escapes of the characters the shell escapes there.
func (t *tokenizer) quoted(q rune) {
	t.inWord = true
	t.i++

	for t.i < len(t.src) && t.src[t.i] != q {
		c := t.src[t.i]
		if q == '"' && c == '\\' && strings.ContainsRune("\"\\$`", t.peek(1)) {
			c = t.peek(1)
			t.i++
		}

		t.word.WriteRune(c)
		t.i++
	}

	t.i++ // the closing quote
}

// substitution copies `$(...)` whole, nesting included, into the word.
func (t *tokenizer) substitution() {
	depth := 0

	for t.i < len(t.src) {
		c := t.src[t.i]
		t.add(c)
		t.i++

		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return
			}
		}
	}
}

// redirection drops `>`, `2>&1`, `<file` and the like with their target, and
// queues a here-document's delimiter.
func (t *tokenizer) redirection(c rune) {
	// a word of digits right before is the descriptor, not an argument
	if t.inWord && strings.Trim(t.word.String(), "0123456789") == "" {
		t.word.Reset()
		t.inWord = false
	} else {
		t.endWord()
	}

	if c == '<' && t.peek(1) == '<' && t.peek(2) != '<' {
		t.i += 2

		dash := t.peek(0) == '-'
		if dash {
			t.i++
		}

		for t.peek(0) == ' ' || t.peek(0) == '\t' {
			t.i++
		}

		var delim strings.Builder

		for t.i < len(t.src) && !strings.ContainsRune(" \t\n;&|<>()", t.src[t.i]) {
			if r := t.src[t.i]; r != '\'' && r != '"' && r != '\\' {
				delim.WriteRune(r)
			}

			t.i++
		}

		t.heredocs = append(t.heredocs, delim.String())
		t.dashDocs = append(t.dashDocs, dash)

		return
	}

	for t.i < len(t.src) && strings.ContainsRune("<>&|", t.src[t.i]) {
		t.i++
	}

	t.dropNext = true
}

// skipHeredocs skips the bodies queued on the line just ended.
func (t *tokenizer) skipHeredocs() {
	for k, delim := range t.heredocs {
		for t.i < len(t.src) {
			end := t.i
			for end < len(t.src) && t.src[end] != '\n' {
				end++
			}

			line := string(t.src[t.i:end])
			if t.dashDocs[k] {
				line = strings.TrimLeft(line, "\t")
			}

			t.i = min(end+1, len(t.src))

			if line == delim {
				break
			}
		}
	}

	t.heredocs, t.dashDocs = nil, nil
}
