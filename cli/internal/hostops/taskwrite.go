package hostops

import (
	"slices"
)

type Props struct {
	Status, Priority, Assignee string
	Labels, Set                []string
}

func (p Props) Empty() bool {
	return p.Status == "" && p.Priority == "" && p.Assignee == "" && p.Labels == nil && p.Set == nil
}

// Args is the props as the tracker driver's flags.
func (p Props) Args() []string {
	var args []string

	if p.Status != "" {
		args = append(args, "--status", p.Status)
	}

	if p.Priority != "" {
		args = append(args, "--priority", p.Priority)
	}

	for _, l := range p.Labels {
		args = append(args, "--label", l)
	}

	if p.Assignee != "" {
		args = append(args, "--assignee", p.Assignee)
	}

	for _, kv := range p.Set {
		args = append(args, "--set", kv)
	}

	return args
}

// PriorityLabel takes the first label in name order when several carry the
// level; the index is lower-cased, and the hosts match case-insensitively.
func PriorityLabel(level string, priorities map[string]string) string {
	var names []string

	for name, l := range priorities {
		if l == level {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	if len(names) == 0 {
		return ""
	}

	return names[0]
}

type titleChange struct{ ID, Number, From, To string }
