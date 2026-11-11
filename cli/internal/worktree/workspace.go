package worktree

import (
	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// WorkspaceAt is the workspace opened on path, nil for none.
func WorkspaceAt(wss []agentctl.Workspace, path string) *agentctl.Workspace {
	for i := range wss {
		if wss[i].Path != "" && gitx.SamePath(wss[i].Path, path) {
			return &wss[i]
		}
	}

	return nil
}

// WorkspaceByID is the workspace with id, nil for none.
func WorkspaceByID(wss []agentctl.Workspace, id string) *agentctl.Workspace {
	for i := range wss {
		if wss[i].ID == id {
			return &wss[i]
		}
	}

	return nil
}

// WorkspaceLabel is ws's label, "" for no workspace.
func WorkspaceLabel(ws *agentctl.Workspace) string {
	if ws == nil {
		return ""
	}

	return ws.Label
}
