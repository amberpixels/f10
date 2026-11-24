package remote

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

// ServiceLabel names the launchd agent; ServiceUnit the systemd user unit.
const (
	ServiceLabel = "io.github.amberpixels.f10.daemon"
	ServiceUnit  = "f10-daemon.service"
)

// ServiceEnv is the environment the service carries from the shell that
// installed it: launchd starts a process with almost none, and without these
// the daemon would land in another run dir and find none of the user's tools.
var ServiceEnv = []string{"PATH", "HOME", "F10_RUN_DIR", "XDG_RUNTIME_DIR", "F10_USER_DIR"}

// ErrNoService is a platform with neither launchd nor systemd.
var ErrNoService = errors.New("f10 remote install supports macOS (launchd) and Linux (systemd)")

// A Service is the daemon's launchd agent or systemd user unit.
type Service struct {
	GOOS    string
	Home    string
	UID     int
	Program string   // the f10 the service runs
	Env     []string // KEY=VALUE pairs it carries
	Log     string   // where its output goes
	// Run runs launchctl or systemctl.
	Run func(ctx context.Context, name string, args ...string) error
	// Drain hands the running daemon's drives to the next one before the
	// service restarts, so reinstalling never drops them; nil skips it.
	Drain func(ctx context.Context) error
}

// Path is the service file.
func (s *Service) Path() (string, error) {
	switch s.GOOS {
	case "darwin":
		return filepath.Join(s.Home, "Library", "LaunchAgents", ServiceLabel+".plist"), nil
	case "linux":
		return filepath.Join(s.Home, ".config", "systemd", "user", ServiceUnit), nil
	}

	return "", ErrNoService
}

// Render is the service file's text.
func (s *Service) Render() (string, error) {
	var tmpl *template.Template

	switch s.GOOS {
	case "darwin":
		tmpl = plistTmpl
	case "linux":
		tmpl = unitTmpl
	default:
		return "", ErrNoService
	}

	type kv struct{ Key, Value string }

	env := make([]kv, 0, len(s.Env))
	for _, e := range s.Env {
		k, v, _ := strings.Cut(e, "=")
		env = append(env, kv{k, v})
	}

	var b strings.Builder

	err := tmpl.Execute(&b, map[string]any{
		"Label": ServiceLabel, "Program": s.Program, "Env": env, "Log": s.Log,
	})

	return b.String(), err
}

// Install writes the service file and loads it, replacing a loaded one.
func (s *Service) Install(ctx context.Context) error {
	path, err := s.Path()
	if err != nil {
		return err
	}

	text, err := s.Render()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // launchd and systemd read it
		return err
	}

	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // as above
		return err
	}

	// a plain SIGTERM from the restart below would stop each drive for good;
	// an upgrade drain lets the restarted daemon resume them
	if s.Drain != nil {
		if err := s.Drain(ctx); err != nil {
			return err
		}
	}

	if s.GOOS == "darwin" {
		// bootout fails when nothing is loaded, which is the usual case
		_ = s.Run(ctx, "launchctl", "bootout", s.domain(), path)

		return s.Run(ctx, "launchctl", "bootstrap", s.domain(), path)
	}

	for _, args := range [][]string{{"daemon-reload"}, {"enable", ServiceUnit}, {"restart", ServiceUnit}} {
		if err := s.Run(ctx, "systemctl", append([]string{"--user"}, args...)...); err != nil {
			return err
		}
	}

	return nil
}

// Uninstall unloads the service and removes its file; false when none was
// installed.
func (s *Service) Uninstall(ctx context.Context) (bool, error) {
	path, err := s.Path()
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if s.GOOS == "darwin" {
		_ = s.Run(ctx, "launchctl", "bootout", s.domain(), path)
	} else {
		_ = s.Run(ctx, "systemctl", "--user", "disable", "--now", ServiceUnit)
	}

	if err := os.Remove(path); err != nil {
		return true, err
	}

	if s.GOOS == "linux" {
		return true, s.Run(ctx, "systemctl", "--user", "daemon-reload")
	}

	return true, nil
}

var (
	plistProgram = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)
	unitProgram  = regexp.MustCompile(`(?m)^ExecStart="([^"]*)"`)
)

// InstalledProgram is the program an installed service file runs; "" when
// the file is missing or names none.
func (s *Service) InstalledProgram() (string, bool) {
	path, err := s.Path()
	if err != nil {
		return "", false
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}

	re := unitProgram
	if s.GOOS == "darwin" {
		re = plistProgram
	}

	m := re.FindSubmatch(raw)
	if m == nil {
		return "", true
	}

	if s.GOOS == "darwin" {
		return xmlUnescape(string(m[1])), true
	}

	return systemdUnquote(string(m[1])), true
}

func xmlEscape(v string) string {
	var b strings.Builder

	_ = xml.EscapeText(&b, []byte(v))

	return b.String()
}

func xmlUnescape(v string) string {
	var out string
	if err := xml.Unmarshal([]byte("<s>"+v+"</s>"), &out); err != nil {
		return v
	}

	return out
}

// systemdQuote is v inside a double-quoted unit value: backslash and quote
// escaped, and % doubled so systemd reads no specifier.
func systemdQuote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)

	return strings.ReplaceAll(v, "%", "%%")
}

func systemdUnquote(v string) string {
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`, "%%", "%").Replace(v)
}

var plistTmpl = template.Must(template.New("plist").Funcs(template.FuncMap{"x": xmlEscape}).Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{x .Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{x .Program}}</string>
		<string>daemon</string>
		<string>--service</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
{{- range .Env}}
		<key>{{x .Key}}</key>
		<string>{{x .Value}}</string>
{{- end}}
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ExitTimeOut</key>
	<integer>660</integer>
	<key>StandardOutPath</key>
	<string>{{x .Log}}</string>
	<key>StandardErrorPath</key>
	<string>{{x .Log}}</string>
</dict>
</plist>
`))

var unitTmpl = template.Must(template.New("unit").Funcs(template.FuncMap{"q": systemdQuote}).Parse(
	`[Unit]
Description=f10 daemon: batches, watches and the control-plane API

[Service]
ExecStart="{{q .Program}}" daemon --service
{{- range .Env}}
Environment="{{q .Key}}={{q .Value}}"
{{- end}}
Restart=always
RestartSec=5
KillMode=mixed
TimeoutStopSec=11min
StandardOutput=append:{{.Log}}
StandardError=append:{{.Log}}

[Install]
WantedBy=default.target
`))

// Describe is the service's state in one line, for install's report.
func (s *Service) Describe() string {
	path, err := s.Path()
	if err != nil {
		return err.Error()
	}

	return fmt.Sprintf("%s, running %s daemon --service", path, s.Program)
}

// Supported reports a platform with a service manager f10 installs into.
func (s *Service) Supported() bool { return s.GOOS == "darwin" || s.GOOS == "linux" }

func (s *Service) domain() string { return "gui/" + strconv.Itoa(s.UID) }
