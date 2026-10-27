#!/usr/bin/env bash
# f10 · state - where a run is right now, so the status line can show it.
#
# Steps write a phase of the capture, plan, ship chain as they enter and leave it; the status line
# reads the file back as three glyphs. Without it a run's position is invisible until the plan file
# lands.
#
# The binary owns the file: `f10 state` writes it and `f10 hook` handles every hook event. This
# script keeps two jobs. It renders, because the status line runs it on every refresh and a `while
# read` loop costs no process. And it forwards the writer verbs to the binary under the name step
# prose types.
#
# **Cosmetic by construction.** Nothing in the pipeline reads this state, so a missing binary, a
# missing or stale file exits 0 silently. Only a malformed call (an unknown phase or status) exits
# 2: that is a typo in step prose, and staying silent would freeze the badge.
#
# Usage:
#   f10-state.sh set <capture|plan|ship> <pending|running|done|failed|partial|skipped|prior|blocked> [<leaf>]
#   f10-state.sh task <id> [<url>]      record the task this run is about
#   f10-state.sh note <reason> [<next>] why a run stopped, and what unblocks it
#   f10-state.sh ask <text>             the question a driven run stopped on, options included
#   f10-state.sh final <step>           declare the ship pipeline's last step
#   f10-state.sh pipeline <step>...     declare the ship pipeline's steps, the last one final
#   f10-state.sh show                   the current run, human-readable (`f10 status` says more)
#   f10-state.sh clear | prune          drop this session's state | drop long-dead sessions
#   f10-state.sh render [<session id>]  the status-line segment; status-line JSON on stdin
#   f10-state.sh doctor                 is any of this actually wired up?
#
# A fixed path, not ${CLAUDE_PLUGIN_DATA}: the renderer runs from the user's `statusLine` command,
# which never sees that variable, so writers and reader would disagree on the file.
#
# Keyed by session, not repo: two sessions over one checkout are two runs, and `/clear` mints a new
# id that retires the badge. Kept outside the repo for stealth mode (conventions/context.md).
#
# The statusline symlink points here; `f10 hook` re-points it at every session start. The
# `<session>.conventions` marker beside the run state belongs to f10-inject.sh; doctor reads it.

set -uo pipefail

state_dir="${F10_STATE_DIR:-$HOME/.claude/f10/state}"
ttl="${F10_STATE_TTL:-86400}" # a badge older than this reads as stale and renders as nothing
phases="capture plan ship"

# --- the state file: `key value` lines, one per key ---------------------------------------------
# Flat text, not JSON: the status line reads it on every message and refresh tick, and a `while
# read` loop costs no process. Parsed, never sourced, so a half-written file cannot run as shell.
# Only the keys the badge draws are read; the binary writes the rest.

st_leaf=""
st_updated=0
st_capture="pending"
st_plan="pending"
st_ship="pending"

sid=""
file=""

# session id: the status line's ($1), then the F10_SESSION_ID override, then the one Claude Code
# exports into every Bash call. Empty is normal for a run by hand from a plain shell.
find_sid() {
  sid="${1:-${F10_SESSION_ID:-${CLAUDE_CODE_SESSION_ID:-}}}"
  [ -n "$sid" ] || return 1
  sid="${sid//[^A-Za-z0-9._-]/_}" # it names a file; a session id never needs anything else
  file="$state_dir/$sid.state"
}

load() {
  [ -n "$file" ] && [ -f "$file" ] || return 1
  local k v
  while read -r k v; do
    case "$k" in
      leaf) st_leaf="$v" ;;
      updated) st_updated="$v" ;;
      capture) st_capture="$v" ;;
      plan) st_plan="$v" ;;
      ship) st_ship="$v" ;;
    esac
  done <"$file"
  # A truncated or hand-edited file must degrade to a stale badge, not an arithmetic error on the
  # status line, so the one numeric field is checked here.
  case "$st_updated" in '' | *[!0-9]*) st_updated=0 ;; esac
  return 0
}

# --- rendering --------------------------------------------------------------------------------
# Glyph first, color second: red/green collapses in daltonized themes, so each status has its own
# shape. NO_COLOR and F10_STATE_COLOR=0 drop the color and keep the shapes.

# Chosen by what terminal fonts contain: a missing codepoint is silently drawn from another font
# with its own baseline and width. ◐ (U+25D0) is missing from JetBrains Mono, Fira Code and Hack,
# so the first six are circles-by-fill those fonts all carry. partial (U+F015A) and blocked
# (U+F073A) have no portable circle, so they bet on Nerd Font Material Design icons.
# Override with F10_STATE_GLYPHS="<pending> <running> <done> <skipped> <failed> <prior> <partial> <blocked>".
read -r -a glyph_set <<<"${F10_STATE_GLYPHS:-○ ◎ ● ◌ ✗ ◉ 󰅚 󰜺}"

# These two set globals instead of printing: render calls them per phase on every refresh, and
# `$(glyph_for ...)` would fork a subshell each time.
_glyph=""
_color=""

glyph_for() {
  case "$1" in
    running) _glyph="${glyph_set[1]:-◎}" ;;
    done) _glyph="${glyph_set[2]:-●}" ;;
    skipped) _glyph="${glyph_set[3]:-◌}" ;;
    failed) _glyph="${glyph_set[4]:-✗}" ;;
    prior) _glyph="${glyph_set[5]:-◉}" ;;
    partial) _glyph="${glyph_set[6]:-󰅚}" ;;
    blocked) _glyph="${glyph_set[7]:-󰜺}" ;;
    *) _glyph="${glyph_set[0]:-○}" ;;
  esac
}

color_for() {
  case "$1" in
    done) _color=$'\033[32m' ;;
    running) _color=$'\033[1;36m' ;;
    failed) _color=$'\033[1;31m' ;;
    partial) _color=$'\033[1;33m' ;; # failed's weight in yellow: stopped, but the work survived
    blocked) _color=$'\033[1;35m' ;; # neither red nor yellow: nothing broke, someone said no
    prior) _color=$'\033[2;32m' ;;   # done's green, dimmed: it happened, just not in this run
    *) _color=$'\033[2m' ;;          # pending and skipped are both "nothing happening here"
  esac
}

render() {
  local want="${1:-}"
  # The piped status-line JSON carries the authoritative session id; the environment is the
  # by-hand fallback. Parsed with string operations, to spare a `jq` per refresh.
  if [ -z "$want" ] && [ ! -t 0 ]; then
    local blob rest
    blob="$(cat 2>/dev/null)"
    rest="${blob#*\"session_id\"}"
    if [ "$rest" != "$blob" ]; then
      rest="${rest#*\"}"
      want="${rest%%\"*}"
    fi
  fi

  find_sid "$want" || return 0
  load || return 0

  local now
  now="$(date +%s)"
  [ "$((now - st_updated))" -le "$ttl" ] || return 0

  local color=1
  { [ -n "${NO_COLOR:-}" ] || [ "${F10_STATE_COLOR:-1}" = "0" ]; } && color=0

  local reset="" icons="" p status
  [ "$color" = 1 ] && reset=$'\033[0m'
  for p in $phases; do
    case "$p" in
      capture) status="$st_capture" ;;
      plan) status="$st_plan" ;;
      *) status="$st_ship" ;;
    esac
    glyph_for "$status"
    if [ "$color" = 1 ]; then
      color_for "$status"
      icons="$icons$_color$_glyph$reset"
    else
      icons="$icons$_glyph"
    fi
  done

  # An F10 keycap (md-keyboard_f10, U+F12B4, Nerd Fonts) marks the circles as f10's. No task id
  # or step: every column here is one the branch loses; `show` has the detail. Without a Nerd Font
  # it degrades to one tofu cell.
  local label="󱊴"
  if [ "$color" = 1 ]; then
    label=$'\033[2m'"$label$reset"
  fi

  # A stopped run shows the step it stopped on; a normal run stays three glyphs wide.
  # `f10 status` carries the reason.
  local word=""
  if [ -n "$st_leaf" ]; then
    case "$st_capture$st_plan$st_ship" in
      *failed* | *partial* | *blocked*)
        word=" $st_leaf"
        [ "$color" = 1 ] && word=$'\033[2m'"$word$reset"
        ;;
    esac
  fi

  # Leading space, so a status-line script can append the result unconditionally.
  printf ' %s %s%s' "$label" "$icons" "$word"
}

# --- the writer verbs -------------------------------------------------------------------------

# The binary writes; this name stays because step prose types it. Cached only, so a step's badge
# call never waits on a download or build; with no binary yet it is a no-op, like a missing file.
forward() {
  local out rc
  out="$(F10_SHIM_CACHED_ONLY=1 "$(dirname "${BASH_SOURCE[0]}")/f10" state "$@" 2>&1)"
  rc=$?
  if [ "$rc" = 2 ]; then
    printf '%s\n' "$out" >&2
    exit 2
  fi
  [ "$rc" = 0 ] && [ -n "$out" ] && printf '%s\n' "$out"
  exit 0
}

cmd_doctor() {
  local sid_src="none"
  [ -n "${CLAUDE_CODE_SESSION_ID:-}" ] && sid_src="CLAUDE_CODE_SESSION_ID"
  [ -n "${F10_SESSION_ID:-}" ] && sid_src="F10_SESSION_ID"
  find_sid
  echo "=== f10 state ==="
  printf 'state dir   %s\n' "$state_dir"
  printf 'session     %s (from %s)\n' "${sid:--}" "$sid_src"
  printf 'state file  %s\n' "${file:--}"
  if [ -n "$file" ] && [ -f "$file" ]; then
    printf 'exists      yes\n'
  else
    printf 'exists      no - no f10 run has reported a phase in this session\n'
  fi
  printf 'ttl         %ss\n' "$ttl"
  if [ -n "$sid" ] && [ -f "$state_dir/$sid.conventions" ]; then
    printf 'rules       told to load (f10-inject.sh)\n'
  else
    printf 'rules       not told yet in this session - the first f10 skill does it\n'
  fi
  if [ -d "$state_dir" ]; then
    printf 'files       %s\n' "$(find "$state_dir" -type f -name '*.state' 2>/dev/null | wc -l | tr -d ' ')"
  fi
  local link="$HOME/.claude/f10/statusline"
  if [ -L "$link" ]; then
    printf 'statusline  %s -> %s\n' "$link" "$(readlink "$link")"
  else
    printf 'statusline  %s ABSENT - it is relinked at session start, so start a session\n' "$link"
  fi
  echo
  echo "--- rendered ---"
  local seg
  seg="$(render "" </dev/null)"
  if [ -n "$seg" ]; then
    printf '[%s ]\n' "$seg"
  else
    echo "(nothing - no state for this session, or older than the ttl)"
  fi
  echo
  echo "--- status line ---"
  echo "In your settings.json statusLine command, after stdin is read into \$input:"
  echo "  f10=\$(printf '%s' \"\$input\" | \"\$HOME/.claude/f10/statusline\" 2>/dev/null)"
  echo "then append \"\$f10\" to what it prints. See the README's Status line section."
  exit 0
}

# Invoked through the status-line symlink there is no subcommand to give - the name is the verb.
[ "${0##*/}" = "statusline" ] && set -- render "$@"

case "${1:-}" in
  set | task | note | ask | final | pipeline | show | clear | prune) forward "$@" ;;
  render)
    shift
    render "${1:-}"
    ;;
  doctor) cmd_doctor ;;
  *)
    cat >&2 <<'USAGE'
f10-state.sh - where an f10 run is right now, for the status line to render.

  set <capture|plan|ship> <pending|running|done|failed|partial|skipped|prior|blocked> [<leaf>]
  task <id> [<url>]        record the task this run is about
  note <reason> [<next>]   why a run stopped, and what unblocks it
  ask <text>               the question a driven run stopped on, options included
  final <step>             declare the ship pipeline's last step
  pipeline <step>...       declare the ship pipeline's steps, the last one final
  show                     the current run, human-readable
  clear | prune            drop this session's state | drop long-dead sessions
  render [<session id>]    the status-line segment; status-line JSON on stdin
  doctor                   is any of this actually wired up?
USAGE
    exit 2
    ;;
esac
