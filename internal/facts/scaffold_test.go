package facts

import (
	"slices"
	"testing"

	"github.com/amberpixels/f10/internal/probe"
	"github.com/amberpixels/f10/internal/resolve"
)

func TestScaffoldSections(t *testing.T) {
	cases := []struct {
		name        string
		res         *resolve.Resolution
		pb          *probe.Probes
		wantSection []string
		wantOmitted []string
		wantIn      []string
		wantNotIn   []string
		storage     string // out-of-tree declares itself by location, which Build reads off the layers
	}{
		{
			name: "github checkout with a stack and a justfile",
			res:  &resolve.Resolution{Project: "demo", StorageRoot: "/repo/.f10"},
			pb: &probe.Probes{
				Host:   "github.com",
				CLI:    probe.Signal{Value: "gh", Evidence: "well-known host github.com"},
				Stack:  []probe.Signal{{Value: "Go", Evidence: "go.mod"}},
				Verify: []probe.Signal{{Value: "just lint / just test", Evidence: "justfile"}},
			},
			wantSection: []string{"Project", "Visibility", "Storage", "Tracker", "Hosting & PR", "Verify"},
			wantOmitted: []string{"Roles", "Ship pipeline(s)", "Review", "Guardrails"},
			wantIn: []string{
				"# demo · f10 project instructions",
				"GitHub Issues. Task ids `GH-###`.",
				"`gh issue view <n> --comments`",
				"`gh issue create`",
				"github.com. Open PRs with `gh pr create`.",
				"`just lint` / `just test`",
				"in-repo (/repo/.f10)",
			},
			// the evidence that found a value is a provenance column, never a declared fact
			wantNotIn: []string{"go.mod", "justfile", "(via gh)", "TODO"},
		},
		{
			name: "gitlab checkout",
			res:  &resolve.Resolution{Project: "demo", StorageRoot: "/repo/.f10"},
			pb: &probe.Probes{
				Host:  "gitlab.com",
				CLI:   probe.Signal{Value: "glab", Evidence: "well-known host gitlab.com"},
				Stack: []probe.Signal{{Value: "Ruby", Evidence: "Gemfile"}},
			},
			wantSection: []string{"Project", "Visibility", "Storage", "Tracker", "Hosting & PR"},
			wantOmitted: []string{"Roles", "Ship pipeline(s)", "Verify", "Review", "Guardrails"},
			wantIn: []string{
				"GitLab work items. Task ids `GL-###`.",
				"`glab issue view <n>`",
				"Open MRs with `glab mr create`.",
				"Ruby.",
			},
		},
		{
			name:        "no remote, no markers: only what has a defined default",
			res:         &resolve.Resolution{Project: "bare", StorageRoot: "/repo/.f10"},
			pb:          &probe.Probes{CLI: probe.Signal{Value: "none", Evidence: "no origin remote"}},
			wantSection: []string{"Visibility", "Storage"},
			wantOmitted: []string{
				"Project", "Roles", "Tracker", "Hosting & PR",
				"Ship pipeline(s)", "Verify", "Review", "Guardrails",
			},
			wantNotIn: []string{"## Project", "## Tracker", "## Hosting & PR", "## Verify"},
		},
		{
			name: "unknown routing leaves the tracker to a human",
			res:  &resolve.Resolution{Project: "demo", StorageRoot: "/repo/.f10"},
			pb: &probe.Probes{
				Host: "git.example.com",
				CLI:  probe.Signal{Value: "unknown", Evidence: "neither gh nor glab is logged in to git.example.com"},
			},
			wantSection: []string{"Visibility", "Storage"},
			wantNotIn:   []string{"## Tracker", "## Hosting & PR", "neither gh nor glab"},
		},
		{
			name:    "out-of-tree storage names itself and its root",
			res:     &resolve.Resolution{Project: "demo", StorageRoot: "/home/e/.f10/demo"},
			pb:      &probe.Probes{CLI: probe.Signal{Value: "none", Evidence: "no origin remote"}},
			storage: "out-of-tree",
			wantIn:  []string{"out-of-tree (/home/e/.f10/demo)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eff := Build(tc.res, tc.pb)
			if tc.storage != "" {
				eff.Storage = tc.storage
			}

			d := Scaffold(tc.res, tc.pb, eff)

			if tc.wantSection != nil && !slices.Equal(d.Sections, tc.wantSection) {
				t.Errorf("sections = %v, want %v", d.Sections, tc.wantSection)
			}

			if tc.wantOmitted != nil && !slices.Equal(d.Omitted, tc.wantOmitted) {
				t.Errorf("omitted = %v, want %v", d.Omitted, tc.wantOmitted)
			}

			for _, want := range tc.wantIn {
				if !contains(d.Markdown, want) {
					t.Errorf("markdown missing %q:\n%s", want, d.Markdown)
				}
			}

			for _, unwanted := range tc.wantNotIn {
				if contains(d.Markdown, unwanted) {
					t.Errorf("markdown carries %q:\n%s", unwanted, d.Markdown)
				}
			}
		})
	}
}

// The promotion this whole command exists for: what detection found this
// run must read back as a declared fact the next one, with the sub-facts
// intact. A renderer that drifts from the parsers fails here first.
func TestScaffoldRoundTrip(t *testing.T) {
	detected := &probe.Probes{
		Host:   "github.com",
		CLI:    probe.Signal{Value: "gh", Evidence: "well-known host github.com"},
		Stack:  []probe.Signal{{Value: "Go", Evidence: "go.mod"}},
		Verify: []probe.Signal{{Value: "just lint / just test", Evidence: "justfile"}},
	}

	before := Build(&resolve.Resolution{Project: "demo", StorageRoot: "/repo/.f10"}, detected)
	d := Scaffold(&resolve.Resolution{Project: "demo", StorageRoot: "/repo/.f10"}, detected, before)

	declaring := &resolve.Resolution{
		Project:     "demo",
		StorageRoot: "/repo/.f10",
		Layers:      []resolve.Layer{{Label: "main", Dir: dirWithProjectMD(t, d.Markdown)}},
	}

	after := Build(declaring, detected)

	for _, name := range d.Sections {
		if f := fieldByName(t, after, name); f.Origin != "declared (main)" {
			t.Errorf("%s reads %q after init wrote it, want declared (main)", name, f.Origin)
		}
	}

	if after.TrackerKind != before.TrackerKind || after.IDPrefix != before.IDPrefix {
		t.Errorf("tracker sub-facts drifted: %q/%q, want %q/%q",
			after.TrackerKind, after.IDPrefix, before.TrackerKind, before.IDPrefix)
	}

	if after.Visibility != before.Visibility || after.Storage != before.Storage {
		t.Errorf("visibility/storage drifted: %q/%q, want %q/%q",
			after.Visibility, after.Storage, before.Visibility, before.Storage)
	}

	// the title heading carries no body, so it opens no section the
	// contract does not know
	if len(after.Unrecognized) > 0 {
		t.Errorf("init wrote unrecognized sections: %+v", after.Unrecognized)
	}
}
