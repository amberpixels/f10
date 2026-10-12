#!/usr/bin/env bash
# f10 · rules - the static half of a run's context.
#
# Cats the six convention files every f10 run obeys. No arguments, git or probes, so the output is
# identical everywhere. f10-inject.sh tells the model to run it, on a session's first f10 skill and
# again after compaction; a subagent's prompt says to run it. f10-load.sh, whose output varies by
# worktree and step, reloads every run.
#
# Usage:  f10-rules.sh
#   Arguments are ignored, not rejected: a caller passing a stale step name still gets the rules.

set -uo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

convs=(context latency failure gaps report voice)

echo "=== f10 rules (conventions/) - static, identical every run ==="
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
echo "=== end f10 rules - in this context now; the hooks say when to run this again ==="
