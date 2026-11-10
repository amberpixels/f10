package main

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
	task string // "" for bare
	text string
	from answerSource
	at   time.Time // when an inbox answer was given
}

// An answerBook holds the drive's answers. The command line and the inbox
// are two ways in: an inbox answer for a task replaces a flag's, since it
// came first.
type answerBook struct {
	bare   string
	byTask map[string]answer
}

func newAnswerBook() answerBook { return answerBook{byTask: map[string]answer{}} }

func (b *answerBook) give(a answer) {
	if a.task == "" {
		b.bare = a.text

		return
	}

	if b.byTask == nil {
		b.byTask = map[string]answer{}
	}

	b.byTask[a.task] = a
}

// keyed is the answer the drive would send task, "" and false for none; a
// heard answer is never sent.
func (b *answerBook) keyed(task string) (answer, bool) {
	a, ok := b.byTask[task]
	if !ok || a.from == heardInbox {
		return answer{}, false
	}

	return a, true
}

// heard takes task's heard answer: when it was given, false for none.
func (b *answerBook) heard(task string) (time.Time, bool) {
	a, ok := b.byTask[task]
	if !ok || a.from != heardInbox {
		return time.Time{}, false
	}

	delete(b.byTask, task)

	return a.at, true
}

// take takes the answer for task's question: its keyed one, else the bare
// one, "" for none.
func (b *answerBook) take(task string) string {
	if a, ok := b.keyed(task); ok {
		delete(b.byTask, task)

		return a.text
	}

	a := b.bare
	b.bare = ""

	return a
}

// takeKeyed takes task's keyed answer only, false for none.
func (b *answerBook) takeKeyed(task string) (string, bool) {
	a, ok := b.keyed(task)
	if ok {
		delete(b.byTask, task)
	}

	return a.text, ok
}

// pending is what a record keeps for a resume: by task id, "" for the bare
// one; a heard answer has been delivered already.
func (b *answerBook) pending() map[string]string {
	out := map[string]string{}
	if b.bare != "" {
		out[""] = b.bare
	}

	for id, a := range b.byTask {
		if a.from != heardInbox {
			out[id] = a.text
		}
	}

	return out
}

// clear drops every answer still held.
func (b *answerBook) clear() {
	b.bare = ""
	maps.DeleteFunc(b.byTask, func(string, answer) bool { return true })
}
