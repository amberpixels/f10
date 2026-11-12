// Package drive runs a list of tasks through their flow from one process: each
// in its own worktree and agent, dependents once their base has code, halting
// when an agent asks and nothing else can move; and the watcher's passes over
// tasks no drive owns.
package drive

import (
	"context"
	"strconv"
	"strings"
	"time"
)

const (

	// promptSettle is how old a prompt mark must be before an idle agent
	// beside it reads as a turn that ended, not one Herdr has yet to show
	promptSettle = 5 * time.Second
	// StartWindow is how long a prompted agent has to show it took the
	// prompt: Herdr reporting it working, or its run reporting.
	StartWindow = 2 * time.Minute
	// StallGrace absorbs the Stop hook, which promotes a finished phase
	// asynchronously after the agent already reads idle. A long verify left
	// in the background does not count against it (backgroundPending).
	StallGrace = time.Minute
	// maxSpan caps a range, since every id in it costs a tracker call.
	maxSpan = 50
)

func ResumeCommand(argv []string) string {
	parts := []string{"f10", "drive"}

	for i := 0; i < len(argv); i++ {
		tok := argv[i]

		switch {
		case tok == "--answer":
			i++

			continue
		case strings.HasPrefix(tok, "--answer="):
			continue
		case strings.ContainsAny(tok, " \t\"'"):
			tok = strconv.Quote(tok)
		}

		parts = append(parts, tok)
	}

	return strings.Join(parts, " ")
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
