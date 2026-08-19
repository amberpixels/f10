// Package resolve locates the f10 configuration layers for a working
// directory: the checkout root, the instruction layers in precedence order,
// their layering mode, and the storage root. bin/resolve.sh is the semantics
// oracle - the parity test in this package runs both against the same
// fixtures, so a change to either side fails loudly until both move.
package resolve

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/amberpixels/f10/internal/gitx"
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

// A Resolution names where configuration lives for one working directory.
// Path fields are physical (symlinks resolved), matching the resolver script.
type Resolution struct {
	Cwd              string  // where the run started
	CheckoutRoot     string  // root of the checkout the run started in
	MainRoot         string  // root of the main checkout; empty when git cannot name one
	Project          string  // basename of the main checkout, or of the cwd outside git
	StorageRoot      string  // where plans (and in-repo instructions) live
	Layering         string  // LayeringExtends or LayeringReplaces
	LayeringDeclared bool    // a worktree project.md carries the Layering line
	Layers           []Layer // precedence order: later wins
	Source           string  // human description; wording matches resolve.sh
	NestedIgnored    string  // a .f10/instructions below the root, noted and ignored
}

// Resolve resolves the configuration layout for cwd. It never fails -
// absence is a valid result, same charter as the script.
func Resolve(ctx context.Context, cwd string) *Resolution {
	res := &Resolution{Cwd: cwd, Layering: LayeringExtends}

	here := gitx.Out(ctx, cwd, "rev-parse", "--show-toplevel")
	if here == "" {
		here = canon(cwd)
	} else {
		here = canon(here)
	}

	res.CheckoutRoot = here
	res.MainRoot = mainWorktree(ctx, cwd)

	// the base layer is main's, or simply this checkout's where git cannot
	// name a main worktree
	baseLabel, baseRoot := "checkout", here
	if res.MainRoot != "" {
		baseLabel, baseRoot = "main", res.MainRoot
	}

	res.Project = filepath.Base(baseRoot)
	res.StorageRoot = filepath.Join(here, ".f10")

	baseInstr := existingDir(filepath.Join(baseRoot, ".f10", "instructions"))

	var wtInstr string
	if here != baseRoot {
		wtInstr = existingDir(filepath.Join(here, ".f10", "instructions"))
	}

	// `Layering - replaces main` in the worktree's own project.md takes
	// main's place wholesale; anything else - another value, no line, no
	// project.md - extends it.
	if wtInstr != "" && baseInstr != "" {
		if decl := layeringDecl(filepath.Join(wtInstr, "project.md")); decl != "" {
			res.LayeringDeclared = true
			if strings.Contains(decl, "replaces") || strings.Contains(decl, "Replaces") {
				res.Layering = LayeringReplaces
			}
		}
	}

	switch {
	case baseInstr != "" && wtInstr != "" && res.Layering == LayeringExtends:
		res.Layers = []Layer{
			{Label: baseLabel, Dir: baseInstr},
			{Label: "worktree", Dir: wtInstr},
		}
		res.Source = "layered - " + baseLabel + " (" + baseRoot + ") + worktree (" + here + ")"
	case wtInstr != "":
		res.Layers = []Layer{{Label: "worktree", Dir: wtInstr}}
		res.Source = "worktree (" + here + ")"

		if baseInstr != "" {
			res.Source += " - replaces main"
		}
	case baseInstr != "":
		res.Layers = []Layer{{Label: baseLabel, Dir: baseInstr}}
		res.Source = baseLabel + " (" + baseRoot + ")"

		if here != baseRoot {
			res.Source += " via worktree fallback"
		}
	default:
		// out-of-tree is per-project by construction, so it is a single
		// layer and only a last resort - and finding it moves the whole
		// storage root, plans included
		if home, err := os.UserHomeDir(); err == nil {
			oot := existingDir(filepath.Join(home, ".f10", res.Project, "instructions"))
			if oot != "" {
				res.Layers = []Layer{{Label: "out-of-tree", Dir: oot}}
				res.Source = "out-of-tree (~/.f10/" + res.Project + ")"
				res.StorageRoot = filepath.Join(home, ".f10", res.Project)
			}
		}
	}

	if res.Source == "" {
		res.Source = "none"
	}

	// a config below the root is not a per-directory config - note it
	// rather than pass it over silently
	if canon(cwd) != here {
		res.NestedIgnored = existingDir(filepath.Join(cwd, ".f10", "instructions"))
	}

	return res
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

// layeringDecl returns the first Layering declaration line in the file, or
// "". The pattern matches the script's grep: list markers and bold
// asterisks tolerated, `-` or `:` after the word.
func layeringDecl(projectMD string) string {
	data, err := os.ReadFile(projectMD)
	if err != nil {
		return ""
	}

	line := regexp.MustCompile(`(?i)^\s*[-*#]*\s*\**layering\**\s*[-:]`)
	for l := range strings.SplitSeq(string(data), "\n") {
		if line.MatchString(l) {
			return l
		}
	}

	return ""
}
