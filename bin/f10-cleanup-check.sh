#!/usr/bin/env bash
# f10 · cleanup-check - prove a cleanup pass stayed in scope (steps/cleanup.md).
#
# Both modes compare the current files against a snapshot taken before the pass, never HEAD: in a
# ship pipeline the tree already holds implement's uncommitted work. Paths are looked up as
# <snapshot-dir>/<file>, so run from the directory the snapshot was taken in, with the same spelling.
#
# comments  Every non-comment line is unchanged, after dropping full-line comments and blank lines,
#           stripping trailing comments, and mapping em and en dashes to a hyphen. Prints
#           `CODE CHANGED: <file>`, or `UNCHECKED: <file>` for a comment syntax it does not know.
# literals  Every literal in the snapshot (backtick span, fenced code line, path, --flag, number)
#           appears somewhere in the current files. The union is searched, not the matching file,
#           since a rule may have moved to its canonical home. Tokens listed in
#           <snapshot-dir>/.dropped, one per line, are dropped on purpose and only echoed.
#
# Usage:  f10-cleanup-check.sh comments|literals <snapshot-dir> <file>...
#   Exit 1 when anything is flagged or missing, 2 on bad usage.

# shellcheck disable=SC2016 # literal backticks and $ in the patterns
set -uo pipefail

usage() {
  echo "usage: f10-cleanup-check.sh comments|literals <snapshot-dir> <file>..." >&2
  exit 2
}

[ $# -ge 3 ] || usage
mode=$1 snap=$2
shift 2

style_of() { # the comment syntax of a file, by name
  case "$(basename "$1")" in
    Makefile | makefile | justfile | Justfile | Dockerfile | Gemfile | Rakefile) echo hash ;;
    *.sh | *.bash | *.zsh | *.rb | *.rake | *.py | *.yaml | *.yml | *.toml | *.pl | *.r | *.mk) echo hash ;;
    *.go | *.js | *.jsx | *.mjs | *.cjs | *.ts | *.tsx | *.rs | *.java | *.kt | *.swift | *.c | *.h | *.cc | *.cpp | *.hpp | *.cs | *.scala | *.php) echo slash ;;
    *.erb) echo erb ;;
    *.sql | *.lua | *.hs) echo dash ;;
  esac
}

code_lines() { # code_lines <style> <file>: the lines a comment-only pass must leave alone
  local full trail em=$'\xe2\x80\x94' en=$'\xe2\x80\x93'
  case "$1" in
    hash) full='^[[:space:]]*#' trail='[[:space:]]+#.*$' ;;
    slash) full='^[[:space:]]*(//|/\*|\*)' trail='[[:space:]]+(//|/\*).*$' ;;
    erb) full='^[[:space:]]*<%#' trail='[[:space:]]+#.*$' ;;
    dash) full='^[[:space:]]*--' trail='[[:space:]]+--.*$' ;;
  esac
  grep -vE "$full" "$2" | sed -E "s@$trail@@; s/$em/-/g; s/$en/-/g; s/[[:space:]]+$//" | grep -v '^$'
}

check_comments() {
  local flagged=0 f style
  for f in "$@"; do
    [ -f "$snap/$f" ] || continue
    style=$(style_of "$f")
    if [ -z "$style" ]; then
      echo "UNCHECKED: $f"
      flagged=1
      continue
    fi
    if [ ! -f "$f" ] || ! diff <(code_lines "$style" "$snap/$f") <(code_lines "$style" "$f") >/dev/null; then
      echo "CODE CHANGED: $f"
      flagged=1
    fi
  done
  [ "$flagged" -eq 0 ] && echo "comments: code unchanged"
  return "$flagged"
}

literals() { # every literal token in one markdown file
  # Fenced code lines, trimmed.
  awk '/^[[:space:]]*```/ { fence = !fence; next } fence { gsub(/^[[:space:]]+|[[:space:]]+$/, ""); if ($0 != "") print }' "$1"
  # Prose outside fences: backtick spans, then paths, flags and numbers.
  local prose
  prose=$(mktemp)
  awk '/^[[:space:]]*```/ { fence = !fence; next } !fence' "$1" | sed -E 's/^[[:space:]]*[0-9]+\.[[:space:]]//' >"$prose"
  grep -oE '`[^`]+`' "$prose" | sed -E 's/^`|`$//g'
  grep -oE '[A-Za-z0-9_.~<>${}-]*/[A-Za-z0-9_.~<>${}/*-]+' "$prose" | sed -E 's/[.]+$//' | grep -vE '^/+$|^[0-9/]+$|^[A-Za-z]+(/[A-Za-z]+)+$' # word/word is prose
  grep -oE -- '--[a-z][a-z0-9-]+' "$prose"
  # Single digits match almost anywhere, so only longer numbers, decimals and percentages count.
  grep -oE '(^|[^A-Za-z0-9_-])([0-9]{2,}|[0-9]+[.:][0-9]+|[0-9]+%)([.:][0-9]+)*%?' "$prose" | sed -E 's/^[^0-9]//'
  rm -f "$prose"
}

check_literals() {
  local union dropped="$snap/.dropped" missing=0 f lost
  union=$(mktemp)
  # shellcheck disable=SC2064 # expand now: union is local
  trap "rm -f '$union'" EXIT
  cat "$@" >"$union" 2>/dev/null
  for f in "$@"; do
    [ -f "$snap/$f" ] || continue
    lost=$(literals "$snap/$f" | sort -u | while IFS= read -r tok; do
      [ -n "$tok" ] || continue
      if [ -f "$dropped" ] && grep -qxF -- "$tok" "$dropped"; then continue; fi
      grep -qF -- "$tok" "$union" || printf '  %s\n' "$tok"
    done)
    if [ -n "$lost" ]; then
      echo "MISSING in $f:"
      echo "$lost"
      missing=1
    fi
  done
  if [ -s "$dropped" ]; then
    echo "dropped on purpose:"
    sed 's/^/  /' "$dropped"
  fi
  [ "$missing" -eq 0 ] && echo "literals: all kept"
  return "$missing"
}

case "$mode" in
  comments) check_comments "$@" ;;
  literals) check_literals "$@" ;;
  *) usage ;;
esac
