package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivectl"
)

// Every row of the flag table parses, is or is not taken by --resume, and
// is or is not part of the record's match, as the row says.
func TestDriveFlagTable(t *testing.T) {
	values := map[string]string{
		"--flow": "local", "--pace": "fast", "--agent": "opus", "--model": "opus",
		"--answer": "1. yes", "--every": "5s", "--parallel": "3", "--resume": "2",
	}

	for _, f := range drivectl.DriveFlags {
		if f.Name == "--resume" {
			continue
		}

		t.Run(f.Name, func(t *testing.T) {
			given := []string{f.Name}
			if f.Value != "" {
				given = append(given, values[f.Name])
			}

			if _, err := drivectl.ParseDriveArgs(append([]string{"GH-1"}, given...)); err != nil {
				t.Fatalf("parse %q: %v", given, err)
			}

			if f.Value != "" {
				inline := []string{"GH-1", f.Name + "=" + values[f.Name]}
				if _, err := drivectl.ParseDriveArgs(inline); err != nil {
					t.Fatalf("parse %q: %v", inline, err)
				}
			}

			_, err := drivectl.ParseDriveArgs(append([]string{"--resume", "2"}, given...))
			if took := err == nil; took != f.Resume {
				t.Errorf("--resume with %s: err = %v, want taken %v", f.Name, err, f.Resume)
			}

			kept := drive.RecordArgs(append([]string{"GH-1"}, given...))
			if matched := len(kept) > 1; matched == f.Tunes {
				t.Errorf("recordArgs(%q) = %q, want matched %v", given, kept, !f.Tunes)
			}
		})
	}

	_, err := drivectl.ParseDriveArgs([]string{"GH-1", "--force"})
	if err == nil {
		t.Fatal("--force parsed")
	}

	for _, f := range drivectl.DriveFlags {
		if !strings.Contains(err.Error(), f.Name) {
			t.Errorf("the unknown-flag message does not name %s: %v", f.Name, err)
		}
	}

	for _, f := range drivectl.DriveFlags {
		if f.Name != "--resume" && !strings.Contains(drive.DriveUsage(), f.Name) {
			t.Errorf("the usage does not name %s", f.Name)
		}
	}

	if slices.ContainsFunc(drivectl.ResumeTunes(), func(f drivectl.DriveFlag) bool { return !f.Resume }) {
		t.Error("the resume usage names a flag --resume refuses")
	}
}
