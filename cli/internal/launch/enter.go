package launch

import (
	"context"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// A typed prompt's Enter can arrive while the input box is still busy with
// the text, and be lost; these bound how long a typed prompt is watched for
// that before Enter is pressed again, once.
var (
	EnterRetry = 10 * time.Second
	EnterPoll  = 500 * time.Millisecond
	// promptMarks is where the UserPromptSubmit hook leaves its marks
	promptMarks = state.Dir
)

// promptKey is how much of a prompt identifies it in the input box: enough
// to tell it from the user's own typing, short enough to sit on one line.
const promptKey = 30

// HoldsPrompt reports whether the input box on screen still holds text,
// unsubmitted. A screen with no box found holds nothing, so no Enter is
// pressed where f10 cannot see what it would submit.
func HoldsPrompt(screen, text string) bool {
	box, ok := inputBox(screen)
	if !ok {
		return false
	}

	// only a multi-line prompt is pasted as a block; a placeholder after a
	// one-line prompt is the user's own paste
	if strings.Contains(text, "\n") && strings.Contains(box, "[Pasted text #") {
		return true
	}

	key := []rune(strings.Join(strings.Fields(text), " "))
	key = key[:min(len(key), promptKey)]

	return len(key) > 0 && strings.Contains(strings.Join(strings.Fields(box), " "), string(key))
}

// inputBox is the text between the screen's last two horizontal rules,
// which is where Claude draws its input box.
func inputBox(screen string) (string, bool) {
	lines := strings.Split(screen, "\n")

	var rules []int

	for i, l := range lines {
		if isRule(l) {
			rules = append(rules, i)
		}
	}

	if len(rules) < 2 {
		return "", false
	}

	top, bottom := rules[len(rules)-2], rules[len(rules)-1]

	return strings.Join(lines[top+1:bottom], "\n"), true
}

func isRule(line string) bool {
	l := strings.TrimSpace(line)

	return len([]rune(l)) >= 10 && strings.Trim(l, "─") == ""
}

// RetryEnter presses Enter once when the agent's input box still holds
// text. It says what it did, or why it could not look, "" when the prompt
// had gone through.
func RetryEnter(ctx context.Context, b agentctl.Backend, name, text string) string {
	screen, err := b.Screen(ctx, name)
	if err != nil {
		return "could not read the agent's screen to check the prompt was submitted: " + err.Error()
	}

	if !HoldsPrompt(screen, text) {
		return ""
	}

	if err := b.SendEnter(ctx, name); err != nil {
		return "the prompt is still in the agent's input box, and pressing Enter failed: " + err.Error()
	}

	return "Enter resent: the prompt was still in the agent's input box"
}

// PromptChecked types text into the task's agent, then waits up to
// EnterRetry for the agent's prompt mark and presses Enter once if the text
// is still in its input box. The note says what the check did; it is never
// an error, since the prompt itself went out.
func PromptChecked(ctx context.Context, path, id, suffix, text string) (string, error) {
	sent := time.Now()

	name, err := PromptAgentAs(ctx, path, id, suffix, text)
	if err != nil {
		return "", err
	}

	return WatchEnter(ctx, worktree.Agents(path), path, name, text, sent), nil
}

// WatchEnter is PromptChecked's wait, for a prompt typed at sent into the
// agent in the checkout at root. Only the prompt mark ends it early: an
// empty input box may be one that has not drawn the text yet.
func WatchEnter(ctx context.Context, b agentctl.Backend, root, name, text string, sent time.Time) string {
	since := sent.Truncate(time.Second) // marks keep whole seconds

	for deadline := sent.Add(EnterRetry); time.Now().Before(deadline); {
		if p, ok := state.LastPrompt(promptMarks(), root); ok && !p.At.Before(since) {
			return ""
		}

		timer := time.NewTimer(min(EnterPoll, time.Until(deadline)))

		select {
		case <-ctx.Done():
			timer.Stop()

			return ""
		case <-timer.C:
		}
	}

	return RetryEnter(ctx, b, name, text)
}
