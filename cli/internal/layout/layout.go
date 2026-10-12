// Package layout locates the f10 configuration layers for a working
// directory: the checkout root, the instruction layers in precedence order,
// their layering mode, and the storage root. bin/f10-load.sh is the semantics
// oracle - the parity test in this package runs both against the same
// fixtures, so a change to either side fails loudly until both move.
package layout

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/frontmatter"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// Layering modes: a worktree layer either extends main's instructions or
// replaces them wholesale.
const (
	LayeringExtends  = "extends"
	LayeringReplaces = "replaces"
)

// A Layer is one instructions directory contributing configuration. Layers
// are ordered by precedence: a later layer's field beats an earlier one's.
type Layer struct {
	Label string `json:"label"` // "main", "checkout", "worktree" or "out-of-tree"
	Dir   string `json:"dir"`
}

// A Layout names where configuration lives for one working directory.
// Path fields are physical (symlinks resolved), matching f10-load.sh.
type Layout struct {
	Cwd              string  // where the run started
	CheckoutRoot     string  // root of the checkout the run started in
	MainRoot         string  // root of the main checkout; empty when git cannot name one
	Project          string  // basename of the main checkout, or of the cwd outside git
	StorageRoot      string  // where plans (and in-repo instructions) live
	Layering         string  // LayeringExtends or LayeringReplaces
	LayeringDeclared bool    // a worktree project.md declares Layering
	LayeringFrom     string  // "frontmatter" or "prose", when declared
	Layers           []Layer // precedence order: later wins
	Source           string  // human description; wording matches f10-load.sh
	NestedIgnored    string  // a .f10/instructions below the root, noted and ignored
}

// Locate finds the configuration layout for cwd. It never fails -
// absence is a valid result, same charter as the script.
func Locate(ctx context.Context, cwd string) *Layout {
	lay := &Layout{Cwd: cwd, Layering: LayeringExtends}

	here := gitx.Out(ctx, cwd, "rev-parse", "--show-toplevel")
	if here == "" {
		here = canon(cwd)
	} else {
		here = canon(here)
	}

	lay.CheckoutRoot = here
	lay.MainRoot = mainWorktree(ctx, cwd)

	// the base layer is main's, or simply this checkout's where git cannot
	// name a main worktree
	baseLabel, baseRoot := "checkout", here
	if lay.MainRoot != "" {
		baseLabel, baseRoot = "main", lay.MainRoot
	}

	lay.Project = filepath.Base(baseRoot)
	lay.StorageRoot = filepath.Join(here, ".f10")

	baseInstr := existingDir(filepath.Join(baseRoot, ".f10", "instructions"))

	var wtInstr string
	if here != baseRoot {
		wtInstr = existingDir(filepath.Join(here, ".f10", "instructions"))
	}

	// `Layering - replaces main` in the worktree's own project.md takes
	// main's place wholesale; anything else - another value, no line, no
	// project.md - extends it.
	if wtInstr != "" && baseInstr != "" {
		if decl, from := layeringDecl(filepath.Join(wtInstr, "project.md")); decl != "" {
			lay.LayeringDeclared, lay.LayeringFrom = true, from
			if strings.Contains(decl, "replaces") || strings.Contains(decl, "Replaces") {
				lay.Layering = LayeringReplaces
			}
		}
	}

	switch {
	case baseInstr != "" && wtInstr != "" && lay.Layering == LayeringExtends:
		lay.Layers = []Layer{
			{Label: baseLabel, Dir: baseInstr},
			{Label: "worktree", Dir: wtInstr},
		}
		lay.Source = "layered - " + baseLabel + " (" + baseRoot + ") + worktree (" + here + ")"
	case wtInstr != "":
		lay.Layers = []Layer{{Label: "worktree", Dir: wtInstr}}
		lay.Source = "worktree (" + here + ")"

		if baseInstr != "" {
			lay.Source += " - replaces main"
		}
	case baseInstr != "":
		lay.Layers = []Layer{{Label: baseLabel, Dir: baseInstr}}
		lay.Source = baseLabel + " (" + baseRoot + ")"

		if here != baseRoot {
			lay.Source += " via worktree fallback"
		}
	default:
		// out-of-tree is per-project by construction, so it is a single
		// layer and only a last resort - and finding it moves the whole
		// storage root, plans included
		if home, err := os.UserHomeDir(); err == nil {
			oot := existingDir(filepath.Join(home, ".f10", lay.Project, "instructions"))
			if oot != "" {
				lay.Layers = []Layer{{Label: "out-of-tree", Dir: oot}}
				lay.Source = "out-of-tree (~/.f10/" + lay.Project + ")"
				lay.StorageRoot = filepath.Join(home, ".f10", lay.Project)
			}
		}
	}

	if lay.Source == "" {
		lay.Source = "none"
	}

	// a config below the root is not a per-directory config - note it
	// rather than pass it over silently
	if canon(cwd) != here {
		lay.NestedIgnored = existingDir(filepath.Join(cwd, ".f10", "instructions"))
	}

	return lay
}

// mainWorktree returns the main checkout's root: the first worktree git
// lists. Empty outside git.
func mainWorktree(ctx context.Context, cwd string) string {
	porcelain := gitx.Out(ctx, cwd, "worktree", "list", "--porcelain")
	for line := range strings.SplitSeq(porcelain, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			return canon(path)
		}
	}

	return ""
}

// canon is the script's `cd dir && pwd -P`: the physical form of a path.
func canon(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}

	return path
}

// existingDir returns path when it is a directory, "" otherwise.
func existingDir(path string) string {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return path
	}

	return ""
}

// layeringDecl returns the file's Layering declaration and where it was
// found: the frontmatter's `layering:` key, else the first prose line. The
// patterns match the script's greps; the prose one tolerates list markers
// and emphasis or code marks, `-` or `:` after the word.
func layeringDecl(projectMD string) (string, string) {
	data, err := os.ReadFile(projectMD)
	if err != nil {
		return "", ""
	}

	block, body, ok := frontmatter.Split(data)
	if ok {
		var fm struct {
			Layering string `yaml:"layering"`
		}

		if yaml.Unmarshal(block, &fm) == nil && fm.Layering != "" {
			return fm.Layering, "frontmatter"
		}
	}

	line := regexp.MustCompile("(?i)^\\s*[-*#]*\\s*[*_`]*layering[*_`]*\\s*[-:]")
	for l := range strings.SplitSeq(string(body), "\n") {
		if line.MatchString(l) {
			return l, "prose"
		}
	}

	return "", ""
}
