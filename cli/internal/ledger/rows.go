package ledger

// Readers for the payload fields several packages read, by the kinds that
// carry them; any other kind reads as empty.

// Skill is the skill a run_started or task_halted row names.
func (e Event) Skill() string {
	switch e.Kind {
	case RunStarted:
		p, _ := Decode[RunStartedPayload](e)

		return p.Skill
	case TaskHalted:
		p, _ := Decode[TaskHaltedPayload](e)

		return p.Skill
	}

	return ""
}

// Reason is why a wait started or ended, or why drive halted a task.
func (e Event) Reason() string {
	switch e.Kind {
	case WaitStarted:
		p, _ := Decode[WaitStartedPayload](e)

		return p.Reason
	case WaitEnded:
		p, _ := Decode[WaitEndedPayload](e)

		return p.Reason
	case TaskHalted:
		p, _ := Decode[TaskHaltedPayload](e)

		return p.Reason
	}

	return ""
}

// Backstop reports a session_stopped row the backstop wrote, not a Stop hook.
func (e Event) Backstop() bool {
	if e.Kind != SessionStopped {
		return false
	}

	p, _ := Decode[SessionStoppedPayload](e)

	return p.Backstop
}
