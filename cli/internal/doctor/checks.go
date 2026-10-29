package doctor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/layout"
)

// Check names, in the order Run reports them.
const (
	CheckVersion    = "version"
	CheckScripts    = "scripts"
	CheckHooks      = "hooks"
	CheckStatusline = "statusline"
	CheckHerdr      = "herdr"
	CheckHost       = "host"
	CheckProject    = "project"
)

// herdrScope is every command that runs inside a Herdr session.
var herdrScope = []string{"start", "forward", "drive", "finish"}

// stateScript is the plugin script the status line runs and steps call; finding it on PATH finds the plugin.
const stateScript = "f10-state.sh"

// shimName is the plugin's bin/f10, which resolves and runs the binary.
const shimName = "f10"

// originSession marks a plugin found on the session's PATH: loaded, so enabled.
const originSession = "this session's PATH"

// statuslineScope is what a statusline problem affects: the badge, nothing a run reads.
var statuslineScope = []string{"status-line badge"}

// A plugin is the install the checks judge: inside a Claude Code session
// the one its PATH reaches, which may be a directory marketplace or a
// --plugin-dir rather than the versioned install, else the recorded install.
type plugin struct {
	Root    string
	Version string
	Origin  string // where Root came from, for the detail lines
	ID      string // <name>@<marketplace>, what the claude CLI takes
}

func inSession(env *Env) bool { return env.Getenv("CLAUDECODE") == "1" }

func resolvePlugin(env *Env, in *install) plugin {
	p := plugin{ID: in.id()}

	if inSession(env) {
		if path, err := env.LookPath(stateScript); err == nil {
			root := filepath.Dir(realDir(filepath.Dir(path)))
			if name, version := readManifest(root); name == pluginName {
				p.Root, p.Version, p.Origin = root, version, originSession

				return p
			}
		}
	}

	if in != nil {
		p.Root, p.Version, p.Origin = in.InstallPath, in.Version, "installed_plugins.json"
		if _, version := readManifest(in.InstallPath); version != "" {
			p.Version = version
		}
	}

	return p
}

// readManifest is the name and version in a plugin root's plugin.json.
func readManifest(root string) (string, string) {
	data, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return "", ""
	}

	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}

	if json.Unmarshal(data, &m) != nil {
		return "", ""
	}

	return m.Name, m.Version
}

// notInstalled is the result every plugin-side check gives without a plugin.
func notInstalled(name, reason string, p plugin) Result {
	return Result{
		Name:   name,
		Status: StatusProblem,
		Detail: reason,
		Remedy: "claude plugin install " + p.ID,
	}
}

// checkVersion compares the plugin's version with the binary's: the skills
// describe the binary's flags, so the two must be one release.
func checkVersion(env *Env, p plugin, reason string) Result {
	if p.Root == "" {
		return notInstalled(CheckVersion, reason, p)
	}

	// the shim fetches or builds the binary for the plugin's version; a
	// mismatch means some other f10 ran this
	install := "run " + filepath.Join(p.Root, "bin", shimName) + ", which fetches or builds " + p.Version +
		", in place of the f10 earlier on PATH"
	r := Result{Name: CheckVersion}

	switch {
	case env.Version == "" || env.Version == "unknown":
		r.Status, r.Detail, r.Remedy = StatusProblem, "the binary carries no version; plugin "+p.Version, install
	case strings.HasPrefix(env.Version, "devel"):
		r.Status = StatusOK
		r.Detail = "binary is a dev build (" + env.Version + "), not compared with plugin " + p.Version
	case env.Version == p.Version:
		r.Status, r.Detail = StatusOK, "plugin and binary are both "+p.Version
	default:
		r.Status = StatusProblem
		r.Detail = "plugin " + p.Version + ", binary " + env.Version

		r.Remedy = install
		if older(p.Version, env.Version) {
			r.Remedy = "claude plugin update " + p.ID + ", then restart Claude Code"
		}
	}

	return r
}

// older reports whether version a is below b, comparing the X.Y.Z core; a
// version that does not parse is never older.
func older(a, b string) bool {
	ca, okA := core(a)
	cb, okB := core(b)

	return okA && okB && slices.Compare(ca, cb) < 0
}

func core(v string) ([]int, bool) {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil, false
	}

	out := make([]int, 0, len(parts))

	for _, s := range parts {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, false
		}

		out = append(out, n)
	}

	return out, true
}

// checkScripts lists the plugin's bin/, so a new script is covered without
// naming it, and inside a session checks each resolves there.
func checkScripts(env *Env, p plugin, reason string) Result {
	if p.Root == "" {
		return notInstalled(CheckScripts, reason, p)
	}

	bin := filepath.Join(p.Root, "bin")
	r := Result{Name: CheckScripts}

	entries, err := os.ReadDir(bin)
	if err != nil {
		r.Status, r.Detail, r.Remedy = StatusProblem, bin+" is missing", "claude plugin update "+p.ID

		return r
	}

	var names, broken []string

	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}

		names = append(names, e.Name())

		if fi, err := os.Stat(filepath.Join(bin, e.Name())); err != nil || fi.Mode()&0o111 == 0 {
			broken = append(broken, e.Name())
		}
	}

	if len(broken) > 0 {
		r.Status = StatusProblem
		r.Detail = "not executable in " + bin + ": " + strings.Join(broken, ", ")
		r.Remedy = "claude plugin update " + p.ID

		return r
	}

	if !inSession(env) {
		r.Status = StatusOK
		r.Detail = strconv.Itoa(len(names)) + " scripts in " + bin + "; on PATH inside Claude Code sessions only"

		return r
	}

	for _, name := range names {
		path, err := env.LookPath(name)
		if err != nil {
			r.Status, r.Detail = StatusProblem, name+" is not on this session's PATH"
		} else if !samePath(filepath.Dir(path), bin) {
			r.Status, r.Detail = StatusProblem, name+" resolves to "+path+", not "+bin
		}

		if r.Status == StatusProblem {
			r.Remedy = "restart Claude Code: a session puts the plugin's bin/ on PATH when it starts"
			if name == shimName && err == nil {
				r.Remedy = "remove " + path + ", or restart Claude Code: the SessionStart hook puts the plugin's bin/ first"
			}

			return r
		}
	}

	r.Status = StatusOK
	r.Detail = strconv.Itoa(len(names)) + " scripts resolve from " + bin + " (" + p.Origin + ")"

	return r
}

// checkHooks tells "not registered" from "registered, but needs a restart".
func checkHooks(env *Env, p plugin, in *install, reason string, set settings) Result {
	if p.Root == "" {
		return notInstalled(CheckHooks, reason, p)
	}

	r := Result{Name: CheckHooks}
	hooksFile := filepath.Join(p.Root, "hooks", "hooks.json")

	var hooks struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}

	switch data, err := os.ReadFile(hooksFile); {
	case in != nil && p.Origin != originSession && !set.Enabled[in.Key]:
		r.Status = StatusProblem
		r.Detail = in.Key + " is installed but not enabled in any settings file"
		r.Remedy = "claude plugin enable " + in.Key

		return r
	case err != nil:
		r.Status, r.Detail, r.Remedy = StatusProblem, hooksFile+" is missing", "claude plugin update "+p.ID

		return r
	case json.Unmarshal(data, &hooks) != nil || len(hooks.Hooks) == 0:
		r.Status, r.Detail, r.Remedy = StatusProblem, hooksFile+" declares no hooks", "claude plugin update "+p.ID

		return r
	}

	restart := "restart Claude Code: hooks load when a session starts"

	started, which, ok := lastStart(env)
	if !ok {
		r.Status, r.Detail, r.Remedy = StatusProblem, "registered, but no session has run the SessionStart hook yet", restart

		return r
	}

	if in != nil {
		if updated, err := time.Parse(time.RFC3339, in.LastUpdated); err == nil && updated.After(started) {
			r.Status = StatusProblem
			r.Detail = "registered, but the plugin changed at " + updated.Local().Format(time.DateTime) +
				", after " + which + " at " + started.Format(time.DateTime)
			r.Remedy = restart

			return r
		}
	}

	r.Status = StatusOK
	r.Detail = strconv.Itoa(len(hooks.Hooks)) + " hook events registered from " + hooksFile

	return r
}

// lastStart is when this session started, by the marker its SessionStart
// hook wrote, else when any session last did, by the statusline symlink
// every start re-points: an older plugin writes no marker.
func lastStart(env *Env) (time.Time, string, bool) {
	if sid := sessionID(env); sid != "" {
		if fi, err := os.Stat(filepath.Join(stateDir(env), sid+".started")); err == nil {
			return fi.ModTime(), "this session started", true
		}
	}

	if fi, err := os.Lstat(statuslineLink(env)); err == nil {
		return fi.ModTime(), "the last session start", true
	}

	return time.Time{}, "", false
}

// sessionID and stateDir mirror internal/state's Session and Dir over env, so a test can fake them.
func sessionID(env *Env) string {
	sid := env.Getenv("F10_SESSION_ID")
	if sid == "" {
		sid = env.Getenv("CLAUDE_CODE_SESSION_ID")
	}

	return strings.Map(func(r rune) rune {
		if r == '.' || r == '_' || r == '-' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') ||
			('0' <= r && r <= '9') {
			return r
		}

		return '_'
	}, sid)
}

func stateDir(env *Env) string {
	if d := env.Getenv("F10_STATE_DIR"); d != "" {
		return d
	}

	return filepath.Join(env.Home, ".claude", "f10", "state")
}

func statuslineLink(env *Env) string {
	return filepath.Join(env.Home, ".claude", "f10", "statusline")
}

func checkStatusline(env *Env, set settings) Result {
	r := Result{Name: CheckStatusline, Scope: statuslineScope}
	link := statuslineLink(env)

	if !strings.Contains(set.StatusLine, "f10/statusline") && !strings.Contains(set.StatusLine, stateScript) {
		r.Status = StatusProblem
		r.Detail = "statusLine does not run " + link
		r.Remedy = "add the f10 segment from docs/statusline.md to statusLine in " + set.User

		return r
	}

	if _, err := os.Stat(link); err != nil {
		r.Status = StatusProblem
		r.Detail = link + " is missing or points at nothing"
		r.Remedy = "start a new Claude Code session: its SessionStart hook relinks it"

		return r
	}

	r.Status = StatusOK
	r.Scope = nil

	r.Detail = "statusLine in " + set.From + " runs " + link
	if !set.Refresh {
		r.Detail += "; set statusLine.refreshInterval to 2 so a long step does not show a stale glyph"
	}

	return r
}

// checkHerdr is scoped: without Herdr only the commands that open
// workspaces and reach agents stop working.
func checkHerdr(env *Env) Result {
	r := Result{Name: CheckHerdr, Scope: herdrScope}

	path, err := env.LookPath("herdr")
	if err != nil {
		r.Status = StatusProblem
		r.Detail = "herdr is not on PATH"
		r.Remedy = "install Herdr from https://herdr.dev"

		return r
	}

	r.Status = StatusOK
	r.Scope = nil

	r.Detail = path
	if env.Getenv("HERDR_ENV") != "1" {
		r.Detail += "; not inside a Herdr pane, where " + strings.Join(herdrScope, ", ") + " must run"
	}

	return r
}

func checkHost(ctx context.Context, env *Env) Result {
	r := Result{Name: CheckHost}
	pb := env.Probes

	if pb == nil || pb.CLI.Value == "none" {
		r.Status, r.Detail = StatusNotNeeded, "no remote host"
		if pb != nil {
			r.Detail = pb.CLI.Evidence
		}

		return r
	}

	if pb.CLI.Value != "gh" && pb.CLI.Value != "glab" {
		r.Status = StatusProblem
		r.Detail = pb.CLI.Evidence
		r.Remedy = "gh auth login --hostname " + pb.Host + " (GitHub) or glab auth login --hostname " + pb.Host + " (GitLab)"

		return r
	}

	cli := pb.CLI.Value
	if _, err := env.LookPath(cli); err != nil {
		r.Status, r.Detail, r.Remedy = StatusProblem, cli+" is not on PATH", "install "+cli

		return r
	}

	res, err := env.Run(ctx, env.Layout.CheckoutRoot, cli, "auth", "status", "--hostname", pb.Host)
	if err != nil || res.Code != 0 {
		r.Status = StatusProblem
		r.Detail = cli + " is not signed in to " + pb.Host
		r.Remedy = cli + " auth login --hostname " + pb.Host

		return r
	}

	r.Status, r.Detail = StatusOK, cli+" is signed in to "+pb.Host

	return r
}

func checkProject(env *Env) Result {
	r := Result{Name: CheckProject}

	if env.Layout == nil || len(env.Layout.Layers) == 0 {
		r.Status, r.Detail = StatusNotNeeded, "no project.md here; inferred defaults apply"

		return r
	}

	file := projectFile(env.Layout)

	var errs, warnings []string

	if env.Facts != nil {
		for _, p := range env.Facts.Problems {
			if p.Error {
				errs = append(errs, p.Message)
			} else {
				warnings = append(warnings, p.Message)
			}
		}
	}

	if len(errs) > 0 {
		r.Status = StatusProblem
		r.Detail = errs[0]

		if len(errs) > 1 {
			r.Detail += " (+" + strconv.Itoa(len(errs)-1) + " more)"
		}

		r.Remedy = "edit " + file + "; f10 config shows every fact and its origin"

		return r
	}

	r.Status = StatusOK

	r.Detail = file + " parses"
	if len(warnings) > 0 {
		r.Detail += ", " + strconv.Itoa(len(warnings)) + " warning(s) in f10 config"
	}

	return r
}

// projectFile is the project.md of the layer that wins.
func projectFile(lay *layout.Layout) string {
	return filepath.Join(lay.Layers[len(lay.Layers)-1].Dir, "project.md")
}

func realDir(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}

	return dir
}
