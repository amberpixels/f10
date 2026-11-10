package main

import (
	"slices"
	"strings"
	"testing"
)

// Every row of the flag table parses, is or is not taken by --resume, and
// is or is not part of the record's match, as the row says.
func TestDriveFlagTable(t *testing.T) {
	values := map[string]string{
		"--flow": "local", "--pace": "fast", "--agent": "opus", "--model": "opus",
		"--answer": "1. yes", "--every": "5s", "--parallel": "3", "--resume": "2",
	}

	for _, f := range driveFlags {
		if f.name == "--resume" {
			continue
		}

		t.Run(f.name, func(t *testing.T) {
			given := []string{f.name}
			if f.value != "" {
				given = append(given, values[f.name])
			}

			if _, err := parseDriveArgs(append([]string{"GH-1"}, given...)); err != nil {
				t.Fatalf("parse %q: %v", given, err)
			}

			if f.value != "" {
				inline := []string{"GH-1", f.name + "=" + values[f.name]}
				if _, err := parseDriveArgs(inline); err != nil {
					t.Fatalf("parse %q: %v", inline, err)
				}
			}

			_, err := parseDriveArgs(append([]string{"--resume", "2"}, given...))
			if took := err == nil; took != f.resume {
				t.Errorf("--resume with %s: err = %v, want taken %v", f.name, err, f.resume)
			}

			kept := recordArgs(append([]string{"GH-1"}, given...))
			if matched := len(kept) > 1; matched == f.tunes {
				t.Errorf("recordArgs(%q) = %q, want matched %v", given, kept, !f.tunes)
			}
		})
	}

	_, err := parseDriveArgs([]string{"GH-1", "--force"})
	if err == nil {
		t.Fatal("--force parsed")
	}

	for _, f := range driveFlags {
		if !strings.Contains(err.Error(), f.name) {
			t.Errorf("the unknown-flag message does not name %s: %v", f.name, err)
		}
	}

	for _, f := range driveFlags {
		if f.name != "--resume" && !strings.Contains(driveUsage(), f.name) {
			t.Errorf("the usage does not name %s", f.name)
		}
	}

	if slices.ContainsFunc(resumeTunes(), func(f driveFlag) bool { return !f.resume }) {
		t.Error("the resume usage names a flag --resume refuses")
	}
}
