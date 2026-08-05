#!/usr/bin/env bash
# f10 · conventions - the static half of a run's context.
#
# Cats the five cross-cutting rule files every f10 run obeys. Deliberately inert: no arguments, no
# git, no filesystem decisions, no probes. Its output is identical on every machine, in every repo,
# for every step - which is precisely why it is not part of resolve.sh, whose output is none of
# those things.
#
# That split is what makes the load-once rule statable. Conventions cannot vary, so a second copy in
# one context window is waste; project resolution can vary mid-conversation (a different worktree, an
# edited project.md), so resolve.sh is never skipped. Fused, "load the conventions once" could not be
# said without also skipping facts the run still needed.
#
# Usage:  conventions.sh
#   Arguments are ignored rather than rejected - a caller passing a stale step name should get the
#   conventions, not an error. There is nothing to select: every path but capture-only already
#   needed all four, so selecting cost a rule to follow and saved one file.

set -uo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

convs=(context latency failure gaps report)

echo "=== f10 conventions - static, identical every run. Load once per context. ==="
echo "carries: ${convs[*]}"
echo
for c in "${convs[@]}"; do
  echo "--- convention: $c ---"
  if [ -f "$root/conventions/$c.md" ]; then
    cat "$root/conventions/$c.md"
  else
    echo "MISSING - $root/conventions/$c.md not found"
  fi
  echo
done
echo "=== end conventions - already in this context, so do not load them again ==="
