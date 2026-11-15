package remote

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

func testService(t *testing.T, goos string) (*Service, *[]string) {
	t.Helper()

	var ran []string

	return &Service{
		GOOS:    goos,
		Home:    t.TempDir(),
		UID:     501,
		Program: "/Users/me/Plugins & Co/bin/f10",
		Env:     []string{"PATH=/opt/homebrew/bin:/usr/bin", "F10_RUN_DIR=/tmp/run 100%"},
		Log:     "/tmp/run/daemon.log",
		Run: func(_ context.Context, name string, args ...string) error {
			ran = append(ran, name+" "+strings.Join(args, " "))

			return nil
		},
	}, &ran
}

// The service runs the daemon in service mode with the installing shell's
// environment, and its program reads back from the file it wrote.
func TestServiceFiles(t *testing.T) {
	t.Parallel()

	for goos, want := range map[string][]string{
		"darwin": {
			"<string>io.github.amberpixels.f10.daemon</string>",
			"<string>/Users/me/Plugins &amp; Co/bin/f10</string>",
			"<string>--service</string>",
			"<key>KeepAlive</key>\n\t<true/>",
			"<key>ExitTimeOut</key>\n\t<integer>660</integer>",
			"<key>F10_RUN_DIR</key>\n\t\t<string>/tmp/run 100%</string>",
		},
		"linux": {
			`ExecStart="/Users/me/Plugins & Co/bin/f10" daemon --service`,
			`Environment="F10_RUN_DIR=/tmp/run 100%%"`,
			"Restart=always",
			"KillMode=mixed",
			"TimeoutStopSec=11min",
			"WantedBy=default.target",
		},
	} {
		s, _ := testService(t, goos)

		text, err := s.Render()
		if err != nil {
			t.Fatal(err)
		}

		for _, w := range want {
			if !strings.Contains(text, w) {
				t.Errorf("%s service lacks %q:\n%s", goos, w, text)
			}
		}

		if err := s.Install(t.Context()); err != nil {
			t.Fatal(err)
		}

		if prog, ok := s.InstalledProgram(); !ok || prog != s.Program {
			t.Errorf("%s: InstalledProgram = %q, %v; want %q", goos, prog, ok, s.Program)
		}
	}
}

func TestServiceInstallAndUninstallCommands(t *testing.T) {
	t.Parallel()

	for goos, want := range map[string][]string{
		"darwin": {
			"launchctl bootout gui/501 ",
			"launchctl bootstrap gui/501 ",
			"launchctl bootout gui/501 ",
		},
		"linux": {
			"systemctl --user daemon-reload",
			"systemctl --user enable f10-daemon.service",
			"systemctl --user restart f10-daemon.service",
			"systemctl --user disable --now f10-daemon.service",
			"systemctl --user daemon-reload",
		},
	} {
		s, ran := testService(t, goos)

		// a reinstall restarts the daemon, so its drives are drained for
		// the restarted one first
		s.Drain = func(context.Context) error {
			*ran = append(*ran, "drain")

			return nil
		}

		if err := s.Install(t.Context()); err != nil {
			t.Fatal(err)
		}

		if (*ran)[0] != "drain" {
			t.Errorf("%s: install ran %q before draining the daemon", goos, (*ran)[0])
		}

		*ran = (*ran)[1:]

		ok, err := s.Uninstall(t.Context())
		if !ok || err != nil {
			t.Fatalf("%s: Uninstall = %v, %v", goos, ok, err)
		}

		path, _ := s.Path()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s: service file left behind", goos)
		}

		if len(*ran) != len(want) || !slices.EqualFunc(*ran, want, strings.HasPrefix) {
			t.Errorf("%s ran %q, want %q", goos, *ran, want)
		}

		if ok, _ := s.Uninstall(t.Context()); ok {
			t.Errorf("%s: a second uninstall found a service", goos)
		}
	}

	if _, err := (&Service{GOOS: "windows"}).Path(); err == nil {
		t.Error("a windows service path was given")
	}
}
