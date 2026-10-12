#!/usr/bin/env bash
# f10 · conventions - the static half of a run's context.
#
# Cats the six cross-cutting rule files every f10 run obeys. No arguments, git or probes, so the
# output is identical everywhere: that is what lets a caller load it once per context while
# bundle.sh, whose output varies by worktree and step, reloads every run.
#
# Usage:  conventions.sh
#   Arguments are ignored, not rejected: a caller passing a stale step name still gets the
#   conventions. Nothing is selectable; nearly every path needs all six.

set -uo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

convs=(context latency failure gaps report voice)

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
