// Package layers holds the module's import rule: every package has a rank,
// and a package imports only packages of a lower rank, so feature groups
// cannot depend on each other in cycles. The table is the architecture; a
// change to it is a design change. Its tests also hold the package comment
// rule: the comment lives in <pkg>.go or doc.go, a binary's in main.go.
package layers

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// A Rule ranks every package at or below Prefix, a path relative to the
// module root. Peers lets packages of that rank import each other, which the
// domain packages need and Go's own cycle check keeps acyclic. Exact ranks
// Prefix alone, and every package below it then needs a rule of its own, so
// a new one is never ranked by a broader rule by accident.
type Rule struct {
	Prefix string
	Rank   int
	Peers  bool
	Exact  bool
}

// Table ranks the module, inner to outer; the longest matching prefix wins.
var Table = []Rule{
	// leaves: text, files, git, the shell and the terminal, which read no
	// project state, so nothing of the domain may reach into them
	{Prefix: "internal/textx", Rank: -2},
	{Prefix: "internal/fsx", Rank: -2},
	{Prefix: "internal/taskid", Rank: -2},
	{Prefix: "internal/shell", Rank: -2},
	{Prefix: "internal/gitx", Rank: -2},
	{Prefix: "internal/flock", Rank: -2},
	{Prefix: "internal/seam", Rank: -3},          // the test swap, which every package's tests may import
	{Prefix: "internal/gitx/gitxtest", Rank: -3}, // test repos, gitx's own tests included
	{Prefix: "internal/testenv", Rank: -3},       // the hermetic test environment, for every package's tests
	{Prefix: "internal/buildinfo", Rank: -2},
	{Prefix: "internal/calendar", Rank: -2},
	{Prefix: "internal/mdline", Rank: -2},
	{Prefix: "internal/tasktitle", Rank: -2},
	{Prefix: "internal/flow", Rank: -2},
	{Prefix: "internal/trace", Rank: -2},
	{Prefix: "internal/agentctl", Rank: -2},
	{Prefix: "internal/images", Rank: -2},
	{Prefix: "internal/projects", Rank: -2},
	{Prefix: "internal/ui", Rank: -1},
	{Prefix: "internal/tasktree", Rank: -1},
	{Prefix: "internal/trackerdriver", Rank: -1}, // imports shell
	{Prefix: "internal/asks", Rank: -1},          // imports fsx

	// the domain packages, which Go's own cycle check keeps acyclic
	{Prefix: "internal", Rank: 0, Peers: true},
	{Prefix: "internal/layers", Rank: 0, Peers: true},
	{Prefix: "internal/target", Rank: 1},
	{Prefix: "internal/render", Rank: 2, Peers: true},
	{Prefix: "internal/hostops", Rank: 2, Peers: true},
	{Prefix: "internal/tasksource", Rank: 2, Peers: true},
	{Prefix: "internal/stack", Rank: 2, Peers: true},
	{Prefix: "internal/worktree", Rank: 2, Peers: true},
	{Prefix: "internal/archive", Rank: 2, Peers: true},
	{Prefix: "internal/runplan", Rank: 2, Peers: true},
	{Prefix: "internal/reviews", Rank: 2, Peers: true},
	{Prefix: "internal/scope", Rank: 2, Peers: true},
	{Prefix: "internal/launch", Rank: 2, Peers: true},
	{Prefix: "internal/drivectl", Rank: 2, Peers: true},
	{Prefix: "internal/asknotify", Rank: 2, Peers: true},
	{Prefix: "internal/statusview", Rank: 2, Peers: true},
	{Prefix: "internal/testkit", Rank: 2, Peers: true}, // the fakes, which swap seams in the services above
	{Prefix: "internal/command", Rank: 2, Peers: true, Exact: true},

	// leaf commands: nothing but services below them
	{Prefix: "internal/command/trace", Rank: 3},
	{Prefix: "internal/command/lore", Rank: 3},
	{Prefix: "internal/command/ledger", Rank: 3},
	{Prefix: "internal/command/state", Rank: 3},
	{Prefix: "internal/command/hook", Rank: 3},
	{Prefix: "internal/command/asks", Rank: 3},
	{Prefix: "internal/command/stats", Rank: 3},
	{Prefix: "internal/command/atlas", Rank: 3},

	// the commands that read a task, its request, the config and the board
	{Prefix: "internal/command/task", Rank: 4},
	{Prefix: "internal/command/pr", Rank: 4},
	{Prefix: "internal/command/review", Rank: 4},
	{Prefix: "internal/command/config", Rank: 4},
	{Prefix: "internal/command/standup", Rank: 4},
	{Prefix: "internal/command/board", Rank: 4},
	{Prefix: "internal/command/status", Rank: 4},

	// finish, start (which sweeps finished tasks), then the drive over both;
	// each command sits just above the core it parses flags for
	{Prefix: "internal/finish", Rank: 5},
	{Prefix: "internal/finish/finishtest", Rank: 6}, // the janitor fixture, for finish's and the drive's tests
	{Prefix: "internal/command/finish", Rank: 6},
	{Prefix: "internal/start", Rank: 7},
	{Prefix: "internal/command/start", Rank: 8},
	{Prefix: "internal/drive", Rank: 9},
	{Prefix: "internal/command/drive", Rank: 10},
	{Prefix: "internal/command/daemon", Rank: 10},

	{Prefix: "cmd/f10", Rank: 11},
	{Prefix: "cmd/f10eval", Rank: 11},
}

// A Violation is one import the table forbids, or a package it does not rank.
type Violation struct {
	From, To string
	Why      string
}

func (v Violation) String() string {
	if v.To == "" {
		return v.From + ": " + v.Why
	}

	return v.From + " imports " + v.To + ": " + v.Why
}

// commandRoot holds the commands, which parse flags and print: only another
// command or a binary may import one, so a service never depends on a flag.
const commandRoot = "internal/command"

// Check returns every violation in imports, a map from each module package,
// relative to the module root, to the module packages it imports.
func Check(imports map[string][]string, table []Rule) []Violation {
	var out []Violation

	for from, tos := range imports {
		fr, ok := RankOf(from, table)
		if !ok || underExact(from, fr, table) {
			out = append(out, Violation{From: from, Why: "no rank in the layers table"})

			continue
		}

		for _, to := range tos {
			tr, ok := RankOf(to, table)
			if !ok || to == from || ownTestHelper(from, to) {
				continue
			}

			switch {
			case strings.HasPrefix(to, commandRoot+"/") && strings.HasPrefix(from, "internal/") &&
				from != commandRoot && !strings.HasPrefix(from, commandRoot+"/"):
				out = append(out, Violation{
					From: from, To: to, Why: "a service imports a command; move the helper down to a service",
				})
			case tr.Rank > fr.Rank:
				out = append(out, Violation{
					From: from, To: to,
					Why: fmt.Sprintf("rank %d imports rank %d; move the shared piece down instead", fr.Rank, tr.Rank),
				})
			case tr.Rank == fr.Rank && (!fr.Peers || !tr.Peers):
				out = append(out, Violation{
					From: from, To: to,
					Why: fmt.Sprintf("peers of rank %d import each other; move the shared piece down instead", fr.Rank),
				})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })

	return out
}

// ownTestHelper says to is from's own test helper package, <pkg>/<pkg>test:
// it imports from, so it ranks above it, and only from's tests import it.
func ownTestHelper(from, to string) bool {
	return to == from+"/"+path.Base(from)+"test"
}

// RankOf is the rule that ranks pkg, a path relative to the module root.
func RankOf(pkg string, table []Rule) (Rule, bool) {
	var (
		best  Rule
		found bool
	)

	for _, r := range table {
		under := !r.Exact && strings.HasPrefix(pkg, r.Prefix+"/")
		if (pkg == r.Prefix || under) && (!found || len(r.Prefix) > len(best.Prefix)) {
			best, found = r, true
		}
	}

	return best, found
}

// underExact says pkg lies below an Exact rule while a broader rule ranked it.
func underExact(pkg string, ranked Rule, table []Rule) bool {
	for _, r := range table {
		if r.Exact && strings.HasPrefix(pkg, r.Prefix+"/") && len(ranked.Prefix) < len(r.Prefix) {
			return true
		}
	}

	return false
}
