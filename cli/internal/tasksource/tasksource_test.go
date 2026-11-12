package tasksource_test

import (
	"fmt"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/tasksource"
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
		{false, facts.TrackerGitHub, "gh", "tasksource.HostSource"},
		{false, facts.TrackerGitHub, "", "tasksource.RefusedSource"},
		{false, facts.TrackerNotion, "gh", "tasksource.RefusedSource"},
		{true, facts.TrackerGitHub, "gh", "tasksource.FallbackSource tasksource.HostSource"},
		{true, facts.TrackerGitHub, "", "tasksource.FallbackSource tasksource.RefusedSource"},
		{true, facts.TrackerNotion, "gh", "tasksource.FallbackSource tasksource.RefusedSource"},
	} {
		name := fmt.Sprintf("driver %v, %s, cli %q", tc.driver, tc.kind, tc.cli)
		t.Run(name, func(t *testing.T) {
			tg := testkit.DeclaredTracker(t, tc.kind)
			tg.Probes = &probe.Probes{CLI: probe.Signal{Value: tc.cli, Evidence: "none"}}

			if tc.driver {
				testkit.WriteDriver(t, tg.Layout.StorageRoot.Path())
			}

			src := tasksource.TasksFor(tg)
			got := fmt.Sprintf("%T", src)

			if f, ok := src.(tasksource.FallbackSource); ok {
				got += fmt.Sprintf(" %T", f.Rest)
			}

			if got != tc.want {
				t.Errorf("tasks() = %s, want %s", got, tc.want)
			}
		})
	}
}
