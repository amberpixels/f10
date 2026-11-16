package mcp

import (
	"testing"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// A model's made-up caller context never reaches the socket, which would
// honor it; every other argument does.
func TestToolCallsDropTheCallerContext(t *testing.T) {
	t.Parallel()

	start := &f10v1.StartRequest{Task: "GH-1", Caller: &f10v1.CallerContext{Cwd: "/elsewhere", Env: []string{"A=1"}}}
	dropCaller(start.ProtoReflect())

	answer := &agentsv1.AnswerAskRequest{Id: "k", Caller: &agentsv1.CallerContext{Env: []string{"A=1"}}}
	dropCaller(answer.ProtoReflect())

	if start.GetCaller() != nil || start.GetTask() != "GH-1" || answer.GetCaller() != nil || answer.GetId() != "k" {
		t.Errorf("start = %v, answer = %v, want each without its caller and with the rest", start, answer)
	}
}
