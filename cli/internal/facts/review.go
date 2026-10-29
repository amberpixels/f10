package facts

import (
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/mdline"
	"github.com/amberpixels/f10/cli/internal/probe"
)

// The remote half of the Review section, bold items with a fixed vocabulary
// so init can render them and `f10 review` act on them. A line fitting no
// vocabulary leaves its fact empty, never guessed; the rest stays prose.

// A Review is the parsed remote-review contract. The zero value means the
// section declares no remote reviewer, which is the common case.
type Review struct {
	Reviewer     string `json:"reviewer,omitempty"`     // the identity: a login, or a check's name
	ReviewerKind string `json:"reviewerKind,omitempty"` // bot | human | check
	Arrives      string `json:"arrives,omitempty"`      // issue comment | inline review | check run
	Trigger      string `json:"trigger,omitempty"`      // automatic | manual | automatic, manual
	TriggerHow   string `json:"triggerHow,omitempty"`   // the rest of the line: how a human fires it
	ManualHow    string `json:"manualHow,omitempty"`    // the manual part alone, when the line names both
	Done         string `json:"done,omitempty"`         // posted | marker | submitted | concluded
	DoneText     string `json:"doneText,omitempty"`     // the marker's text
	Handled      string `json:"handled,omitempty"`      // reaction | marker | checkbox | thread resolved
	HandledText  string `json:"handledText,omitempty"`  // the reaction's emoji, or the marker's text
	Settled      string `json:"settled,omitempty"`      // reaction | marker
	SettledText  string `json:"settledText,omitempty"`  // the reaction's emoji, or the marker's text

	// settledAs is the settled value as declared, so a value outside the
	// vocabulary is refused rather than read as none declared
	settledAs string
}

// Remote reports whether the section names a remote reviewer at all.
func (r Review) Remote() bool { return r.Reviewer != "" }

// Missing names the facts a remote review still lacks, in contract order,
// so a refusal can say which line to fix. Settled is optional: it is
// missing only when declared and unreadable.
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

	if r.settledAs != "" && r.Settled == "" {
		missing = append(missing, "settled")
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
	codeSpanRE = regexp.MustCompile("`([^`]+)`")
	manualRE   = regexp.MustCompile(`(?i)` + TriggerManual)
	quotedRE   = regexp.MustCompile(`"([^"]+)"`)
)

// parseReview reads the four items out of a Review section's body. Only
// list items count, so a prose line opening with "Done ..." is never read
// as the Done fact; the key may be bold or plain.
func parseReview(body string) Review {
	var r Review

	for line := range strings.SplitSeq(body, "\n") {
		item, isItem := mdline.Item(line)
		if !isItem {
			continue
		}

		label, value, ok := mdline.Cut(item, " - ", ":")
		if !ok {
			continue
		}

		key := strings.ToLower(strings.Join(strings.Fields(label), " "))

		switch {
		case key == "reviewer":
			r.ReviewerKind, r.Reviewer = parseReviewer(value)
		case strings.HasPrefix(key, "arrives"):
			r.Arrives = parseArrives(value)
		case key == "trigger":
			r.Trigger, r.TriggerHow = parseTrigger(value)
			r.ManualHow = manualPart(value)
		case strings.HasPrefix(key, "done"):
			done, handled := splitDoneHandled(value)
			r.Done, r.DoneText = parseDone(done)
			r.Handled, r.HandledText = parseHandled(handled)
		case key == "settled":
			r.settledAs = value
			r.Settled, r.SettledText = parseSettled(value)
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

// The manual triggers `f10 review trigger` can perform: a comment on the
// request, or a review requested from a login.
const (
	ManualComment = "comment"
	ManualRequest = "request"
)

// ManualAction is the manual how as a kind and its text: a comment posting
// the first code span, or a review requested from the span's login. Both need
// the span, since a word guessed from prose could reach a real account.
func (r Review) ManualAction() (string, string, bool) {
	if !strings.Contains(r.Trigger, TriggerManual) {
		return "", "", false
	}

	how := r.Manual()
	lower := strings.ToLower(how)
	span := ""

	if m := codeSpanRE.FindStringSubmatch(how); m != nil {
		span = strings.TrimSpace(m[1])
	}

	switch {
	case strings.Contains(lower, "comment") && span != "":
		return ManualComment, span, true
	case strings.Contains(lower, "request") && span != "":
		return ManualRequest, strings.TrimPrefix(span, "@"), true
	}

	return "", "", false
}

// Manual is how the manual trigger fires: the manual part of a line naming
// both, else the whole how, so a code span describing the automatic half
// is never posted.
func (r Review) Manual() string {
	if r.ManualHow != "" {
		return r.ManualHow
	}

	return r.TriggerHow
}

// manualPart is what follows the word manual, up to the next `;`, when the
// line names both kinds: `automatic, on push; manual, comment `@claude“`.
func manualPart(value string) string {
	lower := strings.ToLower(value)
	if !strings.Contains(lower, TriggerAutomatic) {
		return ""
	}

	// matched on value itself: lowercasing can change a rune's byte length
	loc := manualRE.FindStringIndex(value)
	if loc == nil {
		return ""
	}

	rest, _, _ := strings.Cut(value[loc[1]:], ";")

	return strings.Trim(rest, " ,:-")
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

// parseSettled reads a reaction or a marker. Not the checkbox, which
// handled owns, nor resolved threads, which a human resolving them by hand
// would turn into a settled mark nobody left.
func parseSettled(value string) (string, string) {
	settled, text := parseHandled(value)
	if settled != HandledReaction && settled != HandledMarker {
		return "", ""
	}

	return settled, text
}

// markedText is the code span or quoted string on a line.
func markedText(value string) string {
	for _, re := range []*regexp.Regexp{codeSpanRE, quotedRE} {
		if m := re.FindStringSubmatch(value); m != nil {
			return strings.TrimSpace(m[1])
		}
	}

	return ""
}

// reviewBody renders the five items from what the workflow probe found, in
// the vocabulary parseReview reads: the file init writes parses back to the
// detection it replaced. "" when no review workflow was found.
func reviewBody(pb *probe.Probes) string {
	d := detectedReview(pb)
	if d == (ReviewDecl{}) {
		return ""
	}

	return "- **Reviewer** - " + d.Reviewer + "\n" +
		"- **Arrives as** - " + d.Arrives + "\n" +
		"- **Trigger** - " + d.Trigger + "\n" +
		"- **Done, handled** - " + d.Done + "\n" +
		"- **Settled** - " + d.Settled
}

// detectedReview is the remote review a CI workflow implies, as the
// strings the frontmatter's review key takes. Settled is a rocket: GitHub
// and GitLab spell it the same, unlike +1 or hooray.
func detectedReview(pb *probe.Probes) ReviewDecl {
	if pb == nil || pb.Review == nil {
		return ReviewDecl{}
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

	return ReviewDecl{
		Reviewer: "bot `" + rv.Reviewer + "`",
		Arrives:  "issue comment",
		Trigger:  trigger,
		Done:     "posted, reaction `eyes`",
		Settled:  "reaction `rocket`",
	}
}
