package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// The public vocabulary of start has no "drive": a batch is the record, the
// daemon is the process, and "driven" is the mode. Package and type names
// keep the old word, so these tests are what stops it reaching a user.

// oldWordRe matches the old word alone. "driven", "undriven" and "driver"
// are other words, and `drive_env` or `F10_DRIVE_FILE` are one word each.
var oldWordRe = regexp.MustCompile(`(?i)\b(drive|drives|drove|driving)\b`)

// storedOldWords are the string literals that keep the old word because a
// file, an ask, the ledger or an older client already holds it. Keyed
// `file: literal`.
var storedOldWords = map[string]string{
	"internal/asks/ask.go: drive":                       "an answer's source, as asks and the ledger hold it",
	"internal/asks/events.go: drive":                    "an ask's label, kept beside batch for asks already raised",
	"internal/branchmeta/branchmeta.go: f10-drive":      "a branch's git config key",
	"internal/daemon/lock.go: drives":                   "the records' directory under the storage root",
	"internal/daemon/serve.go: .drive":                  "the file a batch's process writes its number to",
	"internal/drivestore/lease.go: drives":              "the records' directory under the storage root",
	"internal/drivestore/lease.go:  drive=":             "a lease file's line",
	"internal/drivestore/lease.go: drive":               "a lease file's key",
	"internal/drivestore/lease.go: drive-":              "a batch's lease name",
	"internal/layers/layers.go: internal/drive":         "a package path",
	"internal/layers/layers.go: internal/command/drive": "a package path",
	"internal/state/launches.go: drive":                 "a launch's kind, as session state holds it",
}

// oldSpellings are the literals that name an old spelling on purpose: the
// aliases kept for one release.
var oldSpellings = map[string]string{
	"internal/command/drive/drive.go: drive":                 "the hidden command f10 drive plan runs under",
	"internal/command/drive/drive.go: f10 drive plan":        "its notice",
	"internal/command/status/status.go: drives":              "the hidden --drives flag",
	"internal/command/status/status.go: f10 status --drives": "its notice",
}

func goLiterals(t *testing.T, dirs ...string) []codeText {
	t.Helper()

	root := filepath.Join("..", "..")

	var out []codeText

	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return err
			}

			fset := token.NewFileSet()

			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}

			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)

			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.ImportSpec:
					return false
				case *ast.Field:
					// a struct tag is a stored key, never a message
					if x.Tag != nil {
						ast.Inspect(x.Type, literalVisitor(fset, rel, &out))

						return false
					}
				case *ast.BasicLit:
					addLiteral(fset, rel, x, &out)
				}

				return true
			})

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	return out
}

func literalVisitor(fset *token.FileSet, rel string, out *[]codeText) func(ast.Node) bool {
	return func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok {
			addLiteral(fset, rel, lit, out)
		}

		return true
	}
}

func addLiteral(fset *token.FileSet, rel string, lit *ast.BasicLit, out *[]codeText) {
	if lit.Kind != token.STRING {
		return
	}

	text, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}

	*out = append(*out, codeText{rel, fset.Position(lit.Pos()).Line, text})
}

func TestMessagesKeepTheStartVocabulary(t *testing.T) {
	seen := map[string]bool{}

	for _, lit := range goLiterals(t, "internal", "cmd", filepath.Join("gen", "mcptools")) {
		if !oldWordRe.MatchString(lit.text) {
			continue
		}

		key := lit.file + ": " + lit.text
		seen[key] = true

		if storedOldWords[key] != "" || oldSpellings[key] != "" {
			continue
		}

		t.Errorf("%s:%d says %q: a batch is the record, the daemon is the process, driven is the mode",
			lit.file, lit.line, lit.text)
	}

	for _, list := range []map[string]string{storedOldWords, oldSpellings} {
		for key := range list {
			if !seen[key] {
				t.Errorf("the allowlist holds %q, which the code no longer has: drop the entry", key)
			}
		}
	}
}

// proseOldWords are the lines of the plugin's prose that keep the old word
// on purpose, keyed `file: a piece of the line`. An entry no line matches
// fails, so the list only shrinks.
var proseOldWords = map[string]string{
	"README.md: the same commands drive a Go service":           "the verb, about a stack",
	"steps/demo.md: seed and drive the app":                     "the verb, about an app under demo",
	"steps/demo.md: launches and drives the app":                "the verb, about an app under demo",
	"steps/demo.md: **What drives the app**":                    "the verb, about an app under demo",
	"docs/project-setup.md: The old key `drive_env`":            "an old spelling, named beside its replacement",
	"docs/cli.md: under their old names, `F10_DRIVE_FILE`":      "the two variables a daemon still sets for an older child",
	"ts/web/src/views/Inbox.tsx: carries it as drive":           "a stored label's name",
	"proto/f10/v1/events.proto: (`drive` for a batch":           "a stored value",
	"proto/f10/v1/runs.proto: // drive is the batch's number":   "a deprecated message's field",
	"proto/f10/v1/daemon.proto: // drive is the batch's number": "a deprecated message's field",
}

// oldSpellingsParagraph opens the one paragraph of the docs that lists the
// old spellings.
const oldSpellingsParagraph = "**Old spellings.**"

// oldSpellingRe matches an old spelling inside code, where the old word
// alone is allowed because a stored name may hold it.
var oldSpellingRe = regexp.MustCompile(`f10 drive\b|--drives\b|start --resume|drive_env|F10_DRIVE_`)

// tsWordsRe is what a person reads in a line of the web client: a comment, a
// string, or the text between two JSX tags. An identifier is none of them,
// so a property named by a stored label is not read as prose.
var tsWordsRe = regexp.MustCompile("//.*$|^\\s*/?\\*.*$|\"[^\"]*\"|'[^']*'|`[^`]*`|>[^<>{}]+<")

func tsProse(line string) string { return strings.Join(tsWordsRe.FindAllString(line, -1), " ") }

// TestProseKeepsTheStartVocabulary reads prose and code apart. Prose never
// says the old word. Code, in a span or a fence, may hold a stored name such
// as `drives/`, and never an old spelling outside its one paragraph.
func TestProseKeepsTheStartVocabulary(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	used := map[string]bool{}

	check := func(rel string, n int, line string, code bool) {
		prose := line

		switch ext := filepath.Ext(rel); {
		case code:
			prose = ""
		case ext == ".md":
			prose = codeSpanRe.ReplaceAllString(line, "")
		case ext == ".ts" || ext == ".tsx":
			prose = tsProse(line)
		}

		// a proto field or method named with the old word is a wire name, and
		// the comment that calls it the old name has to name it
		if strings.HasPrefix(rel, "proto/") &&
			(!strings.HasPrefix(strings.TrimSpace(line), "//") || strings.Contains(line, "is the old name of")) {
			return
		}

		if !oldWordRe.MatchString(prose) && !oldSpellingRe.MatchString(line) {
			return
		}

		for key := range proseOldWords {
			file, piece, _ := strings.Cut(key, ": ")
			if file == rel && strings.Contains(line, piece) {
				used[key] = true

				return
			}
		}

		t.Errorf("%s:%d says %q: a batch is the record, the daemon is the process, driven is the mode",
			rel, n, strings.TrimSpace(line))
	}

	files := []string{"README.md", filepath.Join("proto", "README.md")}

	// the plugin's prose, then what else a user or a model reads: the hook
	// scripts, the plugin manifest and the web client's words
	read := map[string][]string{
		"steps": {".md"}, "skills": {".md"}, "conventions": {".md"}, "modes": {".md"}, "docs": {".md"},
		"proto": {".md", ".proto"}, "bin": {".sh", ""}, ".claude-plugin": {".json"},
		filepath.Join("ts", "web", "src"): {".ts", ".tsx"},
	}

	for dir, exts := range read {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}

			if slices.Contains(exts, filepath.Ext(path)) {
				rel, _ := filepath.Rel(root, path)
				files = append(files, rel)
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}

		fenced, listing := false, false

		for i, line := range strings.Split(string(raw), "\n") {
			switch {
			case strings.HasPrefix(strings.TrimSpace(line), "```"):
				fenced = !fenced

				continue
			case strings.Contains(line, oldSpellingsParagraph):
				listing = true
			case strings.TrimSpace(line) == "":
				listing = false
			}

			if !listing {
				check(filepath.ToSlash(rel), i+1, line, fenced)
			}
		}
	}

	for key := range proseOldWords {
		if !used[key] {
			t.Errorf("the allowlist holds %q, which no line has: drop the entry", key)
		}
	}
}

// Every command's help is read by a user, a hidden command's too.
func TestHelpKeepsTheStartVocabulary(t *testing.T) {
	walkCommands(newApp(), "f10", func(path string, c *cli.Command) {
		texts := []string{c.Usage, c.ArgsUsage, c.Description}

		for _, f := range c.Flags {
			if df, ok := f.(cli.DocGenerationFlag); ok {
				texts = append(texts, df.GetUsage())
			}

			// --drives is the hidden old spelling of --batches
			for _, name := range f.Names() {
				if name != "drives" {
					texts = append(texts, name)
				}
			}
		}

		// f10 drive is the hidden old spelling of f10 start --propose
		if path == "f10 drive" {
			return
		}

		for _, text := range texts {
			if oldWordRe.MatchString(text) {
				t.Errorf("%s: help says %q", path, text)
			}
		}
	})
}
