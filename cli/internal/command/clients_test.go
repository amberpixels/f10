package command_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
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

// startAllowed is every f10 package drive/start.go imports. The file runs
// start's in-process checks (a closed task, a PR to pick up) before it calls
// the API, so it cannot pass the client-files guard; a new import here is a
// choice made on purpose, not drift.
var startAllowed = []string{
	"gen/f10/v1", "internal/agents", "internal/apiclient", "internal/command/config", "internal/drive",
	"internal/drivectl", "internal/launch", "internal/start", "internal/target",
}

func TestStartFileImportsOnlyItsAllowlist(t *testing.T) {
	t.Parallel()

	f, err := parser.ParseFile(token.NewFileSet(), filepath.FromSlash("drive/start.go"), nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}

	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)

		rel, ok := strings.CutPrefix(path, "github.com/amberpixels/f10/cli/")
		if ok && !slices.Contains(startAllowed, rel) {
			t.Errorf(
				"drive/start.go imports %s, outside its allowlist: route it through the API, or add it on purpose",
				path,
			)
		}
	}
}
