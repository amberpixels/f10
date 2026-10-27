package lore

import (
	"fmt"
	"io"
	"strings"
)

// RenderShow writes a change's entries for a person: a header line per
// entry, its fields indented under it, then its body.
func RenderShow(w io.Writer, change string, entries []Entry) error {
	if _, err := fmt.Fprintf(
		w,
		"%s  %d %s\n",
		change,
		len(entries),
		plural(len(entries), "entry", "entries"),
	); err != nil {
		return err
	}

	for _, e := range entries {
		if err := renderEntry(w, e); err != nil {
			return err
		}
	}

	return nil
}

// RenderWhy writes a why answer: the line and how it was linked, then per
// change its intent, its latest verdict and the entries on the line.
func RenderWhy(w io.Writer, r *WhyResult) error {
	link := "linked by the commit's trailer"
	if r.Via == ViaAnchor {
		link = "linked by an anchor at the commit; the commit carries no trailer naming a change with entries"
	}

	if _, err := fmt.Fprintf(w, "%s:%d  last changed in %s, %s\n", r.Path, r.Line, short(r.Commit), link); err != nil {
		return err
	}

	for _, rec := range r.Records {
		if _, err := fmt.Fprintf(w, "\n== %s\n", rec.Change); err != nil {
			return err
		}

		var list []Entry
		if rec.Intent != nil {
			list = append(list, *rec.Intent)
		}

		if rec.Verdict != nil {
			list = append(list, *rec.Verdict)
		}

		list = append(list, rec.Entries...)

		for _, e := range list {
			if err := renderEntry(w, e); err != nil {
				return err
			}
		}
	}

	return nil
}

// RenderLessons writes the lessons for a file, or says there are none.
func RenderLessons(w io.Writer, path string, lessons []Entry) error {
	if len(lessons) == 0 {
		_, err := fmt.Fprintf(w, "%s  no lessons\n", path)

		return err
	}

	if _, err := fmt.Fprintf(
		w,
		"%s  %d %s\n",
		path,
		len(lessons),
		plural(len(lessons), "lesson", "lessons"),
	); err != nil {
		return err
	}

	for _, e := range lessons {
		if err := renderEntry(w, e); err != nil {
			return err
		}
	}

	return nil
}

func renderEntry(w io.Writer, e Entry) error {
	var b strings.Builder

	fmt.Fprintf(&b, "\n%s  %s  %s  %s", e.Kind, e.ID, e.At.UTC().Format("2006-01-02 15:04Z"), e.Human)

	if e.Agent != nil && (e.Agent.Model != "" || e.Agent.Via != "") {
		b.WriteString("  with " + strings.Join(nonEmpty(e.Agent.Model, e.Agent.Via), " via "))
	}

	b.WriteString("\n")

	field := func(key, value string) {
		if value != "" {
			fmt.Fprintf(&b, "  %s: %s\n", key, value)
		}
	}

	field("severity", e.Severity)
	field("status", e.Status)
	field("resolution", e.Resolution)
	field("source", e.Source)
	field("outcome", e.Outcome)
	field("gate", e.Gate)
	field("commit", e.Commit)
	field("judged", e.Judged)
	field("chosen", e.Chosen)

	for _, r := range e.Rejected {
		field("rejected", r.Option+" - "+r.Reason)
	}

	for _, a := range e.Anchors {
		field("anchor", a.String())
	}

	field("supersedes", e.Supersedes)

	if e.Retired {
		field("retired", "yes")
	}

	b.WriteString("\n")

	for line := range strings.SplitSeq(strings.TrimSpace(e.Body), "\n") {
		if line == "" {
			b.WriteString("\n")

			continue
		}

		b.WriteString("    " + line + "\n")
	}

	_, err := io.WriteString(w, b.String())

	return err
}

func nonEmpty(values ...string) []string {
	var out []string

	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}

	return out
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}

	return sha
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}
