package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// The standup noun reads what one person did in this checkout over a
// period: their commits from git, the PRs or MRs they authored, reviewed or
// merged from the host, grouped by task id. It changes nothing, so it does
// not fetch: remote-tracking refs are read as they are, and the host's PR
// states are fresh regardless. The user is their git email and their host
// login, never a name, which two people can share.
//
// The period runs since the last standup reported, which `mark` records
// once a report went out. Reading never records: a script calling --json
// does not move the next report's start.
func standupCommand() *cli.Command {
	return &cli.Command{
		Name:  "standup",
		Usage: "what you did in this checkout since your last standup: commits and PRs, grouped by task",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "since",
				Usage: "start the period here instead: an RFC 3339 time, or a `DATE` (YYYY-MM-DD, local midnight)",
			},
			&cli.BoolFlag{Name: "week", Usage: "start the period at last week's Monday"},
			&cli.BoolFlag{Name: "json", Usage: "emit the report as JSON (the schema is in docs/cli.md)"},
		},
		Action: runStandup,
		Commands: []*cli.Command{
			{
				Name:  "mark",
				Usage: "record a standup as reported: the next period starts when this one ended",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "since",
						Usage:    "the `TIME` the reported standup started at, as its report gave it",
						Required: true,
					},
				},
				Action: runStandupMark,
			},
		},
	}
}

func runStandup(ctx context.Context, cmd *cli.Command) error {
	if cmd.IsSet("since") && cmd.Bool("week") {
		return errors.New("--since and --week each set the start: pass one")
	}

	var since time.Time

	if cmd.IsSet("since") {
		var err error
		if since, err = parseSince(cmd.String("since")); err != nil {
			return err
		}
	}

	last, err := readLastStandup()
	if err != nil {
		return err
	}

	workdays, err := readWorkdays()
	if err != nil {
		return err
	}

	now := time.Now()
	start, period := standup.Resolve(now, since, cmd.Bool("week"), last)

	// with no standup stored, one wider collection finds the last working
	// day with activity, and the same input is grouped again from it
	from := start
	if period == standup.PeriodLastWorkingDay {
		from = standup.FallbackFrom(now)
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	h, hostErr := t.host()

	in, err := collectStandup(ctx, standupSource{
		dir: t.lay.CheckoutRoot, main: cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot), prefix: t.eff.IDPrefix,
		host: h, noHost: hostErr != nil,
	}, from)
	if err != nil {
		return err
	}

	if period == standup.PeriodLastWorkingDay {
		in.Since = standup.LastWorkingDay(now, workdays, standup.ActivityTimes(standup.Group(in)))
	}

	rep := standup.Group(in)
	rep.Period = period
	rep.Days = standup.Days(standup.ActivityTimes(rep), time.Local)

	if cmd.Bool("json") {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		return enc.Encode(rep)
	}

	return renderStandup(cmd.Writer, rep)
}

// runStandupMark records a reported standup. It takes the start the report
// showed rather than working it out again, so the record and the report
// agree across midnight and across scopes.
func runStandupMark(_ context.Context, cmd *cli.Command) error {
	since, err := parseSince(cmd.String("since"))
	if err != nil {
		return err
	}

	now := time.Now()
	if since.After(now) {
		return fmt.Errorf("--since %s is in the future", since.Format(time.RFC3339))
	}

	return writeLastStandup(standup.Last{At: now.UTC(), Since: since.UTC()})
}

// parseSince reads --since. The period's other forms (since the last
// standup, a week) are not spellings of this one.
func parseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)

	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	if t, err := time.ParseInLocation(time.DateOnly, s, time.Local); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("--since %q: want an RFC 3339 time or a YYYY-MM-DD date", s)
}

// standupSource is the checkout a standup reads, apart from the target so
// a test can name each part.
type standupSource struct {
	dir, main, prefix string
	host              host
	noHost            bool // no host CLI for this checkout
}

// collectStandup reads git and, where the checkout has one, the host. A
// checkout with no host CLI reports commits only; a host CLI that fails is
// the command's error, never a quiet fall back to commits.
func collectStandup(ctx context.Context, src standupSource, since time.Time) (standup.Input, error) {
	email := gitx.Out(ctx, src.dir, "config", "user.email")
	if email == "" {
		return standup.Input{}, errors.New("git has no user.email here, and standup matches your commits by it " +
			"(git config user.email you@example.com)")
	}

	remotes := strings.Fields(gitx.Out(ctx, src.dir, "remote"))

	defRef, def, err := standupDefault(ctx, src, remotes)
	if err != nil {
		return standup.Input{}, err
	}

	commits, err := userCommits(ctx, src.dir, email, since, defRef, def, remotes)
	if err != nil {
		return standup.Input{}, err
	}

	in := standup.Input{
		Prefix:        src.prefix,
		Since:         since,
		Checkout:      src.dir,
		DefaultBranch: def,
		User:          standup.User{Email: email},
		Commits:       commits,
		Bases:         recordedBases(ctx, src.main),
	}

	if src.noHost {
		return in, nil
	}

	login, err := src.host.login(ctx)
	if err != nil {
		return standup.Input{}, err
	}

	prs, err := src.host.standupPulls(ctx, login, since)
	if err != nil {
		return standup.Input{}, err
	}

	in.Host, in.User.Login, in.PRs = src.host.name, login, prs

	return in, nil
}

// standupDefault is the default branch as a ref to walk and as a branch
// name: origin/HEAD, else the host's answer. Without either, commits that
// landed would read as unfinished work, so it refuses; a checkout with no
// remote at all has no default anyone else sees, and gets none.
func standupDefault(ctx context.Context, src standupSource, remotes []string) (string, string, error) {
	if r := defaultBranch(ctx, src.dir); r != "" {
		return r, refBranch(r, remotes, ""), nil
	}

	if len(remotes) == 0 {
		return "", "", nil
	}

	if src.noHost {
		return "", "", errors.New(
			"cannot tell the default branch: origin has no HEAD here (git remote set-head origin -a)",
		)
	}

	name, err := src.host.defaultBranchName(ctx)
	if err != nil {
		return "", "", err
	}

	for _, r := range []string{"refs/remotes/origin/" + name, "refs/heads/" + name} {
		if gitx.Out(ctx, src.dir, "rev-parse", "--verify", "--quiet", r) != "" {
			return r, name, nil
		}
	}

	return "", name, nil
}

// logFormat is one commit per line, fields split by the unit separator,
// the subject last: it is the only field that can hold anything.
const logFormat = "--format=%H%x1f%aE%x1f%aI%x1f%P%x1f%S%x1f%s"

// userCommits is the commits on any branch, local or remote-tracking, whose
// mailmapped author email is the user's. git log --author would match a
// regex against the name too. --since reads the committer date and only
// narrows the walk; standup.Group keeps commits by author date.
func userCommits(
	ctx context.Context, dir, email string, since time.Time, defRef, def string, remotes []string,
) ([]standup.Commit, error) {
	stamp := "--since=" + since.Format(time.RFC3339)

	out, err := gitx.Exec(ctx, dir, "log", "--branches", "--remotes", "--source", stamp, logFormat)
	if err != nil {
		return nil, err
	}

	// --source names the first ref the walk reached a commit from, which
	// for a fresh task branch is the branch, not the default it came from
	onDefault := map[string]bool{}

	if defRef != "" {
		for sha := range strings.FieldsSeq(gitx.Out(ctx, dir, "log", "--format=%H", stamp, defRef)) {
			onDefault[sha] = true
		}
	}

	var commits []standup.Commit

	for line := range strings.SplitSeq(out, "\n") {
		f := strings.SplitN(line, "\x1f", 6)
		if len(f) < 6 || !strings.EqualFold(f[1], email) {
			continue
		}

		at, err := time.Parse(time.RFC3339, f[2])
		if err != nil {
			return nil, fmt.Errorf("git log: author date %q: %w", f[2], err)
		}

		branch := refBranch(f[4], remotes, def)
		if onDefault[f[0]] {
			branch = def
		}

		commits = append(commits, standup.Commit{
			SHA: f[0], Subject: f[5], At: at, Branch: branch, Parents: strings.Fields(f[3]),
		})
	}

	return commits, nil
}

// refBranch is a ref as the branch it stands for: refs/ and a remote's
// prefix dropped, so origin/x and x are one branch, and a remote's HEAD is
// the default branch.
func refBranch(r string, remotes []string, def string) string {
	r = strings.TrimPrefix(strings.TrimPrefix(r, "refs/heads/"), "refs/remotes/")

	for _, remote := range remotes {
		if rest, ok := strings.CutPrefix(r, remote+"/"); ok {
			r = rest

			break
		}
	}

	if r == "HEAD" {
		return def
	}

	return r
}

// recordedBases is every head branch's base as `f10 start --after` wrote
// it: the real base of a stacked PR after the host retargets it.
func recordedBases(ctx context.Context, main string) map[string]string {
	out := map[string]string{}

	for line := range strings.SplitSeq(gitx.Out(ctx, main, "config", "--get-regexp", `^branch\..*\.`+cfgAfterBranch+`$`), "\n") {
		key, base, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}

		head := strings.TrimSuffix(strings.TrimPrefix(key, "branch."), "."+cfgAfterBranch)
		out[head] = strings.TrimSpace(base)
	}

	return out
}

// renderStandup is the report for a terminal: one line per task, then the
// activity with no task and the reviews. --json is the contract; this is a
// glance at it.
func renderStandup(w io.Writer, r standup.Report) error {
	var b strings.Builder

	fmt.Fprintf(&b, "since %s", r.Since.Local().Format("2006-01-02 15:04"))

	if r.Period != "" {
		fmt.Fprintf(&b, " (%s)", strings.ReplaceAll(r.Period, "_", " "))
	}

	b.WriteString("\n")

	if len(r.Days) > 0 {
		fmt.Fprintf(&b, "days  %s\n", dayList(r.Days))
	}

	if len(r.Tasks)+len(r.Other.PRs)+len(r.Other.Commits)+len(r.Reviewed) == 0 {
		b.WriteString("\nnothing\n")
	}

	if len(r.Tasks) > 0 {
		b.WriteString("\n")
	}

	for _, t := range r.Tasks {
		fmt.Fprintf(&b, "%-10s %-12s %s%s\n", t.ID, t.State, prList(t.PRs, " -> "), commitCount(t.PRs, t.Commits))
	}

	if n := len(r.Other.PRs) + len(r.Other.Commits); n > 0 {
		fmt.Fprintf(&b, "\nother      %s%s\n", prList(r.Other.PRs, ", "), commitCount(r.Other.PRs, r.Other.Commits))
	}

	if len(r.Reviewed) > 0 {
		b.WriteString("\nreviewed\n")

		for _, p := range r.Reviewed {
			var kinds []string
			for _, a := range p.Actions {
				kinds = append(kinds, a.Kind)
			}

			fmt.Fprintf(&b, "  #%d %s (%s): %s\n", p.Number, p.Title, p.Author, strings.Join(dedupe(kinds), ", "))
		}
	}

	_, err := io.WriteString(w, b.String())

	return err
}

// prList names the PRs; a task's are in chain order, so " -> " reads true.
func prList(prs []standup.PR, sep string) string {
	parts := make([]string, 0, len(prs))
	for _, p := range prs {
		parts = append(parts, fmt.Sprintf("#%d", p.Number))
	}

	return strings.Join(parts, sep)
}

func commitCount(prs []standup.PR, loose []standup.Commit) string {
	n := len(loose)
	for _, p := range prs {
		n += len(p.Commits)
	}

	switch {
	case n == 0:
		return ""
	case len(prs) == 0:
		return fmt.Sprintf("%d commit(s)", n)
	default:
		return fmt.Sprintf("  %d commit(s)", n)
	}
}

func dedupe(s []string) []string {
	var out []string

	for _, v := range s {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}

	return out
}

// dayList names the active days with their weekday, "Fri 10-09, Mon 10-12",
// which is what tells a long weekend from a missed day.
func dayList(days []string) string {
	parts := make([]string, 0, len(days))

	for _, d := range days {
		if t, err := time.Parse(time.DateOnly, d); err == nil {
			d = t.Format("Mon 01-02")
		}

		parts = append(parts, d)
	}

	return strings.Join(parts, ", ")
}
