package testkit

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
)

func ChildTarget(t *testing.T, cli string) *target.Target {
	t.Helper()

	root := t.TempDir()

	return &target.Target{
		Dir:       root,
		Layout:    &layout.Layout{CheckoutRoot: root, StorageRoot: storage.InRepo(root)},
		Probes:    &probe.Probes{CLI: probe.Signal{Value: cli}},
		Effective: &facts.Effective{IDPrefix: "FT"},
	}
}

func ScriptNoBranch(f *Fakes, ids ...string) {
	for _, id := range ids {
		f.Script(RefsKey("refs/heads/", id), "")
		f.Script(RefsKey("refs/remotes/origin/", id), "")
	}
}
