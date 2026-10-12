#!/usr/bin/env bash
# f10 · conventions - the static half of a run's context.
#
# Cats the six cross-cutting rule files every f10 run obeys. No arguments, git or probes, so the
# output is identical everywhere. The plugin's hooks inject it into a session's context (f10-state.sh
# inject) when a skill starts and again after compaction; a subagent, which starts without hook
# context, runs it by hand. bundle.sh, whose output varies by worktree and step, reloads every run.
#
# Usage:  conventions.sh            everything, in order
#         conventions.sh <n>        chunk n of the same text, for one hook entry
#         conventions.sh --count    how many chunks there are
#
# Chunks exist because Claude Code caps what one hook may add to the context at 10,000 characters;
# the whole set is over twice that and context.md alone is over it. The chunks concatenate to the
# no-argument output byte for byte, so a reader sees one text either way. A chunk is a list of
# files, or of one file's two halves, cut at the heading in context_split. Keep each under the
# cap with room to grow: the layout tests pin the size and the hook count.

set -uo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

convs=(context latency failure gaps report voice)

chunks=(
  "context:head latency failure"
  "context:tail"
  "gaps report voice"
)
# shellcheck disable=SC2016 # the backticks are markdown, matched literally
context_split='## `project.md` contract'

cat_part() { # cat_part <name> | <name>:head | <name>:tail
  local name="${1%%:*}" part="${1#*:}" f
  [ "$part" = "$1" ] && part=""
  f="$root/conventions/$name.md"
  [ -n "$part" ] || echo "--- convention: $name ---"
  [ "$part" = "head" ] && echo "--- convention: $name ---"
  if [ ! -f "$f" ]; then
    echo "MISSING - $f not found"
    echo
    return 0
  fi
  case "$part" in
    "") cat "$f" ;;
    head) awk -v s="$context_split" '$0 == s { exit } { print }' "$f" ;;
    tail) awk -v s="$context_split" '$0 == s { p = 1 } p { print }' "$f" ;;
  esac
  # the blank line after a file closes it; a head leaves the file open for its tail
  [ "$part" = "head" ] || echo
}

print_chunk() { # print_chunk <n>
  local n="$1" part
  [ "$n" -ge 1 ] && [ "$n" -le "${#chunks[@]}" ] || return 1
  if [ "$n" = 1 ]; then
    echo "=== f10 conventions - static, identical every run. Injected by the plugin's hooks; a subagent runs this by hand. ==="
    echo "carries: ${convs[*]}"
    echo
  fi
  for part in ${chunks[$((n - 1))]}; do
    cat_part "$part"
  done
  if [ "$n" = "${#chunks[@]}" ]; then
    echo "=== end conventions - already in this context, so do not load them again ==="
  fi
  return 0
}

print_all() {
  local n=1
  while [ "$n" -le "${#chunks[@]}" ]; do
    print_chunk "$n"
    n=$((n + 1))
  done
}

case "${1:-}" in
  --count) echo "${#chunks[@]}" ;;
  # a chunk number; anything else is ignored, not rejected: a caller passing a stale step name
  # still gets the whole text
  *[!0-9]* | '') print_all ;;
  *) print_chunk "$1" || exit 1 ;;
esac
