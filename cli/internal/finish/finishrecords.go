package finish

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// finishRecords marks the task finished in every stopped drive record that
// lists it, so drive list needs no rerun. A failure is a line: the merge
// already landed.
func finishRecords(w io.Writer, in FinishInput, branch string) {
	if in.Root == "" {
		return
	}

	recs, err := drivestore.List(in.Root)
	if err != nil {
		fmt.Fprintf(w, "  marking drive records: %v\n", err)

		return
	}

	for _, rec := range recs {
		match := AttemptRows(in, branch, rec.Tasks)
		if !slices.ContainsFunc(rec.Tasks, func(t drivestore.Task) bool { return !drivectl.RowDone(t) && match(t) }) {
			continue
		}

		if err := drivectl.MarkFinished(in.Root, rec.Number, match); err != nil {
			fmt.Fprintf(w, "  marking drive %d: %v\n", rec.Number, err)
		}
	}
}

// AttemptRows matches the rows of the attempt finish acted on, since one
// attempt finished leaves the task's others alive. A bare finish, or one
// that found the lone <id>-* branch, types no suffix: the branch's ending
// names the attempt, the same way the branch index finds it.
func AttemptRows(in FinishInput, branch string, rows []drivestore.Task) func(drivestore.Task) bool {
	ofTask := func(row string) (string, bool) {
		id, suffix := ref.SplitSuffix(row)

		return suffix, strings.EqualFold(id, in.Task.Label()) ||
			slices.ContainsFunc(in.Task.Members(), func(m string) bool { return strings.EqualFold(id, m) })
	}

	acted := in.Suffix
	if acted == "" {
		for _, t := range rows {
			if suffix, ok := ofTask(t.ID); ok && suffix != "" &&
				strings.HasSuffix(strings.ToLower(branch), "-"+strings.ToLower(suffix)) {
				acted = suffix
			}
		}
	}

	return func(t drivestore.Task) bool {
		suffix, ok := ofTask(t.ID)

		return ok && strings.EqualFold(suffix, acted)
	}
}

// checkLore runs only right before a merge lands (a merged request, --local
// and a spike's close merge nothing). An unreadable store is a reason; under
// block it is the error, before anything changed.
func checkLore(in FinishInput, merging bool) (string, error) {
	if in.LoreStore == nil || !merging {
		return "", nil
	}

	in.step("checking lore")

	var reasons string

	switch res, err := in.LoreStore.Check(in.loreChange()); {
	case err != nil:
		reasons = err.Error()
	case res.Pass():
		return "", nil
	default:
		reasons = strings.Join(res.Reasons, "; ")
	}

	if in.Gate == "block" {
		return "", fmt.Errorf(
			"lore check %s: %s (lore_gate: block): add the entries, or declare lore_gate: warn to merge anyway",
			in.loreChange(),
			reasons,
		)
	}

	return fmt.Sprintf("lore check %s: %s (lore_gate: warn): merged anyway", in.loreChange(), reasons), nil
}

// loreChange is the change the merge closes; a FinishInput built without
// finish (a test, MergeBody alone) reads the task id.
func (in FinishInput) loreChange() string {
	return cmp.Or(in.Change, in.Task.ID)
}
