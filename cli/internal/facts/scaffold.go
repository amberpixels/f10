package facts

import (
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
)

// The write half of the contract: Scaffold renders detection as a declared
// project.md, so a value `detected` this run reads `declared` the next, in
// the vocabulary the readers key on so it never parses back weaker.

// A Draft is one rendered project.md, with what it carries and the sections
// nothing detected.
type Draft struct {
	Markdown string
	Keys     []string
	Sections []string
	Omitted  []string
}

// Scaffold renders a project.md for a checkout that declares none. Pure:
// the caller owns the filesystem, this owns the contract.
func Scaffold(lay *layout.Layout, pb *probe.Probes, eff *Effective) (*Draft, error) {
	d := &Draft{}

	decl := Declared{Visibility: eff.Visibility, Storage: eff.Storage}
	d.Keys = append(d.Keys, "visibility", "storage")

	// no CLI settled: which tracker this repo uses is a human's answer
	if eff.TrackerKind != "" && eff.IDPrefix != "" {
		decl.Tracker = TrackerDecl{Kind: kindKey(eff.TrackerKind), ID: eff.IDPrefix + "-###"}
		d.Keys = append(d.Keys, "tracker")
	}

	if review := detectedReview(pb); review != (ReviewDecl{}) {
		decl.Review = review
		d.Keys = append(d.Keys, "review")
	}

	block, err := renderDeclared(decl)
	if err != nil {
		return nil, err
	}

	bodies := map[string]string{}

	for name, body := range map[string]string{
		"Project":      stackBody(pb),
		"Tracker":      trackerBody(eff, pb),
		"Hosting & PR": hostingBody(pb),
		"Verify":       verifyBody(pb),
	} {
		if body != "" {
			bodies[name] = body
		}
	}

	// the title heading carries no body: prose under it would open a section
	// the contract does not know, kept as unrecognized
	b := &strings.Builder{}
	b.WriteString(block)
	b.WriteString("# " + lay.Project + " · f10 project instructions\n")

	for _, name := range contractFields() {
		body, ok := bodies[name]
		if !ok {
			// Layering is a linked worktree's line, and init refuses to be
			// one; Visibility, Storage and Review went into the frontmatter
			if !slices.Contains([]string{"Layering", "Visibility", "Storage"}, name) &&
				(name != "Review" || decl.Review == (ReviewDecl{})) {
				d.Omitted = append(d.Omitted, name)
			}

			continue
		}

		d.Sections = append(d.Sections, name)
		b.WriteString("\n## " + name + "\n\n" + body + "\n")
	}

	d.Markdown = b.String()

	return d, nil
}

// stackBody is the stack the markers named and not a word more: what the
// project *is* is the one fact no probe reaches, so init leaves it to a human.
func stackBody(pb *probe.Probes) string {
	if len(pb.Stack) == 0 {
		return ""
	}

	values := make([]string, 0, len(pb.Stack))
	for _, s := range pb.Stack {
		values = append(values, s.Value)
	}

	return strings.Join(values, ", ") + "."
}

// trackerBody is the Tracker section's prose half. The kind and the id format
// are frontmatter; the host's issues need no adapter, since f10 reads and
// creates them, and a declared CLI command would steer agents past it.
func trackerBody(eff *Effective, pb *probe.Probes) string {
	if eff.TrackerKind == "" || eff.IDPrefix == "" {
		return ""
	}

	return "Issues on " + pb.Host + ", read and created by `f10 task read` and `f10 task create`; no adapter needed."
}

func hostingBody(pb *probe.Probes) string {
	switch pb.CLI.Value {
	case "gh":
		return pb.Host + ". Open PRs with `f10 pr create`."
	case "glab":
		return pb.Host + ". Open MRs with `f10 pr create`."
	default:
		return ""
	}
}

// verifyBody writes the commands without the evidence that found them: a
// marker file is provenance, `f10 config`'s column, not a declared fact.
func verifyBody(pb *probe.Probes) string {
	var commands []string

	for _, s := range pb.Verify {
		if s.Judgment {
			continue
		}

		for c := range strings.SplitSeq(s.Value, " / ") {
			commands = append(commands, "`"+c+"`")
		}
	}

	return strings.Join(commands, " / ")
}
