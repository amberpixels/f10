package drives

import "time"

// A Catchup is what a branch looked like when a catchup went out, kept by
// the drive on its task and by the watcher on its stored report, so both
// lease the push after it and judge a repeat the same way.
type Catchup struct {
	Target string `yaml:"target"` // the sha the branch was caught up to
	Head   string `yaml:"head"`   // the branch's tip when the catchup went out
	// Origin is origin's copy of the branch then, what the push after the
	// catchup leases against; "" when origin lacked the branch
	Origin string    `yaml:"origin"`
	Base   string    `yaml:"base"` // the target's name in prose
	At     time.Time `yaml:"at"`
	// Failed is a catchup that did not end clean; the watcher sends another
	// only once the fingerprint moves
	Failed bool `yaml:"failed,omitempty"`
	// Fingerprint is the progress state the catchup started from, or for a
	// failed one the state it failed at (see Fingerprint)
	Fingerprint string `yaml:"fingerprint,omitempty"`
	// TargetRef is the target's remote-tracking ref, "" for a target recorded
	// nowhere; a push waits until the target is on it
	TargetRef string `yaml:"target_ref,omitempty"`
	// Spent is set once a push or a later skill made Origin stale, so it
	// leases nothing; a record without it still holds its lease
	Spent bool `yaml:"spent,omitempty"`
	// Integrated is set once this catchup rewrote the branch itself, after
	// Origin was read, so the lease stays sound on a resend
	Integrated bool `yaml:"integrated,omitempty"`
}

// Fingerprint is a catchup's progress state: the target it brings in, the
// conflicts it meets (path and blobs) and the verify command red after it.
// The head is left out, since a merge moves it without progress; an attempt
// with the fingerprint of the one before made none.
func Fingerprint(target, conflicts, red string) string {
	return target + "|" + conflicts + "|" + red
}
