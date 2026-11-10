package main

import (
	"fmt"
	"os"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// agentArgs is the agent's command line: the profile's flags through its
// kind, and each task-file dir of the storage root when that root sits
// outside the worktree, so writing plans and findings there asks no
// permission.
func agentArgs(p agentctl.Profile, root storage.Root, worktree string) []string {
	args := p.Args()
	if !root.Outside(worktree) {
		return args
	}

	for _, d := range root.TaskFileDirs() {
		args = append(args, "--add-dir", d)
	}

	return args
}

// makeStorageDirs creates the task-file dirs agentArgs names, since claude
// may refuse --add-dir on a missing one.
func makeStorageDirs(root storage.Root) error {
	for _, d := range root.TaskFileDirs() {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("creating the storage dirs: %w", err)
		}
	}

	return nil
}
