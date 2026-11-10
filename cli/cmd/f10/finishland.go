package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// A landingKind is how a finish ends the task's work.
type landingKind int

const (
	landingPull     landingKind = iota // merge the PR, or confirm it merged
	landingUnpushed                    // a branch never pushed merges into the local default branch
	landingSpike                       // a spike with no PR closes without a merge
	landingLeftOpen                    // --local: the PR stays open, only the local side is cleaned up
	landingGone                        // the PR merged and the worktree is gone: a finish stopped halfway
)

// landed reports whether the kind merges, after which cleanup runs on past
// a failure instead of stopping.
func (k landingKind) landed() bool {
	return k == landingPull || k == landingUnpushed || k == landingGone
}

// A landing is what land leaves for cleanup and the report.
type landing struct {
	rows  []render.Fact
	notes []string
	ended string             // what already happened, for an error after it
	end   *archive.SeriesEnd // what closes the review series; nil moves the rounds unclosed
	hand  []string           // the handoff printed under the report, set only on a merge
	kept  string             // the kept/ branch a spike's experiment goes to, "" to discard it
}

func land(ctx context.Context, w io.Writer, in finishInput, d finishDecision, errs *cleanupErrs) (landing, error) {
	switch d.kind {
	case landingSpike:
		return landSpike(ctx, w, in, d)
	case landingLeftOpen:
		p := d.pull

		return landing{
			ended: p.URL + " left open",
			rows:  []render.Fact{render.TaskFact(ctx, w, in.main, in.task.ID), {Label: "pr", Value: p.URL, URL: true}},
			notes: []string{fmt.Sprintf("%s is left %s: nothing merged, pulled or deleted on origin",
				p.URL, p.Status)},
		}, nil
	default:
		return landMerge(ctx, w, in, d, errs)
	}
}

// landSpike releases what waited on the spike; its experiment goes later,
// with the worktree.
func landSpike(ctx context.Context, w io.Writer, in finishInput, d finishDecision) (landing, error) {
	l := landing{ended: "the spike " + in.task.ID + " closed"}

	if in.flags.keep {
		l.kept = keptName(ctx, in.main, d.name, in.task.Members())
	}

	released, err := releaseDependents(ctx, in.main, d.name, d.def, false)
	if err != nil {
		return l, err
	}

	l.rows = []render.Fact{render.TaskFact(ctx, w, in.main, in.task.Label())}
	if l.kept != "" {
		l.rows = append(l.rows, render.Fact{Label: "branch", Value: l.kept})
	}

	l.notes = append(spikeNotes(ctx, in, d.name, d.def, l.kept, d.dirty), released...)

	return l, nil
}

// landMerge lands a PR or a branch never pushed. Main stays on the task's
// branch until cleanup, so a failure here leaves a rerun the same shape to
// find; once the merge lands, errs collects instead of stopping.
func landMerge(ctx context.Context, w io.Writer, in finishInput, d finishDecision, errs *cleanupErrs) (landing, error) {
	name, def, p := d.name, d.def, d.pull
	unpushed := d.kind == landingUnpushed

	var (
		riderNotes, notes []string
		sha               string
		err               error
	)

	if len(in.task.Riders()) > 0 && !unpushed {
		if riderNotes, err = settleRiders(ctx, in, name, p.Status == forge.Merged); err != nil {
			return landing{}, err
		}
	}

	l := landing{
		ended: p.URL + " merged",
		// read before the merge, while the branch tip it was written at still exists
		hand: handoffLines(archive.TaskHandoff(in.storage, in.task.ID), in.task.ID, branchTip(ctx, in.main, name, p)),
	}

	recordLanded := sizeRecorder(ctx, in, name, def, unpushed)

	if unpushed {
		l.ended = name + " merged into " + def

		in.step("merging " + name + " into " + def)

		if sha, notes, err = landUnpushed(ctx, in, name, def); err != nil {
			return landing{}, err
		}
	} else {
		in.step("merging " + pullNumber(p.URL))

		if p, notes, err = landPull(ctx, in, name, def, p); err != nil {
			return landing{}, err
		}

		notes = append(notes, riderNotes...)

		in.step("pulling " + def)

		note, deferred := pullMain(ctx, in.root, in.main, def, p.Landed)
		if d.place != inMainCheckout || deferred {
			notes = appendNote(notes, note)
		}

		if deferred && in.deferred != nil {
			in.deferred(note)
		}

		sha = p.Landed
	}

	l.end = &archive.SeriesEnd{Pr: "none", Head: sha}
	if !unpushed {
		l.end.Pr = pullNumber(p.URL)
	}

	notes = appendNote(notes, d.loreNote)

	recordLanded()

	released, err := releaseDependents(ctx, in.main, name, def, true)
	_ = errs.check("releasing dependents", err)

	notes = append(notes, released...)
	l.notes = notes

	l.rows = []render.Fact{render.TaskFact(ctx, w, in.main, in.task.Label())}
	if !unpushed {
		l.rows = append(l.rows, render.Fact{Label: "pr", Value: p.URL, URL: true})
	}

	l.rows = appendClosed(append(l.rows, render.Fact{Label: "merge", Value: sha}), in, unpushed)

	return l, nil
}

// landPull merges an open request or confirms a merged one. Requests based
// on the branch move to the merged one's base before the branch goes, since
// the delete would close them; the notes say which moved.
func landPull(ctx context.Context, in finishInput, name, def string, p forge.PR) (forge.PR, []string, error) {
	var notes []string

	switch p.Status {
	case forge.Open:
		if base := p.Base; base != "" && base != def {
			return forge.PR{}, nil, fmt.Errorf(
				"%s is stacked on %s, not %s: finish the task that branch belongs to first, "+
					"and its finish retargets this PR to %s",
				p.URL,
				base,
				def,
				def,
			)
		}

		method, note, err := in.host.MergeMethod(ctx, in.method)
		if err != nil {
			return forge.PR{}, nil, err
		}

		notes = appendNote(notes, note)

		// settleRiders may have just rewritten the description's closing
		// keywords, and the body under the commit must be the host's current one
		description := p.Body
		if in.traces {
			var note string

			if description, note, err = lastState(ctx, in, name, description); err != nil {
				return forge.PR{}, nil, err
			}

			notes = appendNote(notes, note)
		}

		if p, err = in.host.MergePR(ctx, p, name, method, mergeBody(in, description, method)); err != nil {
			return forge.PR{}, nil, err
		}
	case forge.Merged:
		notes = append(notes, p.URL+" was already merged")
	default:
		return forge.PR{}, nil, fmt.Errorf("%s is %s without a merge: finish only finishes merged or mergeable work; "+
			"--local cleans up here and leaves it %s", p.URL, p.Status, p.Status)
	}

	if p.Status != forge.Merged || p.Landed == "" {
		return forge.PR{}, nil, fmt.Errorf("%s reports %s as %s with no merge commit after the merge",
			in.host.Name, p.URL, p.Status)
	}

	exists, err := gitx.RemoteBranchExists(ctx, in.main, name)
	if err != nil {
		return forge.PR{}, nil, err
	}

	if exists {
		moved, err := retargetStacked(ctx, in.host, p, name, cmp.Or(p.Base, def))
		if err != nil {
			return forge.PR{}, nil, err
		}

		notes = append(notes, moved...)

		if err := in.host.DeleteBranch(ctx, name); err != nil {
			return forge.PR{}, nil, fmt.Errorf("%s merged, but deleting its remote branch failed: %w", p.URL, err)
		}
	}

	// the local copy of origin's branch outlives the API's delete, and a
	// task with any branch left reads as not finished to drive and start
	if err := gitx.DeleteRemoteRef(ctx, in.main, name); err != nil {
		notes = append(notes, fmt.Sprintf("origin/%s is gone but its local copy is not: %v", name, err))
	}

	return p, notes, nil
}

// retargetStacked moves every open request based on branch to base. A
// failure stops before the delete, so the request stays open for a rerun;
// its error names the requests already moved, since no report follows it.
func retargetStacked(ctx context.Context, h hostops.Host, merged forge.PR, branch, base string) ([]string, error) {
	stacked, err := h.StackedPRs(ctx, branch)
	if err != nil {
		return nil, fmt.Errorf("%s merged, but listing the requests based on %s failed, "+
			"so origin/%s is left in place: %w", merged.URL, branch, branch, err)
	}

	var notes, moved []string

	for _, s := range stacked {
		if err := h.Retarget(ctx, s.Number, base); err != nil {
			if len(moved) > 0 {
				err = fmt.Errorf("%w (already retargeted: %s)", err, strings.Join(moved, ", "))
			}

			return nil, fmt.Errorf("%s merged, but retargeting %s to %s failed, "+
				"so origin/%s is left in place to keep it open: %w", merged.URL, s.URL, base, branch, err)
		}

		moved = append(moved, s.URL)
		notes = append(notes, fmt.Sprintf("retargeted %s to %s", s.URL, base))
	}

	return notes, nil
}

// lastState sets the PR body's state block to finish, returning the body the
// host holds. A failed read or write is a note, except for a bundle, whose
// fallback body would close a rider settleRiders just kept open.
func lastState(ctx context.Context, in finishInput, branch, fallback string) (string, string, error) {
	body, err := in.host.PRBody(ctx, branch)

	switch {
	case err != nil && len(in.task.Riders()) > 0:
		return "", "", err
	case err != nil:
		body = fallback
	default:
		body, err = putLastState(ctx, in, branch, body)
	}

	return body, stateNote(err), nil
}

func putLastState(ctx context.Context, in finishInput, branch, body string) (string, error) {
	out, err := trace.Rewrite(body, trace.Update{
		Step:   "finish",
		Task:   in.task.ID,
		Riders: in.task.Riders(),
		After:  branchAfter(ctx, in.main, branch),
	})
	if err != nil || out == body {
		return body, err
	}

	if err := in.host.EditPull(ctx, branch, "", out); err != nil {
		return body, err
	}

	return out, nil
}

func stateNote(err error) string {
	if err == nil {
		return ""
	}

	return "the PR body's state block was not rewritten: " + err.Error()
}

// mergeBody is the host commit's body: the description minus its state
// block, then a Task: line per member, so a squash still names what it
// shipped. A rebase, or glab's plain merge (""), keeps the branch's trailers.
func mergeBody(in finishInput, description, method string) string {
	if !in.traces || (method != "--squash" && method != "--merge") {
		return ""
	}

	trailers, err := trace.Render(trace.Trailers{Tasks: in.task.Members()})
	if err != nil {
		return ""
	}

	trailers += stack.ChangeTrailer(in.task.ID, in.loreChange())

	if body := strings.TrimSpace(trace.StripState(description)); body != "" {
		return body + "\n\n" + trailers
	}

	return trailers
}
