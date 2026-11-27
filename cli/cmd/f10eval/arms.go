package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

// Arms is a scenario's opt-in to `f10eval arms`: the plain prompts run beside
// its f10 prompt. They are written by hand in scenario.json, because what the
// plain arm was told is the thing being compared.
type Arms struct {
	Raw    *Arm `json:"raw"`
	Native *Arm `json:"native"` // none skips the native arm
}

// An Arm is one plain way to run a scenario. PermissionModes holds one mode
// per turn, an empty one meaning the run's -permission-mode.
type Arm struct {
	Prompt          string   `json:"prompt"`
	Prompts         []string `json:"prompts"`
	PermissionModes []string `json:"permission_modes"`
}

const (
	armRaw    = "raw"
	armNative = "native"
	armF10    = "f10"
)

// armOrder is the order arms run and print in, the plainest first.
var armOrder = []string{armRaw, armNative, armF10}

func plainArm(arm string) bool { return arm == armRaw || arm == armNative }

// outcomeChecks judge the result alone, so they compare across arms. The
// rest read f10's own state, which a plain arm never writes.
var outcomeChecks = map[string]bool{
	"verify": true, "accept": true, "commit": true, "planted": true, "findings": true, "rubric": true,
}

// plainFindings is the file a plain arm's review prompt writes its findings
// to, in the format the planted check parses.
const plainFindings = "findings.md"

// permissionModes are claude's --permission-mode choices, as `claude --help`
// lists them.
var permissionModes = []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}

func (a Arm) turns() []string {
	if len(a.Prompts) > 0 {
		return a.Prompts
	}

	return []string{a.Prompt}
}

func (a *Arms) check() error {
	if a.Raw == nil {
		return errors.New("no raw arm, the baseline the others are read against")
	}

	for name, arm := range map[string]*Arm{armRaw: a.Raw, armNative: a.Native} {
		if arm == nil {
			continue
		}

		switch {
		case arm.Prompt != "" && len(arm.Prompts) > 0:
			return fmt.Errorf("%s: both prompt and prompts", name)
		case arm.Prompt == "" && len(arm.Prompts) == 0:
			return fmt.Errorf("%s: no prompt", name)
		case len(arm.PermissionModes) > 0 && len(arm.PermissionModes) != len(arm.turns()):
			return fmt.Errorf("%s: %d permission modes for %d turns", name, len(arm.PermissionModes), len(arm.turns()))
		}

		for _, m := range arm.PermissionModes {
			if m != "" && !slices.Contains(permissionModes, m) {
				return fmt.Errorf("%s: permission mode %q is none of %s", name, m, strings.Join(permissionModes, ", "))
			}
		}
	}

	return nil
}

// armTurns is the prompts the run's arm sends and their permission modes.
func (s Scenario) armTurns() ([]string, []string) {
	var arm *Arm

	if s.Arms != nil {
		switch s.arm {
		case armRaw:
			arm = s.Arms.Raw
		case armNative:
			arm = s.Arms.Native
		}
	}

	if arm == nil {
		return s.turns(), nil
	}

	return arm.turns(), arm.PermissionModes
}

func modeAt(modes []string, i int) string {
	if i < len(modes) {
		return modes[i]
	}

	return ""
}

// plainFixture turns a fixture's f10/ into the CLAUDE.md a project without
// f10 would have, then removes it.
func plainFixture(dir string) error {
	f10 := filepath.Join(dir, "f10")

	body, err := os.ReadFile(filepath.Join(f10, "instructions", "project.md"))
	if err == nil {
		//nolint:gosec // a fixture file committed in the run's repo
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(claudeMD(string(body))), 0o644); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return os.RemoveAll(f10)
}

// f10Sections are project.md's sections that bind f10's adapters, which a
// plain arm has no use for.
var f10Sections = []string{"## Tracker", "## Hosting & PR"}

// claudeMD is project.md without its frontmatter and its f10 sections.
func claudeMD(projectMD string) string {
	body := projectMD
	if rest, ok := strings.CutPrefix(body, "---\n"); ok {
		if _, after, found := strings.Cut(rest, "\n---\n"); found {
			body = after
		}
	}

	var out []string

	skip := false

	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "# ") {
			skip = slices.Contains(f10Sections, strings.TrimSpace(line))
		}

		if !skip {
			out = append(out, line)
		}
	}

	return strings.TrimLeft(strings.Join(out, "\n"), "\n")
}

// isolationLeaks collects what the init events show of the user's settings
// despite isolationArgs: a plugin that is neither built in nor this
// checkout's, and any MCP server.
type isolationLeaks struct {
	plugin  string // this checkout, the one plugin an f10 arm loads
	plain   bool
	plugins []string
	servers []string
}

func (l *isolationLeaks) String() string {
	var parts []string

	if len(l.plugins) > 0 {
		parts = append(parts, "user plugins loaded: "+strings.Join(l.plugins, ", "))
	}

	if len(l.servers) > 0 {
		parts = append(parts, "MCP servers loaded: "+strings.Join(l.servers, ", "))
	}

	return strings.Join(parts, "; ")
}

func (l *isolationLeaks) note(e event) {
	if e.Type != "system" || e.Subtype != "init" {
		return
	}

	for _, p := range e.Plugins {
		if p.Path == "builtin" || !l.plain && samePath(p.Path, l.plugin) {
			continue
		}

		if !slices.Contains(l.plugins, p.Name) {
			l.plugins = append(l.plugins, p.Name)
		}
	}

	for _, m := range e.MCPServers {
		if !slices.Contains(l.servers, m.Name) {
			l.servers = append(l.servers, m.Name)
		}
	}
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)

	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}

	return ra == rb
}

// armsMain runs one scenario as each of its arms on this commit, then prints
// a table with a column per arm.
func armsMain(ctx context.Context, w io.Writer, root string, args []string) error {
	var o options

	fs := flag.NewFlagSet("arms", flag.ContinueOnError)
	bindRunFlags(fs, &o, 3, "evals/reports/arms/<version>/<scenario>")

	if err := fs.Parse(args); err != nil {
		return err
	}

	switch {
	case fs.NArg() > 0:
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case o.only == "":
		return errors.New("usage: f10eval arms -scenario <name> [-repeat N] [-agent <profile>] [-out <dir>]")
	case o.repeat < 1:
		return errors.New("-repeat must be 1 or more")
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	o.root = root

	scenarios, err := loadScenarios(filepath.Join(root, "evals", "scenarios"), o.only)
	if err != nil {
		return err
	}

	s := scenarios[0]
	if s.Arms == nil {
		return fmt.Errorf("%s has no arms field, so it has no plain prompt to compare", s.Name)
	}

	b, err := buildCheckout(ctx, root)
	if err != nil {
		return err
	}

	// the set has a dir of its own, so the release dir's rule that repeats
	// need -out is met, and the freshness check still runs
	out := resolveDir(root, cmp.Or(o.out, filepath.Join("evals", "reports", "arms", b.version, s.Name)))

	check := o
	check.out = out

	if err := checkOut(out, check, scenarios); err != nil {
		return err
	}

	plan, err := planRuns(scenarios, o, out, b.f10, armOrder, armProfile)
	if err != nil {
		return err
	}

	err = execute(ctx, root, o, out, b, plan, func(reps []Report) { writeArmsTable(w, newStyles(w), reps) })

	return err
}

// armProfile is the one profile arms run on: -agent, else the scenario's
// first. Arms compare prompts, not models.
func armProfile(s Scenario, only string) ([]agentctl.Profile, error) {
	ps, err := profilesFor(s, only)
	if err != nil {
		return nil, err
	}

	return ps[:1], nil
}

// armRows are compare's rows an arms table drops: plumbing shows as one share.
var armRows = []string{"plumbing calls", "plumbing time", "plumbing cost"}

// armMetrics is compare's rows for one arm, with the plumbing rows folded
// into their share of the run.
func armMetrics(runs []Report, names []string) []metric {
	all := metrics(runs, names)
	out := slices.DeleteFunc(all, func(m metric) bool { return slices.Contains(armRows, m.name) })

	// a report's own pass also needs the f10-only checks, which only the f10
	// arm runs, so across arms pass counts the outcome checks alone
	for i := range out {
		if out[i].name == "pass" {
			out[i] = outcomePass(runs)
		}
	}

	share := metric{name: "plumbing share", unit: "%"}

	for _, r := range runs {
		// RunSeconds is on the transcript's clock, as Share needs
		if r.Error != "" && len(r.Checks) == 0 || r.Plumbing == nil || r.Plumbing.RunSeconds <= 0 {
			continue
		}

		share.values = append(share.values, r.Plumbing.Share())
	}

	return append(out, share)
}

func outcomePass(runs []Report) metric {
	m := metric{name: "pass", binary: true, higher: true}

	for _, r := range runs {
		if r.Error != "" && len(r.Checks) == 0 {
			continue
		}

		pass := r.Error == ""
		for _, c := range r.Checks {
			pass = pass && (c.Pass || !outcomeChecks[c.Name])
		}

		m.values = append(m.values, b2f(pass))
	}

	return m
}

// f10Only says a row means nothing for a plain arm.
func f10Only(row string) bool {
	if name, ok := strings.CutPrefix(row, "check "); ok {
		return !outcomeChecks[name]
	}

	return row == "plumbing share"
}

// writeArmsTable prints a column per arm, each plain one marked against the
// f10 column by compare's rules.
func writeArmsTable(w io.Writer, st *styles, runs []Report) {
	byArm := map[string][]Report{}
	for _, r := range runs {
		byArm[r.Arm] = append(byArm[r.Arm], r)
	}

	var (
		cols  []string
		sides [][]Report
	)

	for _, a := range armOrder {
		if len(byArm[a]) > 0 {
			cols, sides = append(cols, a), append(sides, byArm[a])
		}
	}

	if len(cols) == 0 {
		return
	}

	names := checkNames(sides...)
	ms := make([][]metric, len(cols))

	for i, side := range sides {
		ms[i] = armMetrics(side, names)
	}

	base := slices.Index(cols, armF10)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "each plain arm against f10: better or worse means it beat or trailed f10 on that row")
	fmt.Fprintln(w, "the pass row counts the outcome checks alone, on every arm")

	var leaks []string

	for _, r := range runs {
		if r.Isolation != "" && !slices.Contains(leaks, r.Isolation) {
			leaks = append(leaks, r.Isolation)
		}
	}

	for _, l := range leaks {
		fmt.Fprintln(w, "warning: not isolated:", l)
	}

	table := [][]string{append([]string{""}, cols...)}

	for row := range ms[0] {
		cells := []string{ms[0][row].name}
		sampled := false

		for i, arm := range cols {
			m := ms[i][row]
			sampled = sampled || len(m.values) > 0

			cell := m.String()

			switch {
			case arm != armF10 && f10Only(m.name):
				cell = "f10 only"
			case base >= 0 && i != base:
				if mk := mark(ms[base][row], m); mk != "" {
					cell += " " + st.verdict(mk)
				}
			}

			cells = append(cells, cell)
		}

		if sampled {
			table = append(table, cells)
		}
	}

	printTable(w, table)
}

// printTable pads by display width, because a mark's escape codes would
// shift tabwriter's columns.
func printTable(w io.Writer, rows [][]string) {
	var widths []int

	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}

			widths[i] = max(widths[i], ansi.StringWidth(c))
		}
	}

	for _, r := range rows {
		var b strings.Builder

		b.WriteString("  ")

		for i, c := range r {
			b.WriteString(c)

			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-ansi.StringWidth(c)+2))
			}
		}

		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}
