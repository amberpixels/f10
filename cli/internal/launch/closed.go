package launch

import (
	"fmt"
	"strings"
)

// Closed is what a discussion's agent is told about a task the tracker
// already closed. It holds the commits that shipped the task, and the open
// tasks a typo of its id would have meant.
type Closed struct {
	ID      string   // the task, as its label
	Shipped []string // "<sha> <subject>", newest first
	More    int      // shipping commits left out of Shipped
	Release string   // the oldest tag holding the newest of them, or ""
	Typos   []string // open tasks one digit off the id, as "<id> (<title>)"
}

// Note is the report's line for the closed task.
func (c Closed) Note() string {
	n := c.ID + " is closed"

	switch total := len(c.Shipped) + c.More; {
	case total == 1:
		n += ", shipped by 1 commit"
	case total > 1:
		n += fmt.Sprintf(", shipped by %d commits", total)
	}

	if c.Release != "" {
		n += " (" + c.Release + ")"
	}

	if len(c.Typos) > 0 {
		n += "; did you mean " + joinOr(c.Typos) + "?"
	}

	return n
}

// clause tells the agent the task is closed, what shipped it, and the
// likely reasons it was started anyway, for the user to pick from.
func (c Closed) clause() string {
	var p strings.Builder

	fmt.Fprintf(&p, " %s is closed.", c.ID)

	if len(c.Shipped) == 0 {
		p.WriteString(" No commit on the default branch names it or its subtasks, " +
			"so look in its comments for what closed it.")
	} else {
		fmt.Fprintf(&p, " It was shipped by: %s", strings.Join(c.Shipped, "; "))

		if c.More > 0 {
			fmt.Fprintf(&p, "; and %d more", c.More)
		}

		p.WriteString(".")

		if c.Release != "" {
			fmt.Fprintf(&p, " Released in %s.", c.Release)
		}
	}

	p.WriteString(" Say so, and ask which the user meant:")

	if len(c.Typos) > 0 {
		fmt.Fprintf(&p, " a typo for %s, which `f10 start <id>` runs instead;", joinOr(c.Typos))
	}

	p.WriteString(" a small fix on top of the shipped work, as a follow-up PR from this branch;" +
		" or a follow-up task to capture with `/f10:capture`, because a closed task is never reopened.")

	return p.String()
}

// closedClauses is every closed task's clause, in order.
func closedClauses(closed []Closed) string {
	var p strings.Builder
	for _, c := range closed {
		p.WriteString(c.clause())
	}

	return p.String()
}

// joinOr joins items as a question does: "a", "a or b", "a, b or c".
func joinOr(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}

	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}
