package command_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// clientFiles are the files of the control-plane verbs, which the daemon's
// API serves: each parses its flags, calls the API and prints the reply.
// The verbs left in process (janitor, the asks hook and resume, start's
// drives and drive plan) live in other files.
var clientFiles = []string{
	"status/status.go",
	"start/stop.go",
	"start/forward.go",
	"finish/finish.go",
	"board/board.go",
	"task/taskclient.go",
	"asks/asks.go",
}

// controlPlane are the packages that read or change a project's state, which
// a client file reaches only through the API.
var controlPlane = []string{
	"start", "finish", "taskwrite", "tasksource", "drivestore", "stack", "ledger", "api", "daemon",
}

func TestClientFilesHoldNoControlPlane(t *testing.T) {
	t.Parallel()

	for _, name := range clientFiles {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.FromSlash(name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}

		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)

			for _, pkg := range controlPlane {
				banned := "github.com/amberpixels/f10/cli/internal/" + pkg
				if path == banned || strings.HasPrefix(path, banned+"/") {
					t.Errorf("%s imports %s: a control-plane verb calls the API (internal/apiclient)", name, path)
				}
			}
		}
	}
}
