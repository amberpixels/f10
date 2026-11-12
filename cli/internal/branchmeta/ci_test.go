package branchmeta

import "testing"

func TestTriageLimits(t *testing.T) {
	tr := ParseTriage("aaa:fix bbb:rerun junk ccc:other")
	if len(tr.Tried) != 2 || tr.Format() != "aaa:fix bbb:rerun" {
		t.Fatalf("ParseTriage = %+v; want the two well-formed entries", tr)
	}

	// a fix counts for the branch: the new head it pushed may not be fixed again
	if tr.Allows("new", TriedFix) {
		t.Error("a second fix round allowed on a new head")
	}

	if tr.Allows("bbb", TriedRerun) || !tr.Allows("new", TriedRerun) {
		t.Error("a rerun must count for its own sha alone")
	}

	if (Triage{}).Fixed() || !(Triage{}).Allows("x", TriedFix) {
		t.Error("an empty record must allow a fix")
	}
}
