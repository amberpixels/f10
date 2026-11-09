package ledger

// Readers for the payload keys several packages read. Each key is the json
// tag of the observation that writes it, so a renamed tag fails rows_test.

// Skill is the skill a run_started or task_halted row names.
func (e Event) Skill() string { return e.Payload.Val.String("skill") }

// Reason is why a wait started or ended, or why drive halted a task.
func (e Event) Reason() string { return e.Payload.Val.String("reason") }

// Backstop reports a session_stopped row the backstop wrote, not a Stop hook.
func (e Event) Backstop() bool { return e.Payload.Val.Bool("backstop") }
