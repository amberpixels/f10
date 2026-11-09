package main

import "context"

// mergeMethod is the declared method, else the one the repo allows; a
// GitHub repo allowing several gets squash with a note, since a
// history-rewriting default is not silent.
func (h host) mergeMethod(ctx context.Context, declared string) (string, string, error) {
	if declared != "" {
		return h.MethodFor(declared), "", nil
	}

	allowed, err := h.MergeMethods(ctx)
	if err != nil {
		return "", "", err
	}

	if len(allowed) == 1 {
		return allowed[0], "", nil
	}

	return "--squash", "merged with --squash: project.md declares no merge method and the repo allows several; " +
		"declare `merge method: squash` (or merge, rebase) under Hosting & PR to settle it", nil
}
