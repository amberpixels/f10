// Package layers holds the module's import rule: every package has a rank,
// and a package imports only packages of a lower rank, so feature groups
// cannot depend on each other in cycles. The table is the architecture; a
// change to it is a design change.
package layers

import (
	"fmt"
	"sort"
	"strings"
)

// A Rule ranks every package at or below Prefix, a path relative to the
// module root. Peers lets packages of that rank import each other, which the
// domain packages need and Go's own cycle check keeps acyclic.
type Rule struct {
	Prefix string
	Rank   int
	Peers  bool
}

// Table ranks the module, inner to outer; the longest matching prefix wins.
var Table = []Rule{
	{Prefix: "internal", Rank: 0, Peers: true},
	{Prefix: "internal/layers", Rank: 0, Peers: true},
	{Prefix: "internal/target", Rank: 1},
	{Prefix: "internal/render", Rank: 2, Peers: true},
	{Prefix: "internal/hostops", Rank: 2, Peers: true},
	{Prefix: "internal/stack", Rank: 2, Peers: true},
	{Prefix: "internal/worktree", Rank: 2, Peers: true},
	{Prefix: "internal/archive", Rank: 2, Peers: true},
	{Prefix: "internal/runplan", Rank: 2, Peers: true},
	{Prefix: "internal/reviews", Rank: 2, Peers: true},
	{Prefix: "internal/scope", Rank: 2, Peers: true},
	{Prefix: "internal/launch", Rank: 2, Peers: true},
	{Prefix: "internal/driverec", Rank: 2, Peers: true},
	{Prefix: "internal/asknotify", Rank: 2, Peers: true},
	{Prefix: "internal/statusview", Rank: 2, Peers: true},
	{Prefix: "internal/command", Rank: 2, Peers: true},

	// leaf commands: nothing but services below them
	{Prefix: "internal/command/trace", Rank: 3},
	{Prefix: "internal/command/lore", Rank: 3},
	{Prefix: "internal/command/ledger", Rank: 3},
	{Prefix: "internal/command/state", Rank: 3},
	{Prefix: "internal/command/hook", Rank: 3},
	{Prefix: "internal/command/asks", Rank: 3},
	{Prefix: "internal/command/stats", Rank: 3},

	// the commands that read a task, its request, the config and the board
	{Prefix: "internal/command/task", Rank: 4},
	{Prefix: "internal/command/pr", Rank: 4},
	{Prefix: "internal/command/review", Rank: 4},
	{Prefix: "internal/command/config", Rank: 4},
	{Prefix: "internal/command/standup", Rank: 4},
	{Prefix: "internal/command/board", Rank: 4},
	{Prefix: "internal/command/status", Rank: 4},

	// finish, start (which sweeps finished tasks), then the drive over both
	{Prefix: "internal/finish", Rank: 5},
	{Prefix: "internal/command/finish", Rank: 5},
	{Prefix: "internal/start", Rank: 6},
	{Prefix: "internal/command/start", Rank: 6},
	{Prefix: "internal/drive", Rank: 7},
	{Prefix: "internal/command/drive", Rank: 7},
	{Prefix: "internal/command/daemon", Rank: 7},

	{Prefix: "cmd/f10", Rank: 9},
	{Prefix: "cmd/f10eval", Rank: 9},
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

// Check returns every violation in imports, a map from each module package,
// relative to the module root, to the module packages it imports.
func Check(imports map[string][]string, table []Rule) []Violation {
	var out []Violation

	for from, tos := range imports {
		fr, ok := RankOf(from, table)
		if !ok {
			out = append(out, Violation{From: from, Why: "no rank in the layers table"})

			continue
		}

		for _, to := range tos {
			tr, ok := RankOf(to, table)
			if !ok || to == from {
				continue
			}

			switch {
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

// RankOf is the rule that ranks pkg, a path relative to the module root.
func RankOf(pkg string, table []Rule) (Rule, bool) {
	var (
		best  Rule
		found bool
	)

	for _, r := range table {
		if (pkg == r.Prefix || strings.HasPrefix(pkg, r.Prefix+"/")) && (!found || len(r.Prefix) > len(best.Prefix)) {
			best, found = r, true
		}
	}

	return best, found
}
