package facts

import (
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/probe"
)

// The remote half of the Review section: four facts with a fixed vocabulary,
// written as bold items the way Tracker writes its adapters, so f10 init can
// render them and `f10 review` can act on them. Everything else in the
// section (the local reviewer's categories, a never-flag list, who answers
// an ask) stays prose and is never read here.
//
//	- **Reviewer** - bot `claude[bot]`
//	- **Arrives as** - issue comment
//	- **Trigger** - automatic, on open and on push
//	- **Done, handled** - posted, reaction `eyes`
//
// A line that fits no vocabulary leaves its fact empty: the binary refuses
// naming the fact rather than guessing, and config shows the prose as is.

// A Review is the parsed remote-review contract. The zero value means the
// section declares no remote reviewer, which is the common case.
type Review struct {
	Reviewer     string `json:"reviewer,omitempty"`     // the identity: a login, or a check's name
	ReviewerKind string `json:"reviewerKind,omitempty"` // bot | human | check
	Arrives      string `json:"arrives,omitempty"`      // issue comment | inline review | check run
	Trigger      string `json:"trigger,omitempty"`      // automatic | manual | automatic, manual
	TriggerHow   string `json:"triggerHow,omitempty"`   // the rest of the line: how a human fires it
	Done         string `json:"done,omitempty"`         // posted | marker | submitted | concluded
	DoneText     string `json:"doneText,omitempty"`     // the marker's text
	Handled      string `json:"handled,omitempty"`      // reaction | marker | checkbox | thread resolved
	HandledText  string `json:"handledText,omitempty"`  // the reaction's emoji, or the marker's text
}

// Remote reports whether the section names a remote reviewer at all.
func (r Review) Remote() bool { return r.Reviewer != "" }

// Missing names the facts a remote review still lacks, in contract order,
// so a refusal can say which line to fix.
func (r Review) Missing() []string {
	var missing []string

	for _, f := range []struct{ name, value string }{
		{"Reviewer", r.ReviewerKind},
		{"Arrives as", r.Arrives},
		{"Trigger", r.Trigger},
		{"Done", r.Done},
		{"handled", r.Handled},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}

	return missing
}

// The vocabularies, as the words the parser looks for.
const (
	ReviewerBot   = "bot"
	ReviewerHuman = "human"
	ReviewerCheck = "check"

	ArrivesComment = "issue comment"
	ArrivesInline  = "inline review"
	ArrivesCheck   = "check run"

	TriggerAutomatic = "automatic"
	TriggerManual    = "manual"

	DonePosted    = "posted"
	DoneMarker    = "marker"
	DoneSubmitted = "submitted"
	DoneConcluded = "concluded"

	HandledReaction = "reaction"
	HandledMarker   = "marker"
	HandledCheckbox = "checkbox"
	HandledThread   = "thread resolved"
)

var (
	reviewItemRE = regexp.MustCompile(`^[-*]\s+\*\*([^*]+)\*\*\s*[-:]?\s*(.*)$`)
	codeSpanRE   = regexp.MustCompile("`([^`]+)`")
	quotedRE     = regexp.MustCompile(`"([^"]+)"`)
)

// parseReview reads the four items out of a Review section's body.
func parseReview(body string) Review {
	var r Review

	for line := range strings.SplitSeq(body, "\n") {
		m := reviewItemRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}

		key := strings.ToLower(strings.Join(strings.Fields(m[1]), " "))
		value := strings.TrimSpace(m[2])

		switch {
		case key == "reviewer":
			r.ReviewerKind, r.Reviewer = parseReviewer(value)
		case strings.HasPrefix(key, "arrives"):
			r.Arrives = parseArrives(value)
		case key == "trigger":
			r.Trigger, r.TriggerHow = parseTrigger(value)
		case strings.HasPrefix(key, "done"):
			done, handled := splitDoneHandled(value)
			r.Done, r.DoneText = parseDone(done)
			r.Handled, r.HandledText = parseHandled(handled)
		}
	}

	return r
}

// parseReviewer reads `bot `claude[bot]“: the kind is the first word, the
// identity the first code span, else the second word.
func parseReviewer(value string) (string, string) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", ""
	}

	kind := strings.ToLower(fields[0])
	if kind != ReviewerBot && kind != ReviewerHuman && kind != ReviewerCheck {
		return "", ""
	}

	if m := codeSpanRE.FindStringSubmatch(value); m != nil {
		return kind, strings.TrimSpace(m[1])
	}

	if len(fields) > 1 {
		return kind, strings.Trim(fields[1], "`*")
	}

	return "", ""
}

func parseArrives(value string) string {
	lower := strings.ToLower(value)

	switch {
	case strings.Contains(lower, "inline"):
		return ArrivesInline
	case strings.Contains(lower, "check"):
		return ArrivesCheck
	case strings.Contains(lower, "comment"):
		return ArrivesComment
	}

	return ""
}

// parseTrigger reads `automatic, on open and on push` or `manual, comment
// `@claude“: the kind words, then whatever follows as the human's how.
func parseTrigger(value string) (string, string) {
	lower := strings.ToLower(value)
	automatic := strings.Contains(lower, TriggerAutomatic)
	manual := strings.Contains(lower, TriggerManual)

	var kind, how string

	switch {
	case automatic && manual:
		kind = TriggerAutomatic + ", " + TriggerManual
	case automatic:
		kind = TriggerAutomatic
	case manual:
		kind = TriggerManual
	default:
		return "", ""
	}

	if _, rest, ok := strings.Cut(value, ","); ok {
		how = strings.TrimSpace(rest)
	}

	return kind, how
}

// splitDoneHandled cuts the fourth line in two at its first comma outside a
// code span: `posted, reaction `eyes“, `marker "Review complete", checkbox`.
func splitDoneHandled(value string) (string, string) {
	inCode := false

	for i, r := range value {
		switch {
		case r == '`':
			inCode = !inCode
		case r == ',' && !inCode:
			return strings.TrimSpace(value[:i]), strings.TrimSpace(value[i+1:])
		}
	}

	return strings.TrimSpace(value), ""
}

func parseDone(value string) (string, string) {
	lower := strings.ToLower(value)

	switch {
	case strings.Contains(lower, DoneMarker):
		return DoneMarker, markedText(value)
	case strings.Contains(lower, DonePosted):
		return DonePosted, ""
	case strings.Contains(lower, DoneSubmitted):
		return DoneSubmitted, ""
	case strings.Contains(lower, DoneConcluded), strings.Contains(lower, "completed"):
		return DoneConcluded, ""
	}

	return "", ""
}

func parseHandled(value string) (string, string) {
	lower := strings.ToLower(value)

	switch {
	case strings.Contains(lower, HandledReaction):
		emoji := markedText(value)
		if emoji == "" {
			emoji = "eyes"
		}

		return HandledReaction, strings.Trim(emoji, ":")
	case strings.Contains(lower, HandledMarker):
		return HandledMarker, markedText(value)
	case strings.Contains(lower, HandledCheckbox):
		return HandledCheckbox, ""
	case strings.Contains(lower, "resolved"):
		return HandledThread, ""
	}

	return "", ""
}

// markedText is the code span or quoted string on a line: the emoji's
// name, the marker's text.
func markedText(value string) string {
	for _, re := range []*regexp.Regexp{codeSpanRE, quotedRE} {
		if m := re.FindStringSubmatch(value); m != nil {
			return strings.TrimSpace(m[1])
		}
	}

	return ""
}

// reviewBody renders the four items from what the workflow probe found, in
// the vocabulary parseReview reads: the file init writes parses back to the
// detection it replaced. "" when no review workflow was found.
func reviewBody(pb *probe.Probes) string {
	if pb == nil || pb.Review == nil {
		return ""
	}

	rv := pb.Review

	mention := rv.Mention
	if mention == "" {
		mention = "@claude"
	}

	var trigger string

	switch rv.Trigger {
	case "automatic, manual":
		trigger = "automatic, on open and on push; manual, comment `" + mention + "` on the PR for a re-review"
	case "automatic":
		trigger = "automatic, on open and on push"
	default:
		trigger = "manual, comment `" + mention + "` on the PR"
	}

	return "- **Reviewer** - bot `" + rv.Reviewer + "`\n" +
		"- **Arrives as** - issue comment\n" +
		"- **Trigger** - " + trigger + "\n" +
		"- **Done, handled** - posted, reaction `eyes`"
}
