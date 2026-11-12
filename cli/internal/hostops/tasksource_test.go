package hostops_test

import (
	"fmt"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// tasks picks the source from the driver, the tracker kind and the host CLI.
func TestTasksPicksTheSource(t *testing.T) {
	for _, tc := range []struct {
		driver bool
		kind   facts.TrackerKind
		cli    string
		want   string
	}{
		{false, facts.TrackerGitHub, "gh", "hostops.HostSource"},
		{false, facts.TrackerGitHub, "", "hostops.RefusedSource"},
		{false, facts.TrackerNotion, "gh", "hostops.RefusedSource"},
		{true, facts.TrackerGitHub, "gh", "hostops.FallbackSource hostops.HostSource"},
		{true, facts.TrackerGitHub, "", "hostops.FallbackSource hostops.RefusedSource"},
		{true, facts.TrackerNotion, "gh", "hostops.FallbackSource hostops.RefusedSource"},
	} {
		name := fmt.Sprintf("driver %v, %s, cli %q", tc.driver, tc.kind, tc.cli)
		t.Run(name, func(t *testing.T) {
			tg := testkit.DeclaredTracker(t, tc.kind)
			tg.Probes = &probe.Probes{CLI: probe.Signal{Value: tc.cli, Evidence: "none"}}

			if tc.driver {
				testkit.WriteDriver(t, tg.Layout.StorageRoot.Path())
			}

			src := hostops.TasksFor(tg)
			got := fmt.Sprintf("%T", src)

			if f, ok := src.(hostops.FallbackSource); ok {
				got += fmt.Sprintf(" %T", f.Rest)
			}

			if got != tc.want {
				t.Errorf("tasks() = %s, want %s", got, tc.want)
			}
		})
	}
}
