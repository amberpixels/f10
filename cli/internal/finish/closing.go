package finish

import (
	"fmt"
	"regexp"

	"github.com/amberpixels/f10/cli/internal/ref"
)

// issueRef is an issue by `#n` or by its url; the number's pattern goes in
// place of %[1]s.
const issueRef = `(#%[1]s|https?://\S+/(?:issues|work_items)/%[1]s)\b`

// closingKeyword is GitHub's and GitLab's closing keywords before an issue.
// Pickup reads with the same pattern, so what finish keeps open is what it finds.
const closingKeyword = `(?i)\b(?:close[sd]?|closing|fix(?:e[sd])?|fixing|resolve[sd]?|resolving|` +
	`implement(?:s|ed|ing)?):?\s+` + issueRef

// ClosingRE reads any closing keyword before any issue number.
var ClosingRE = regexp.MustCompile(fmt.Sprintf(closingKeyword, `\d+`))

// ClosingLine is the line a commit or a PR body closes issue number with.
func ClosingLine(number string) string { return "Closes #" + number }

// BodyCloses is the riders body closes on merge, in bundle order: a rider
// under `Part of`, or not named at all, stays open.
func BodyCloses(body string, riders []ref.Ref) []string {
	closing := []string{}

	for _, r := range riders {
		re := regexp.MustCompile(fmt.Sprintf(closingKeyword, regexp.QuoteMeta(r.Number)))
		if re.MatchString(body) {
			closing = append(closing, r.ID)
		}
	}

	return closing
}

// KeepOpen turns `Closes #12` into `Part of #12`, so the merge leaves it open.
func KeepOpen(body, number string) string {
	re := regexp.MustCompile(fmt.Sprintf(closingKeyword, regexp.QuoteMeta(number)))

	return re.ReplaceAllString(body, "Part of $1")
}

// CloseAgain undoes KeepOpen.
func CloseAgain(body, number string) string {
	re := regexp.MustCompile(fmt.Sprintf(`\bPart of `+issueRef, regexp.QuoteMeta(number)))

	return re.ReplaceAllString(body, "Closes $1")
}
