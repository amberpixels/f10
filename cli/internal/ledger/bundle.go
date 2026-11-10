package ledger

import (
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ref"
)

// Bundle is the bundle the event's branch ships when the event's task
// leads it, else nil.
func (e Event) Bundle() []string {
	i := strings.LastIndexByte(e.Task, '-')
	if i < 0 {
		return nil
	}

	members := ref.Members(e.Task[:i], e.Branch)
	if len(members) < 2 || !strings.EqualFold(members[0], e.Task) {
		return nil
	}

	return members
}

// OnTask reports whether the event is the task's: its own, or its bundle's.
func (e Event) OnTask(id string) bool {
	if strings.EqualFold(e.Task, id) {
		return true
	}

	return slices.ContainsFunc(e.Bundle(), func(m string) bool { return strings.EqualFold(m, id) })
}
