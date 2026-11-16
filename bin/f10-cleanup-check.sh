#!/usr/bin/env bash
# f10 · cleanup-check - prove a cleanup pass stayed in scope (steps/cleanup.md).
#
# Both modes compare the current files against a snapshot taken before the pass, never HEAD: in a
# ship pipeline the tree already holds implement's uncommitted work. Paths are looked up as
# <snapshot-dir>/<file>, so run from the directory the snapshot was taken in, with the same spelling.
#
# comments  Every line's code is unchanged once comments and blank lines go, em and en dashes
#           counting as a hyphen. Prints `CODE CHANGED: <file>`, `DASH IN CODE: <file>:<line>` for
#           each dash swapped outside a comment (allowed, but reported), or `UNCHECKED: <file>` for
#           a comment syntax it does not know.
# literals  Every literal in the snapshot (backtick span, fenced code line, path, --flag, number)
#           appears somewhere in the current files. The union is searched, not the matching file,
#           since a rule may have moved to its canonical home. Tokens listed in
#           <snapshot-dir>/.dropped, one per line, are dropped on purpose and only echoed.
# prose     No snapshot. Flags each dense sentence in markdown prose or code comments, on the
#           lines changed against <ref> (working tree included) or on every line with --all.
#           Dense means a semicolon, more than 3 joints or more than 30 words. Prints
#           `<file>:<line>: <reason>` per flag.
#
# Usage:  f10-cleanup-check.sh comments|literals <snapshot-dir> <file>...
#         f10-cleanup-check.sh prose <ref | --all> <file>...
#   Exit 1 when anything is flagged or missing, 2 on bad usage.

# shellcheck disable=SC2016 # literal backticks and $ in the patterns
set -uo pipefail

usage() {
  echo "usage: f10-cleanup-check.sh comments|literals <snapshot-dir> <file>..." >&2
  echo "       f10-cleanup-check.sh prose <ref | --all> <file>..." >&2
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

code_lines() { # code_lines <style> <file>: `<line>\t<code>` for every line with code left once comments go
  local lc="" bopen="" bclose="" quotes=""
  case "$1" in
    hash) lc='#' quotes="\"'\`" ;;
    slash) lc='//' bopen='/*' bclose='*/' quotes="\"'\`" ;;
    dash) lc='--' bopen='/*' bclose='*/' quotes="\"'" ;;
    erb) bopen='<%#' bclose='%>' ;;
  esac
  # A lexer, not a regex: a comment marker inside a string is code, and a block comment's state
  # carries across lines. An unclosed quote keeps the rest as code, so a mis-lex flags, never hides.
  awk -v lc="$lc" -v bopen="$bopen" -v bclose="$bclose" -v quotes="$quotes" -v hash="$([ "$1" = hash ] && echo 1)" '
    {
      line = $0; n = length(line); out = ""; i = 1
      while (i <= n) {
        c = substr(line, i, 1)
        if (state == "block") {
          if (substr(line, i, length(bclose)) == bclose) { state = ""; i += length(bclose) } else i++
          continue
        }
        if (state == "str") {
          out = out c
          if (c == "\\" && q != "`" && i < n) { out = out substr(line, i + 1, 1); i += 2; continue }
          if (c == q) state = ""
          i++
          continue
        }
        # A shell # opens a comment only at the start of a word ($#, ${#a} are code).
        if (lc != "" && substr(line, i, length(lc)) == lc && (!hash || i == 1 || substr(line, i - 1, 1) ~ /[ \t]/)) break
        if (bopen != "" && substr(line, i, length(bopen)) == bopen) { state = "block"; i += length(bopen); continue }
        if (quotes != "" && index(quotes, c)) { state = "str"; q = c }
        out = out c
        i++
      }
      sub(/[ \t]+$/, "", out)
      if (out != "") print NR "\t" out
    }' "$2"
}

check_comments() {
  local flagged=0 f style before after em=$'\xe2\x80\x94' en=$'\xe2\x80\x93'
  for f in "$@"; do
    [ -f "$snap/$f" ] || continue
    style=$(style_of "$f")
    if [ -z "$style" ]; then
      echo "UNCHECKED: $f"
      flagged=1
      continue
    fi
    if [ ! -f "$f" ]; then
      echo "CODE CHANGED: $f"
      flagged=1
      continue
    fi
    before=$(code_lines "$style" "$snap/$f")
    after=$(code_lines "$style" "$f")
    if [ "$(cut -f2- <<<"$before" | sed "s/$em/-/g; s/$en/-/g")" != "$(cut -f2- <<<"$after" | sed "s/$em/-/g; s/$en/-/g")" ]; then
      echo "CODE CHANGED: $f"
      flagged=1
    elif [ "$(cut -f2- <<<"$before")" != "$(cut -f2- <<<"$after")" ]; then
      # Only dashes differ, so the code lines pair up one to one: name each, the agent checks it.
      awk -F'\t' -v f="$f" 'NR == FNR { old[FNR] = $0; next } { line = $1; sub(/^[0-9]+\t/, ""); o = old[FNR]; sub(/^[0-9]+\t/, "", o); if ($0 != o) print "DASH IN CODE: " f ":" line }' \
        <(printf '%s\n' "$before") <(printf '%s\n' "$after")
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

markdown_text() { # markdown_text <file>: a `<line>\t<prose>` row per line, the prose empty at a break.
  # Frontmatter, fences and tables are not sentences, so they are skipped, as are headings and HTML
  # comments.
  # Skill descriptions in frontmatter are trigger lists, dense on purpose.
  awk '
    NR == 1 && $0 == "---" { fm = 1; print NR "\t"; next }
    fm { if ($0 == "---") fm = 0; print NR "\t"; next }
    /^[[:space:]]*(```|~~~)/ { fence = !fence; print NR "\t"; next }
    fence || html { if (html && index($0, "-->")) html = 0; print NR "\t"; next }
    /^[[:space:]]*<!--/ { if (!index($0, "-->")) html = 1; print NR "\t"; next }
    /^[[:space:]]*$/ || /^[[:space:]]*\|/ || /^#/ { print NR "\t"; next }
    {
      line = $0
      sub(/^[[:space:]]*(>[[:space:]]*)*/, "", line)
      # A list item starts a new paragraph.
      if (line ~ /^([-*+]|[0-9]+\.)[[:space:]]/) {
        print NR "\t"
        sub(/^([-*+]|[0-9]+\.)[[:space:]]+/, "", line)
      }
      # A leading label names a rule in the house style, so it is not a joint.
      # The forms are `**Name** - ` and `**Name:**`, and code-span keys such as `` `key`: ``.
      if (!sub(/^\*\*[^*]+\*\*( -|:)[[:space:]]+/, "", line)) sub(/^\*\*[^*]+:\*\*[[:space:]]+/, "", line)
      sub(/^(`[^`]+`(, | and | or )?)+:[[:space:]]+/, "", line)
      print NR "\t" line
    }' "$1"
}

comment_text() { # comment_text <style> <file>: like markdown_text, for the comments code_lines drops
  local lc="" bopen="" bclose="" quotes=""
  case "$1" in
    hash) lc='#' quotes="\"'\`" ;;
    slash) lc='//' bopen='/*' bclose='*/' quotes="\"'\`" ;;
    dash) lc='--' bopen='/*' bclose='*/' quotes="\"'" ;;
    erb) bopen='<%#' bclose='%>' ;;
  esac
  # The code_lines lexer, keeping the comment instead of the code. A string never spans lines here,
  # because an unclosed quote (an apostrophe in a heredoc) would hide every later comment.
  awk -v lc="$lc" -v bopen="$bopen" -v bclose="$bclose" -v quotes="$quotes" -v hash="$([ "$1" = hash ] && echo 1)" '
    {
      line = $0; n = length(line); com = ""; i = 1
      if (state == "str") state = ""
      while (i <= n) {
        c = substr(line, i, 1)
        if (state == "block") {
          if (substr(line, i, length(bclose)) == bclose) { state = ""; i += length(bclose); com = com " "; continue }
          com = com c; i++
          continue
        }
        if (state == "str") {
          if (c == "\\" && q != "`" && i < n) { i += 2; continue }
          if (c == q) state = ""
          i++
          continue
        }
        if (lc != "" && substr(line, i, length(lc)) == lc && (!hash || i == 1 || substr(line, i - 1, 1) ~ /[ \t]/)) {
          com = com " " substr(line, i + length(lc)); break
        }
        if (bopen != "" && substr(line, i, length(bopen)) == bopen) { state = "block"; i += length(bopen); continue }
        if (quotes != "" && index(quotes, c)) { state = "str"; q = c }
        i++
      }
      sub(/^[ \t*\/#-]+/, "", com); sub(/[ \t]+$/, "", com)
      # Directives, shebangs and empty comment lines end a paragraph.
      if (com == "" || com ~ /^(!|go:|nolint|shellcheck |\+build)/) print NR "\t"
      else print NR "\t" com
    }' "$2"
}

changed_lines() { # changed_lines <ref> <file>: the line numbers of <file> that differ from <ref>
  if ! git ls-files --error-unmatch -- "$2" >/dev/null 2>&1; then
    echo all
    return
  fi
  git diff -U0 --no-color "$1" -- "$2" | awk '
    /^@@/ {
      split($3, a, ","); start = substr(a[1], 2) + 0; len = (2 in a) ? a[2] + 0 : 1
      for (k = 0; k < len; k++) printf "%d ", start + k
    }'
}

dense() { # dense <file> <changed> < text stream: `<file>:<line>: <reason>` per dense sentence touching a changed line
  local em=$'\xe2\x80\x94' en=$'\xe2\x80\x93'
  # A sentence ends at . ! or ? before a space, closing markup or a quote allowed in between.
  awk -F'\t' -v f="$1" -v changed="$2" -v dashes=" (-|$em|$en) " -v ends="[.!?]+[*_)\"'\`]*([[:space:]]|\$)" '
    BEGIN {
      if (changed == "all") all = 1
      else { n = split(changed, c, " "); for (k = 1; k <= n; k++) if (c[k] != "") hit[c[k]] = 1 }
    }
    function check(   t, joints, semis, words, w, k, nw, reason) {
      if (sent ~ /^[[:space:]]*$/ || !touched) { sent = ""; touched = 0; return }
      t = sent
      # A comma or colon between digits (1,000 or 10:30) is a number, not a joint.
      while (gsub(/[0-9][,:][0-9]/, "000", t)) {}
      semis = gsub(/;/, ";", t)
      # A parenthetical is one insertion, so only its opening paren counts.
      joints = gsub(/[,;:(]/, "&", t) + gsub(dashes, "&", t)
      nw = split(t, w, /[[:space:]]+/); words = 0
      for (k = 1; k <= nw; k++) if (w[k] ~ /[[:alnum:]]/) words++
      reason = ""
      if (semis) reason = "semicolon"
      if (joints > 3) reason = reason (reason ? ", " : "") joints " joints"
      if (words > 30) reason = reason (reason ? ", " : "") words " words"
      if (reason != "") { print f ":" start ": " reason; flagged = 1 }
      sent = ""; touched = 0
    }
    function clean(s,   out) {
      gsub(/`[^`]*`/, "X", s)
      # A link counts as its text, so the url adds no colon.
      while (match(s, /\[[^]]*\]\([^)]*\)/)) {
        out = substr(s, RSTART, RLENGTH); sub(/\]\(.*$/, "", out)
        s = substr(s, 1, RSTART - 1) substr(out, 2) substr(s, RSTART + RLENGTH)
      }
      gsub(/[a-z]+:\/\/[^[:space:])]+/, "X", s)
      return s
    }
    {
      if ($2 == "") { check(); next }
      t = clean($2)
      while (t != "") {
        if (sent == "") start = $1
        if (all || ($1 in hit)) touched = 1
        if (match(t, ends)) {
          sent = sent " " substr(t, 1, RSTART + RLENGTH - 1)
          t = substr(t, RSTART + RLENGTH)
          sub(/^[[:space:]]+/, "", t)
          check()
        } else {
          sent = sent " " t
          t = ""
        }
      }
    }
    END { check(); exit flagged }'
}

check_prose() {
  local ref=$1 flagged=0 f style changed
  shift
  if [ "$ref" != --all ] && ! git rev-parse --verify -q "$ref^{commit}" >/dev/null; then
    echo "prose: unknown ref $ref" >&2
    exit 2
  fi
  for f in "$@"; do
    [ -f "$f" ] || continue
    changed=all
    [ "$ref" = --all ] || changed=$(changed_lines "$ref" "$f")
    [ -n "${changed// /}" ] || continue
    case "$f" in
      *.md | *.markdown) markdown_text "$f" | dense "$f" "$changed" || flagged=1 ;;
      *)
        style=$(style_of "$f")
        if [ -z "$style" ]; then
          echo "UNCHECKED: $f"
          flagged=1
          continue
        fi
        comment_text "$style" "$f" | dense "$f" "$changed" || flagged=1
        ;;
    esac
  done
  [ "$flagged" -eq 0 ] && echo "prose: no dense sentences"
  return "$flagged"
}

case "$mode" in
  comments) check_comments "$@" ;;
  literals) check_literals "$@" ;;
  prose) check_prose "$snap" "$@" ;;
  *) usage ;;
esac
