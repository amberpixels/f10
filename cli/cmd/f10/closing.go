package main

import (
	"fmt"
	"regexp"
)

// issueRef is an issue by `#n` or by its url; the number's pattern goes in
// place of %[1]s.
const issueRef = `(#%[1]s|https?://\S+/(?:issues|work_items)/%[1]s)\b`

// closingKeyword is GitHub's and GitLab's closing keywords before an issue.
// Pickup reads with the same pattern, so what finish keeps open is what it finds.
const closingKeyword = `(?i)\b(?:close[sd]?|closing|fix(?:e[sd])?|fixing|resolve[sd]?|resolving|` +
	`implement(?:s|ed|ing)?):?\s+` + issueRef

// closingRE reads any closing keyword before any issue number.
var closingRE = regexp.MustCompile(fmt.Sprintf(closingKeyword, `\d+`))

// closingLine is the line a commit or a PR body closes issue number with.
func closingLine(number string) string { return "Closes #" + number }

// keepOpen turns `Closes #12` into `Part of #12`, so the merge leaves it open.
func keepOpen(body, number string) string {
	re := regexp.MustCompile(fmt.Sprintf(closingKeyword, regexp.QuoteMeta(number)))

	return re.ReplaceAllString(body, "Part of $1")
}

// closeAgain undoes keepOpen.
func closeAgain(body, number string) string {
	re := regexp.MustCompile(fmt.Sprintf(`\bPart of `+issueRef, regexp.QuoteMeta(number)))

	return re.ReplaceAllString(body, "Closes $1")
}
