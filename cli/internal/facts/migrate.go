package facts

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/frontmatter"
	"github.com/amberpixels/f10/cli/internal/mdline"
)

// renderDeclared writes a frontmatter block, fences included, or "" when
// nothing is declared.
func renderDeclared(d Declared) (string, error) {
	if isEmpty(d) {
		return "", nil
	}

	var b bytes.Buffer

	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)

	if err := enc.Encode(d); err != nil {
		return "", fmt.Errorf("rendering frontmatter: %w", err)
	}

	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("rendering frontmatter: %w", err)
	}

	return "---\n" + b.String() + "---\n", nil
}

func isEmpty(d Declared) bool {
	return d.Visibility == "" && d.Storage == "" && d.Layering == "" && d.Tracker == (TrackerDecl{}) &&
		d.MergeMethod == "" && d.Catchup == "" && d.ReviewTraces == "" && d.Pipelines == nil && d.Planless == nil &&
		d.DriveChain == nil && d.Review == (ReviewDecl{}) && d.Standup.Upkeep == nil
}

// A Migration is what `f10 init --migrate` makes of one prose-only
// project.md: the file with a frontmatter block on top, and the prose each
// moved fact came from, for the user to delete.
type Migration struct {
	Content    string
	Moved      []string // the frontmatter keys written
	Superseded []string // "<key>: <where in the prose>"
}

// ErrHasFrontmatter refuses a second migration.
var ErrHasFrontmatter = errors.New("already has a frontmatter block")

// Migrate reads the facts one project.md declares in prose, with the same
// readers the fallback uses, and puts them in a frontmatter block above the
// untouched file. Only what the file declares moves: defaults and
// detection stay out, so the block says no more than the prose did.
func Migrate(data []byte, path string) (*Migration, error) {
	if _, _, ok := frontmatter.Split(data); ok {
		return nil, ErrHasFrontmatter
	}

	secs, _ := parseFile(data, path, "main")
	body := func(name string) string { return secs[name].body }
	m := &Migration{}

	var d Declared

	moved := func(key, where string) {
		m.Moved = append(m.Moved, key)
		m.Superseded = append(m.Superseded, key+": "+where)
	}

	if w := proseWord(body("Visibility"), "public", "stealth"); w != "" {
		d.Visibility = w
		moved("visibility", "the Visibility section")
	}

	if w := proseWord(body("Storage"), "out-of-tree", "in-repo"); w != "" {
		d.Storage = w
		moved("storage", "the Storage section")
	}

	if decl := mdline.Bare(firstLayering(body("Layering"))); decl != "" {
		d.Layering = decl
		moved("layering", "the Layering line")
	}

	tracker := body("Tracker")
	if kind := proseTrackerKind(strings.ToLower(tracker)); kind != "" {
		d.Tracker.Kind = kindKey(kind)
		moved("tracker.kind", "the tracker's name in the Tracker section")
	}

	if prefix := idPrefix(tracker); prefix != "" {
		d.Tracker.ID = prefix + "-###"
		moved("tracker.id", "the task id format in the Tracker section")
	}

	hosting := mdline.Unmark(body("Hosting & PR"))
	if method := firstMatch(mergeMethodRe, hosting); method != "" {
		d.MergeMethod = method
		moved("merge_method", "`merge method:` in the Hosting & PR section")
	}

	if catchup := firstMatch(catchupRe, hosting); catchup != "" {
		d.Catchup = catchup
		moved("catchup", "`catchup:` in the Hosting & PR section")
	}

	if pipes, planless := prosePipelines(body("Ship pipeline(s)")); pipes != nil {
		d.Pipelines = map[string]Steps{}
		for _, name := range slices.Sorted(maps.Keys(pipes)) {
			d.Pipelines[name] = pipes[name]
		}

		d.Planless = planless
		moved("pipelines", "the Ship pipeline(s) section")
	}

	if chain := proseChain(body("Drive chain")); len(chain) > 0 {
		d.DriveChain = chain
		moved("drive_chain", "the Drive chain section")
	}

	if r := proseReviewDecl(body("Review")); r != (ReviewDecl{}) {
		d.Review = r
		moved("review", "the Reviewer, Arrives as, Trigger and Done items in the Review section")
	}

	if bad := d.check(); len(bad) > 0 {
		return nil, fmt.Errorf("the prose does not migrate cleanly: %s", strings.Join(bad, "; "))
	}

	block, err := renderDeclared(d)
	if err != nil {
		return nil, err
	}

	m.Content = block + string(data)

	return m, nil
}

// firstLayering is the value of a Layering section or line.
func firstLayering(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.ToLower(line); strings.Contains(line, "replaces") {
			return "replaces"
		} else if strings.Contains(line, "extends") {
			return "extends"
		}
	}

	return ""
}

func kindKey(kind string) string {
	for key, name := range trackerKinds {
		if name == kind {
			return key
		}
	}

	return ""
}

// proseReviewDecl lifts the four remote items' values out of Review prose
// as written, so the frontmatter keeps the same words.
func proseReviewDecl(body string) ReviewDecl {
	var d ReviewDecl

	for line := range strings.SplitSeq(body, "\n") {
		item, isItem := mdline.Item(line)
		if !isItem {
			continue
		}

		label, value, ok := mdline.Cut(item, " - ", ":")
		if !ok {
			continue
		}

		switch key := strings.ToLower(strings.Join(strings.Fields(label), " ")); {
		case key == "reviewer":
			d.Reviewer = value
		case strings.HasPrefix(key, "arrives"):
			d.Arrives = value
		case key == "trigger":
			d.Trigger = value
		case strings.HasPrefix(key, "done"):
			d.Done = value
		}
	}

	return d
}
