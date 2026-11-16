package remote

import (
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/gen/agents/v1/agentsv1connect"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
)

// A Scope is what a token lets a device do.
type Scope string

// The scopes a token can carry.
const (
	ScopeRead   Scope = "read"   // status, board, tasks, events, asks and agents
	ScopeAnswer Scope = "answer" // answer an ask
	ScopeSteer  Scope = "steer"  // send text to an agent
	ScopeStart  Scope = "start"  // start, drive or cancel one, create a task
	ScopeFinish Scope = "finish" // finish a task, close its workspace
)

// Scopes is every scope, in the order help lists them.
var Scopes = []Scope{ScopeRead, ScopeAnswer, ScopeSteer, ScopeStart, ScopeFinish}

// anyToken is a procedure every valid token may call.
const anyToken Scope = ""

// procedures is the scope each procedure needs on the tailnet. A procedure
// missing here, and every socket-only one, is refused, so a new method is
// unreachable remotely until it is placed.
var procedures = map[string]Scope{
	f10v1connect.DaemonServiceHelloProcedure:            anyToken,
	f10v1connect.DaemonServiceGetDaemonStatusProcedure:  ScopeRead,
	f10v1connect.TaskServiceGetTaskProcedure:            ScopeRead,
	f10v1connect.RunServiceGetRunStatusProcedure:        ScopeRead,
	f10v1connect.BoardServiceGetBoardProcedure:          ScopeRead,
	f10v1connect.EventServiceWatchEventsProcedure:       ScopeRead,
	f10v1connect.ArtifactServiceListArtifactsProcedure:  ScopeRead,
	f10v1connect.ArtifactServiceGetArtifactProcedure:    ScopeRead,
	f10v1connect.ProjectServiceListProjectsProcedure:    ScopeRead,
	f10v1connect.RunServiceListDrivesProcedure:          ScopeRead,
	agentsv1connect.AskServiceListAsksProcedure:         ScopeRead,
	agentsv1connect.AgentServiceListWorkspacesProcedure: ScopeRead,
	agentsv1connect.AgentServiceListAgentsProcedure:     ScopeRead,
	agentsv1connect.AgentServiceGetAgentStatusProcedure: ScopeRead,
	agentsv1connect.AskServiceAnswerAskProcedure:        ScopeAnswer,
	agentsv1connect.AgentServiceSendTextProcedure:       ScopeSteer,
	f10v1connect.RunServiceForwardProcedure:             ScopeSteer,
	f10v1connect.RunServiceStartProcedure:               ScopeStart,
	f10v1connect.RunServiceDriveProcedure:               ScopeStart,
	f10v1connect.RunServiceCancelDriveProcedure:         ScopeStart,
	f10v1connect.TaskServiceCreateTaskProcedure:         ScopeStart,
	f10v1connect.RunServiceFinishProcedure:              ScopeFinish,
	agentsv1connect.AgentServiceCloseWorkspaceProcedure: ScopeFinish,
}

// socketOnly are the procedures no token reaches: stopping the daemon ends
// every drive on the machine, and a watch registers local paths.
var socketOnly = map[string]bool{
	f10v1connect.DaemonServiceStopDaemonProcedure:   true,
	f10v1connect.DaemonServiceWatchProjectProcedure: true,
}

// Procedures is every procedure tok may call on the tailnet, sorted, so a
// client hides what it cannot call without a scope table of its own.
func Procedures(tok Token) []string {
	var out []string

	for proc, scope := range procedures {
		if scope == anyToken || tok.Has(scope) {
			out = append(out, proc)
		}
	}

	slices.Sort(out)

	return out
}

// Allows reports whether scopes reach a procedure as an operation: placed
// in the table under one of them. Socket-only procedures and those any token
// may call (Hello) are plumbing, so never allowed here.
func Allows(scopes []Scope) func(procedure string) bool {
	return func(procedure string) bool {
		scope, ok := procedures[procedure]

		return ok && scope != anyToken && slices.Contains(scopes, scope)
	}
}

// ParseScopes reads `--scope` values, each one name or a comma list.
func ParseScopes(values []string) ([]Scope, error) {
	var out []Scope

	for _, v := range values {
		for name := range strings.SplitSeq(v, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}

			s, ok := scopeNamed(name)
			if !ok {
				return nil, fmt.Errorf("unknown scope %q: use %s", name, ScopeList())
			}

			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("a token needs at least one scope: %s", ScopeList())
	}

	return out, nil
}

// ScopeList is the scope names joined for a message.
func ScopeList() string {
	names := make([]string, len(Scopes))
	for i, s := range Scopes {
		names[i] = string(s)
	}

	return strings.Join(names, ", ")
}

func scopeNamed(name string) (Scope, bool) {
	for _, s := range Scopes {
		if string(s) == name {
			return s, true
		}
	}

	return "", false
}
