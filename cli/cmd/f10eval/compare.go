package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
)

// compareMain prints two report sets side by side, a baseline and a variant,
// marking a difference only when it exceeds the spread across repeats.
func compareMain(w io.Writer, root string, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: f10eval compare <baseline-dir> <variant-dir>")
	}

	base, err := loadSet(args[0], resolveDir(root, args[0]))
	if err != nil {
		return err
	}

	variant, err := loadSet(args[1], resolveDir(root, args[1]))
	if err != nil {
		return err
	}

	writeComparison(w, base, variant)

	return nil
}

// A reportSet is one directory's reports, grouped by scenario and profile.
type reportSet struct {
	dir     string
	groups  map[groupKey][]Report
	commits []string
}

type groupKey struct{ scenario, agent string }

func (k groupKey) String() string {
	if k.agent == "" {
		return k.scenario
	}

	return k.scenario + " (" + k.agent + ")"
}

// loadSet reads dir; name is the path as typed, for the output.
func loadSet(name, dir string) (reportSet, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return reportSet{}, err
	}

	if len(files) == 0 {
		return reportSet{}, fmt.Errorf("no reports under %s", dir)
	}

	set := reportSet{dir: name, groups: map[groupKey][]Report{}}

	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			return reportSet{}, err
		}

		var r Report
		if err := json.Unmarshal(body, &r); err != nil {
			return reportSet{}, fmt.Errorf("%s: %w", f, err)
		}

		k := groupKey{r.Scenario, r.Agent}
		set.groups[k] = append(set.groups[k], r)

		if !slices.Contains(set.commits, r.Commit) {
			set.commits = append(set.commits, r.Commit)
		}
	}

	return set, nil
}

// A metric is one row of a group: its samples on one side. A binary metric
// holds 1 or 0 per sample and prints as k/N; any other prints its mean and
// min-max range.
type metric struct {
	name   string
	binary bool
	higher bool // higher is better
	unit   string
	values []float64
}

// metrics is a group's rows on one side. A run that errored in setup returned
// before its checks ran, so it counts only in the errors row. A turn error
// (budget, timeout) still ran the checks and spent, so it counts everywhere.
func metrics(runs []Report, checkNames []string) []metric {
	errs := metric{name: "errors", binary: true}
	pass := metric{name: "pass", binary: true, higher: true}
	found := metric{name: "planted found", higher: true}
	outside := metric{name: "planted outside"}
	rubric := metric{name: "rubric lines passed", higher: true}
	cost := metric{name: "cost", unit: "$"}
	dur := metric{name: "duration", unit: "s"}

	byCheck := map[string]*metric{}
	for _, n := range checkNames {
		byCheck[n] = &metric{name: "check " + n, binary: true, higher: true}
	}

	for _, r := range runs {
		errs.values = append(errs.values, b2f(r.Error != ""))
		if r.Error != "" && len(r.Checks) == 0 {
			continue
		}

		pass.values = append(pass.values, b2f(r.Pass))
		cost.values = append(cost.values, r.CostUSD)
		dur.values = append(dur.values, float64(r.DurationMS)/1000)

		for _, c := range r.Checks {
			m := byCheck[c.Name]
			m.values = append(m.values, b2f(c.Pass))

			if c.Planted != nil {
				found.values = append(found.values, float64(c.Planted.Found))
				outside.values = append(outside.values, float64(c.Planted.Outside))
			}

			if c.Rubric != nil {
				n := 0

				for _, l := range c.Rubric.Lines {
					if l.Pass {
						n++
					}
				}

				rubric.values = append(rubric.values, float64(n))
			}
		}
	}

	out := []metric{errs, pass}
	for _, n := range checkNames {
		out = append(out, *byCheck[n])
	}

	return append(out, found, outside, rubric, cost, dur)
}

func b2f(b bool) float64 {
	if b {
		return 1
	}

	return 0
}

func (m metric) String() string {
	n := len(m.values)
	if n == 0 {
		return "-"
	}

	mean, lo, hi := m.stats()
	if m.binary {
		return fmt.Sprintf("%d/%d", int(mean*float64(n)+0.5), n)
	}

	f := func(v float64) string {
		switch m.unit {
		case "$":
			return fmt.Sprintf("$%.2f", v)
		case "s":
			return fmt.Sprintf("%.0fs", v)
		}

		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", v), "0"), ".0")
	}

	return fmt.Sprintf("%s [%s-%s] n=%d", f(mean), f(lo), f(hi), n)
}

// stats is the mean, the min and the max of the samples.
func (m metric) stats() (float64, float64, float64) {
	if len(m.values) == 0 {
		return 0, 0, 0
	}

	sum, lo, hi := 0.0, m.values[0], m.values[0]

	for _, v := range m.values {
		sum += v
		lo, hi = min(lo, v), max(hi, v)
	}

	return sum / float64(len(m.values)), lo, hi
}

// The fewest samples each side needs before a metric is marked at all. One
// run has no spread to judge by, and below three a pass rate's single flip
// is the whole difference.
const (
	minSamples       = 2
	minBinarySamples = 3
)

// mark compares one metric across the sides, and returns "better", "worse"
// or "". A pass rate is marked when the rates differ by at least two runs of
// the smaller side, because min-max cannot mark it: one pass and one fail
// already span [0-1]. Any other metric is marked when its ranges do not
// overlap.
func mark(base, variant metric) string {
	nb, nv := len(base.values), len(variant.values)
	if nb == 0 || nv == 0 {
		return ""
	}

	mb, lb, hb := base.stats()
	mv, lv, hv := variant.stats()

	var up bool

	if base.binary {
		if min(nb, nv) < minBinarySamples || abs(mv-mb) < 2/float64(min(nb, nv))-1e-9 {
			return ""
		}

		up = mv > mb
	} else {
		switch {
		case min(nb, nv) < minSamples:
			return ""
		case lv > hb:
			up = true
		case hv < lb:
			up = false
		default:
			return ""
		}
	}

	if up == base.higher {
		return "better"
	}

	return "worse"
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}

	return v
}

func writeComparison(w io.Writer, base, variant reportSet) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	side := func(label string, s reportSet) {
		runs := 0
		for _, g := range s.groups {
			runs += len(g)
		}

		fmt.Fprintf(tw, "%s\t%s\tcommit %s\t%d runs\n", label, s.dir, strings.Join(s.commits, ", "), runs)
	}

	side("baseline", base)
	side("variant", variant)
	_ = tw.Flush()

	for _, warn := range warnings(base, variant) {
		fmt.Fprintln(w, "warning:", warn)
	}

	var only []string

	for _, k := range groupKeys(base, variant) {
		b, inB := base.groups[k]
		v, inV := variant.groups[k]

		switch {
		case !inV:
			only = append(only, k.String()+": baseline only")
			continue
		case !inB:
			only = append(only, k.String()+": variant only")
			continue
		}

		names := checkNames(b, v)
		bm, vm := metrics(b, names), metrics(v, names)

		fmt.Fprintf(w, "\n%s\n", k)
		fmt.Fprintf(tw, "  \tbaseline\tvariant\t\n")

		for i := range bm {
			if len(bm[i].values) == 0 && len(vm[i].values) == 0 {
				continue
			}

			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", bm[i].name, bm[i], vm[i], mark(bm[i], vm[i]))
		}

		_ = tw.Flush()
	}

	if len(only) > 0 {
		fmt.Fprintln(w)

		for _, o := range only {
			fmt.Fprintln(w, "not compared:", o)
		}
	}
}

// warnings names what makes a comparison weaker than it looks: a group with
// one run has no spread, and a set holding several commits usually mixes two
// runs in one directory.
func warnings(sets ...reportSet) []string {
	var out []string

	for _, s := range sets {
		single := 0

		for _, g := range s.groups {
			if len(g) == 1 {
				single++
			}
		}

		if single > 0 {
			out = append(out, fmt.Sprintf("%s: %d of %d groups have one run, so none of their rows is marked",
				s.dir, single, len(s.groups)))
		}

		if len(s.commits) > 1 {
			out = append(out, fmt.Sprintf("%s holds reports from %d commits", s.dir, len(s.commits)))
		}
	}

	slices.Sort(out)

	return out
}

func groupKeys(sets ...reportSet) []groupKey {
	var keys []groupKey

	for _, s := range sets {
		for k := range s.groups {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}

	slices.SortFunc(keys, func(a, b groupKey) int {
		return cmp.Or(cmp.Compare(a.scenario, b.scenario), cmp.Compare(a.agent, b.agent))
	})

	return keys
}

// checkNames is every check either side ran, in the order first seen.
func checkNames(sides ...[]Report) []string {
	var names []string

	for _, runs := range sides {
		for _, r := range runs {
			for _, c := range r.Checks {
				if !slices.Contains(names, c.Name) {
					names = append(names, c.Name)
				}
			}
		}
	}

	return names
}
