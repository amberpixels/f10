package start

import (
	"cmp"
	"fmt"
	"os"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// AgentArgs is the agent's command line: the profile's flags through its
// kind, and each task-file dir of the storage root when that root sits
// outside the worktree, so writing plans and findings there asks no
// permission.
func AgentArgs(p agentctl.Profile, root storage.Root, worktree string) []string {
	args := p.Args()
	if !root.Outside(worktree) {
		return args
	}

	for _, d := range root.TaskFileDirs() {
		args = append(args, "--add-dir", d)
	}

	return args
}

// drivenSettings sets launch.DrivenEnv for the session: Claude Code applies
// a settings env to its own process, so every hook it runs inherits it.
var drivenSettings = `{"env":{"` + launch.DrivenEnv + `":"1"}}`

// LaunchArgs is AgentArgs for a launch, marked as a drive's when driven:
// the mark goes first, because --add-dir takes every argument after it.
func LaunchArgs(p agentctl.Profile, root storage.Root, worktree string, driven bool) []string {
	args := AgentArgs(p, root, worktree)
	if !driven || cmp.Or(p.Kind, agentctl.DefaultKind) != agentctl.DefaultKind {
		return args
	}

	return append([]string{"--settings", drivenSettings}, args...)
}

// makeStorageDirs creates the task-file dirs AgentArgs names, since claude
// may refuse --add-dir on a missing one.
func makeStorageDirs(root storage.Root) error {
	for _, d := range root.TaskFileDirs() {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("creating the storage dirs: %w", err)
		}
	}

	return nil
}
