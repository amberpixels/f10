package drive

import (
	"maps"
	"time"
)

// answerSource is where one of the drive's answers came from.
type answerSource int

const (
	// fromFlag is --answer, or one a stopped drive's record kept
	fromFlag answerSource = iota
	// fromInbox is an inbox answer the agent never heard: the drive sends it
	fromInbox
	// heardInbox is an inbox answer given where the agent heard it: the
	// drive follows that turn instead of sending anything
	heardInbox
)

// An answer is one the drive holds until a task stopped on a question
// takes it: keyed to a task, or bare for the first such task.
type answer struct {
	Task string // "" for bare
	Text string
	From answerSource
	At   time.Time // when an inbox answer was given
}

// An answerBook holds the drive's answers. The command line and the inbox
// are two ways in: an inbox answer for a task replaces a flag's, since it
// came first.
type answerBook struct {
	Bare   string
	byTask map[string]answer
}

func NewAnswerBook() answerBook { return answerBook{byTask: map[string]answer{}} }

func (b *answerBook) Give(a answer) {
	if a.Task == "" {
		b.Bare = a.Text

		return
	}

	if b.byTask == nil {
		b.byTask = map[string]answer{}
	}

	b.byTask[a.Task] = a
}

// Keyed is the answer the drive would send task, "" and false for none; a
// heard answer is never sent.
func (b *answerBook) Keyed(task string) (answer, bool) {
	a, ok := b.byTask[task]
	if !ok || a.From == heardInbox {
		return answer{}, false
	}

	return a, true
}

// Heard takes task's heard answer: when it was given, false for none.
func (b *answerBook) Heard(task string) (time.Time, bool) {
	a, ok := b.byTask[task]
	if !ok || a.From != heardInbox {
		return time.Time{}, false
	}

	delete(b.byTask, task)

	return a.At, true
}

// Take takes the answer for task's question: its keyed one, else the bare
// one, "" for none.
func (b *answerBook) Take(task string) string {
	if a, ok := b.Keyed(task); ok {
		delete(b.byTask, task)

		return a.Text
	}

	a := b.Bare
	b.Bare = ""

	return a
}

// Pending is what a record keeps for a resume: by task id, "" for the bare
// one; a heard answer has been delivered already.
func (b *answerBook) Pending() map[string]string {
	out := map[string]string{}
	if b.Bare != "" {
		out[""] = b.Bare
	}

	for id, a := range b.byTask {
		if a.From != heardInbox {
			out[id] = a.Text
		}
	}

	return out
}

// Clear drops every answer still held.
func (b *answerBook) Clear() {
	b.Bare = ""
	maps.DeleteFunc(b.byTask, func(string, answer) bool { return true })
}

// takeKeyed takes task's keyed answer only, false for none.
func (b *answerBook) takeKeyed(task string) (string, bool) {
	a, ok := b.Keyed(task)
	if ok {
		delete(b.byTask, task)
	}

	return a.Text, ok
}
