package asks

import (
	"strconv"
	"strings"
)

// Nerd Font Material Design icons, the only range Claude Code keeps:
// md-chat_question heads a banner, md-inbox marks the inbox's counts and rows.
const (
	AskIcon   = "\U000F1738"
	InboxIcon = "\U000F0687"
)

const bannerRule = "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

// BannerFooter is the banner's last line: where an answer goes.
const BannerFooter = "Answer here, or /f10:inbox from the main session"

// Banner renders a as the one shape every way in prints it. It renders the
// questions Parse read, so what the user sees is what an answer resolves
// against. name labels the task, such as project:task in a list spanning
// projects, or a terminal's link. Empty is the task id.
func Banner(a Ask, name string) string {
	task := a.Task
	if name != "" {
		task = name
	}

	header := " " + AskIcon + " ASK · " + task
	if a.Skill != "" {
		header += " · at " + a.Skill
	}

	lines := []string{bannerRule, header, bannerRule}

	qs := a.Questions
	if len(qs) == 0 {
		qs = Parse(a.Text)
	}

	for _, q := range qs {
		num := strconv.Itoa(q.N) + ". "
		lines = append(lines, " "+num+q.Text)

		if len(q.Options) > 0 {
			lines = append(lines, " "+strings.Repeat(" ", len(num))+"> "+strings.Join(q.Options, " | "))
		}
	}

	lines = append(lines, bannerRule, " "+BannerFooter)

	return strings.Join(lines, "\n") + "\n"
}
