package ledger

import "testing"

// The task finish named: its argument, never --close's value, else the row's.
func TestFinishedTask(t *testing.T) {
	cases := []struct {
		name string
		args []any
		task string
		want string
	}{
		{name: "argument", args: []any{"FT-12", "--yes"}, task: "FT-9", want: "FT-12"},
		{name: "no argument", args: []any{"--yes"}, task: "FT-9", want: "FT-9"},
		{name: "close value", args: []any{"--yes", "--close", "FT-13,FT-14"}, task: "FT-12", want: "FT-12"},
		{name: "close value, then argument", args: []any{"--close", "none", "FT-12"}, task: "FT-9", want: "FT-12"},
		{name: "inline close", args: []any{"--close=FT-13", "FT-12"}, task: "FT-9", want: "FT-12"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := Event{Kind: TaskFinished, Task: c.task, Payload: NewPayload(Payload{"args": c.args})}
			if got := e.FinishedTask(); got != c.want {
				t.Errorf("FinishedTask = %q, want %q", got, c.want)
			}
		})
	}
}
