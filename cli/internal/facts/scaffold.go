package facts

import (
	"strings"

	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/resolve"
)

// The write half of the contract. Scaffold renders what detection found as
// declared prose, so a value that was `detected` this run reads `declared`
// the next one - the promotion nothing else in f10 performs.
//
// Every value here is written in the vocabulary the parsers above key on -
// the `GH-###` placeholder idPrefix prefers, the words trackerKind and
// absorb match - because a file that parses back weaker than the detection
// it replaced would be a demotion wearing a promotion's clothes.

// A Draft is one rendered project.md: the markdown, the contract sections
// it carries, and the ones left out because nothing detected them.
type Draft struct {
	Markdown string
	Sections []string
	Omitted  []string
}

// Scaffold renders a project.md for a checkout that declares none. Pure:
// the caller owns the filesystem, this owns the contract.
func Scaffold(res *resolve.Resolution, pb *probe.Probes, eff *Effective) *Draft {
	bodies := map[string]string{
		"Visibility": eff.Visibility,
		"Storage":    storageBody(eff, res),
	}

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

	d := &Draft{}

	// the title heading carries no body on purpose: prose under it would
	// open a section the contract does not know, and parseFile keeps those
	// as unrecognized rather than dropping them
	b := &strings.Builder{}
	b.WriteString("# " + res.Project + " · f10 project instructions\n")

	for _, name := range contractFields() {
		body, ok := bodies[name]
		if !ok {
			// Layering is a linked worktree's line, and init refuses to be
			// one, so it is not a section this checkout is missing
			if name != "Layering" {
				d.Omitted = append(d.Omitted, name)
			}

			continue
		}

		d.Sections = append(d.Sections, name)
		b.WriteString("\n## " + name + "\n\n" + body + "\n")
	}

	d.Markdown = b.String()

	return d
}

// stackBody is the Project section: the stack the markers named, and not a
// word more. What the project *is* is the one fact no probe reaches, so
// init leaves that half to a human rather than guessing it.
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

// trackerBody is the section the pipeline's contracts actually need: the
// kind, the id format in the contract's placeholder shape, and the two
// adapters as the host CLI's own commands.
func trackerBody(eff *Effective, pb *probe.Probes) string {
	if eff.TrackerKind == "" || eff.IDPrefix == "" {
		return "" // no CLI settled: which tracker this repo uses is a human's answer
	}

	view := "issue view <n> --comments"
	if pb.CLI.Value == "glab" {
		view = "issue view <n>"
	}

	return eff.TrackerKind + ". Task ids `" + eff.IDPrefix + "-###`.\n\n" +
		"- **Fetch** - `" + pb.CLI.Value + " " + view + "`\n" +
		"- **Create** - `" + pb.CLI.Value + " issue create`"
}

func hostingBody(pb *probe.Probes) string {
	switch pb.CLI.Value {
	case "gh":
		return pb.Host + ". Open PRs with `gh pr create`."
	case "glab":
		return pb.Host + ". Open MRs with `glab mr create`."
	default:
		return ""
	}
}

// verifyBody writes the commands themselves, without the evidence that
// found them: a marker file is provenance, which is `f10 config`'s column,
// not a declared fact.
func verifyBody(pb *probe.Probes) string {
	var commands []string

	for _, s := range pb.Verify {
		for c := range strings.SplitSeq(s.Value, " / ") {
			commands = append(commands, "`"+c+"`")
		}
	}

	return strings.Join(commands, " / ")
}

// storageBody names the mode and the root. The mode's word has to survive
// into the file: absorb reads it back out of this prose.
func storageBody(eff *Effective, res *resolve.Resolution) string {
	return eff.Storage + " (" + res.StorageRoot + ")"
}
