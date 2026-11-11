package ref

import (
	"fmt"
	"regexp"
	"strings"
)

// namedRiders is how many riders a PR title names before counting the rest.
const namedRiders = 2

// ridersTag is the `(+ABC-2, ABC-3)` a bundle's PR title carries after the
// lead id; BundleTitle writes it and TrimRiders reads it, so they agree.
var ridersTag = regexp.MustCompile(`^\s*\(\+[^)]*\)`)

// BundleTitle names a bundle's riders right after the lead id in title,
// because GitHub cuts long commit subjects from the end. A task alone keeps
// its title; a tag already there is replaced, so a refresh never doubles it.
func BundleTitle(title string, r Ref) string {
	head := r.Head()

	riders := head.Riders()
	if len(riders) == 0 {
		return title
	}

	named := riders[:min(len(riders), namedRiders)]

	tag := " (+" + strings.Join(named, ", ")
	if rest := len(riders) - len(named); rest > 0 {
		tag += fmt.Sprintf(", %d more", rest)
	}

	tag += ")"

	// the digit guard keeps FT-1 from matching inside FT-12
	re := regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(` + regexp.QuoteMeta(head.ID) + `)(?:[^0-9]|$)`)

	loc := re.FindStringSubmatchIndex(title)
	if loc == nil {
		return head.ID + tag + ": " + title
	}

	end := loc[3]

	return title[:end] + tag + TrimRiders(title[end:])
}

// TrimRiders drops the riders tag that opens rest, the text after a lead id.
func TrimRiders(rest string) string {
	return ridersTag.ReplaceAllString(rest, "")
}
