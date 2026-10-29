#!/usr/bin/env bash
# f10 · load-conventions - the static half of a run's context.
#
# Cats the six convention files every f10 run obeys, then the binary's command reference
# (`f10 help --agent`). No arguments, git or probes, so the output is identical everywhere.
# f10-inject.sh tells the model to run it, on a session's first f10 skill and again after compaction;
# a subagent's prompt says to run it. f10-load-project.sh, whose output varies by worktree and step,
# reloads every run.
#
# Usage:  f10-load-conventions.sh
#   Arguments are ignored, not rejected: a caller passing a stale step name still gets them all.

set -uo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

convs=(context latency failure gaps report voice)

echo "=== f10 conventions - static, identical every run ==="
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
echo "--- f10 commands (f10 help --agent) ---"
"$root/bin/f10" help --agent 2>/dev/null || echo "UNAVAILABLE - f10 help --agent failed; the f10 binary is older than this plugin"
echo
echo "=== end f10 conventions - in this context now; the hooks say when to run this again ==="
