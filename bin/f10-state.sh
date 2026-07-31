#!/usr/bin/env bash
# f10 · state - where a run is right now, so the status line can show it.
#
# f10's three skills are one chain - capture → plan → ship - but a run leaves no trace of its
# position anywhere a terminal can see. The plan file lands on disk only at the end, the tracker
# knows nothing until capture is done, and everything in between is prose scrolling past. This
# script is that missing trace: steps write a phase as they enter and leave it, and the status
# line reads the same file back and renders three glyphs.
#
# **Cosmetic by construction.** Nothing in the pipeline reads this state back to decide anything,
# so a missing file, a stale one, or a write that fails is never an error - the badge simply does
# not show. Every subcommand that cannot do its job exits 0 and prints nothing. The one exception
# is a malformed *call* (a phase or status that is not one), which exits 2: that is a typo in a
# step's prose, not a runtime condition, and a silent typo would leave a badge frozen forever.
#
# Usage:
#   f10-state.sh set <capture|plan|ship> <pending|running|done|failed|partial|skipped|prior> [<leaf>]
#   f10-state.sh task <id> [<url>]      record the task this run is about
#   f10-state.sh seed <skill>           begin a fresh run - hooks call this, steps do not
#   f10-state.sh render [<session id>]  the status-line segment; status-line JSON on stdin
#   f10-state.sh show                   the current run, human-readable
#   f10-state.sh clear | prune          drop this session's state | drop long-dead sessions
#   f10-state.sh doctor                 is any of this actually wired up?
#   f10-state.sh hook                   dispatch one hook event; hook JSON on stdin
#
# Where the state lives is a fixed path, deliberately, and not ${CLAUDE_PLUGIN_DATA}: the renderer
# runs from the *user's* `statusLine` command, which is not a plugin context and never sees that
# variable. A plugin-relative root would have the writers and the reader disagree about where the
# file is, with nothing to show for it either way.
#
# Keyed by session rather than by repo, because two sessions over one checkout are two runs - and
# `/clear` mints a new session id, which retires the badge for free. It lives outside the repo,
# which is also what stealth mode wants (conventions/context.md): nothing f10-shaped inside the
# project directory, not even untracked.

set -uo pipefail

state_dir="${F10_STATE_DIR:-$HOME/.claude/f10/state}"
ttl="${F10_STATE_TTL:-86400}" # a badge older than this reads as stale and renders as nothing
phases="capture plan ship"

# --- the state file: `key value` lines, one per key ---------------------------------------------
# Flat text rather than JSON, because the reader is a status line: it runs on every assistant
# message and on every refresh tick, and a `while read` loop costs no process where `jq` costs one.
# Parsed, never sourced - a half-written file should produce a wrong badge, not run as shell.

st_task=""
st_url=""
st_leaf=""
st_updated=0
st_capture="pending"
st_plan="pending"
st_ship="pending"

sid=""
file=""

# session id, in the order the callers can supply it: an explicit override, the hook payload's
# value (passed as $1 by the hook dispatcher), then the variable Claude Code exports into every
# Bash tool call. Empty is a normal outcome - a script run by hand from a plain shell has none.
resolve_sid() {
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
      task) st_task="$v" ;;
      url) st_url="$v" ;;
      leaf) st_leaf="$v" ;;
      updated) st_updated="$v" ;;
      capture) st_capture="$v" ;;
      plan) st_plan="$v" ;;
      ship) st_ship="$v" ;;
    esac
  done <"$file"
  # A truncated or hand-edited file must degrade to a stale badge, never to an arithmetic error on
  # the status line's stdout - so the one field anything does maths on is checked on the way in.
  case "$st_updated" in '' | *[!0-9]*) st_updated=0 ;; esac
  return 0
}

# Whole-file rewrite through a temp file: the file is six lines, and a torn read from the status
# line - which polls this thing - would flicker a half-updated badge.
save() {
  [ -n "$file" ] || return 1
  mkdir -p "$state_dir" 2>/dev/null || return 1
  local tmp="$file.$$"
  {
    echo "v 1"
    echo "task $st_task"
    echo "url $st_url"
    echo "capture $st_capture"
    echo "plan $st_plan"
    echo "ship $st_ship"
    echo "leaf $st_leaf"
    echo "updated $(date +%s)"
  } >"$tmp" 2>/dev/null && mv -f "$tmp" "$file" 2>/dev/null
}

# Entering a phase retires the ones before it. A run reached from a task id never captures, and a
# run handed a plan file never plans, so those phases are not pending - they happened elsewhere, or
# they will not happen. Without this rule every route but the full free-text one would leave
# leading circles that never fill, and no step would ever be the obvious place to say so. It is a
# mechanical consequence of the chain's order, which is why it lives here and not in prose.
#
# `skipped` is the guess this rule can make on its own: "nobody said, and now it is too late".
# Where positive evidence arrives that a phase's work exists from before - a task id resolving
# (cmd_task), the ship skill reusing a saved plan - the phase is upgraded to `prior` instead:
# done, just not by this run.
backfill() {
  case "$1" in
    plan)
      [ "$st_capture" = "pending" ] && st_capture="skipped"
      ;;
    ship)
      [ "$st_capture" = "pending" ] && st_capture="skipped"
      [ "$st_plan" = "pending" ] && st_plan="skipped"
      ;;
  esac
  return 0
}

# --- rendering --------------------------------------------------------------------------------
# Glyph first, color second. A status line is read at a glance, often in a daltonized theme, and
# red/green is precisely the pair that collapses there - so each status carries its own shape and
# the color only reinforces it. NO_COLOR and F10_STATE_COLOR=0 drop the color and keep the shapes.

# The glyphs are one list, and which seven they are was decided by what terminal fonts actually
# contain. A codepoint the font lacks does not fail - the OS quietly substitutes another font, whose
# baseline and advance width are its own, and the badge renders as circles that do not sit on one
# line. U+25D0 ◐, the obvious "half done" mark, is missing from JetBrains Mono, Fira Code and Hack
# alike, which is how this list ended up circles-by-fill instead: ring, double ring, solid, dotted,
# a cross, and a fisheye (U+25C9, a dot inside a ring - "done, just not here"), all six present in
# the common programming fonts. The seventh, partial ("stopped, but the work survived"), is the
# label's bet rather than the circles': no crossed-circle codepoint exists across those same fonts,
# so it is Material Design's md-close-circle-outline (U+F015A), which every Nerd Font carries.
# Override with F10_STATE_GLYPHS="<pending> <running> <done> <skipped> <failed> <prior> <partial>".
read -r -a glyph_set <<<"${F10_STATE_GLYPHS:-○ ◎ ● ◌ ✗ ◉ 󰅚}"

# These two assign to a global instead of printing, and are called rather than substituted. Render
# needs both for each of three phases, on every assistant message and every refresh tick, and
# `$(glyph …)` would fork a subshell for each one - six processes to draw six characters.
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
    *) _glyph="${glyph_set[0]:-○}" ;;
  esac
}

color_for() {
  case "$1" in
    done) _color=$'\033[32m' ;;
    running) _color=$'\033[1;36m' ;;
    failed) _color=$'\033[1;31m' ;;
    partial) _color=$'\033[1;33m' ;; # failed's weight in yellow: stopped, but the work survived
    prior) _color=$'\033[2;32m' ;;   # done's green, dimmed: it happened, just not in this run
    *) _color=$'\033[2m' ;;          # pending and skipped are both "nothing happening here"
  esac
}

render() {
  local want="${1:-}"
  # The status line pipes its own JSON in, and the session id it carries is the authoritative one
  # for the window being drawn - the environment is only the fallback that makes `render` useful by
  # hand. Pulled out with bash string operations rather than the json_get below, which would spend a
  # whole `jq` process on one flat field, every assistant message and every refresh tick.
  if [ -z "$want" ] && [ ! -t 0 ]; then
    local blob rest
    blob="$(cat 2>/dev/null)"
    rest="${blob#*\"session_id\"}"
    if [ "$rest" != "$blob" ]; then
      rest="${rest#*\"}"
      want="${rest%%\"*}"
    fi
  fi

  resolve_sid "$want" || return 0
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

  # The label: an F10 keycap icon, so the three circles read as f10's and not some other plugin's.
  # Nothing else - no task id, no step name - because the segment sits inline in a status line's
  # first row, where every column it takes is a column the branch loses. The icon is Material
  # Design's md-keyboard_f10 (U+F12B4), which every Nerd Font carries; that is a narrower bet than
  # the circles' but the same shape of bet, since a status line dense enough to want this badge is
  # overwhelmingly already on a patched font. On anything else it degrades to one substituted or
  # tofu cell, not a broken badge. Task and leaf still surface in `show`, which is where a human
  # who wants the detail already is.
  local label="󱊴"
  if [ "$color" = 1 ]; then
    label=$'\033[2m'"$label$reset"
  fi

  # A leading space, so a status-line script can concatenate the result unconditionally: empty
  # means empty, and the segment brings its own separator when it is not.
  printf ' %s %s' "$label" "$icons"
}

# --- reading hook payloads ---------------------------------------------------------------------
# jq where it exists, sed where it does not. Only flat scalar fields are ever read, so the sed
# fallback's one weakness - a greedy match landing on a later duplicate key - has no field here it
# can reach. Keeping the fallback means hooks still work on a machine without jq, which is exactly
# the machine where a hard dependency would fail silently at 5ms into a session.
json=""

json_get() { # json_get <key> [<parent object>]
  local k="$1" parent="${2:-}"
  if command -v jq >/dev/null 2>&1; then
    if [ -n "$parent" ]; then
      printf '%s' "$json" | jq -r --arg p "$parent" --arg k "$k" '.[$p][$k] // empty' 2>/dev/null
    else
      printf '%s' "$json" | jq -r --arg k "$k" '.[$k] // empty' 2>/dev/null
    fi
    return 0
  fi
  printf '%s' "$json" | tr -d '\n' |
    sed -n "s/.*\"$k\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

# --- subcommands -------------------------------------------------------------------------------

cmd_set() {
  local phase="${1:-}" status="${2:-}" leaf="${3:-}"
  case "$phase" in
    capture | plan | ship) ;;
    *)
      echo "f10-state: unknown phase '$phase' (want: $phases)" >&2
      exit 2
      ;;
  esac
  case "$status" in
    pending | running | done | failed | partial | skipped | prior) ;;
    *)
      echo "f10-state: unknown status '$status'" >&2
      exit 2
      ;;
  esac

  resolve_sid || exit 0
  load
  backfill "$phase"
  case "$phase" in
    capture) st_capture="$status" ;;
    plan) st_plan="$status" ;;
    ship) st_ship="$status" ;;
  esac
  # the leaf belongs to a step that is running; anything else is a phase boundary and clears it
  if [ "$status" = "running" ]; then
    st_leaf="$leaf"
  else
    st_leaf=""
  fi
  save
  exit 0
}

cmd_task() {
  local id="${1:-}" url="${2:-}"
  [ -n "$id" ] || {
    echo "f10-state: task needs an id" >&2
    exit 2
  }
  resolve_sid || exit 0
  load
  st_task="$id"
  [ -n "$url" ] && st_url="$url"
  # A task id resolving is positive evidence the task existed before this run - so a capture
  # nobody reported (pending) or one backfill already wrote off (skipped) becomes `prior`, not a
  # hollow circle. `done` and `running` stay: capture.md reports those itself, in this run.
  case "$st_capture" in
    pending | skipped) st_capture="prior" ;;
  esac
  save
  exit 0
}

# Does this run's argument read as a description rather than a reference? The first token decides
# where it can: a task id (WS-2703, or bare 2703), a plan path, a url - each is a reference no
# matter what follows it, because the model routinely appends parenthetical context to skill args
# ("WS-2703 (Notion task already fetched; ...)"). Counting tokens across the whole argument read
# every such decorated reference as a description, which is how a run over a pre-existing task once
# wore a solid green capture it never did. Only when the first token claims nothing does the old
# tiebreak apply: one token is a reference, a sentence is a description. That is the same
# distinction the skills themselves route on, and it is the only thing at seed time that knows
# whether capture is a phase this run will actually reach.
#
# It has to be guessed here because the alternative is worse. Reporting is best-effort, so a phase
# still `pending` means "nobody said" - not "it did not happen" - and the badge has to resolve that
# ambiguity in one direction or the other. Resolving it as "skipped" is what made a run that really
# did capture show a hollow first glyph. Guessing the route instead is wrong only for arguments no
# one writes, and the capture step's own report overrides it the moment it speaks.
is_description() {
  local a="${1:-}"
  a="${a//--dry-run/}"
  # shellcheck disable=SC2086 # deliberate: word splitting is how tokens get counted
  set -- $a
  [ "$#" -gt 0 ] || return 1
  case "$1" in
    *.md | http://* | https://*) return 1 ;;
  esac
  [[ "$1" =~ ^[A-Za-z]+-[0-9]+$ || "$1" =~ ^[0-9]+$ ]] && return 1
  [ "$#" -gt 1 ]
}

# A skill invocation begins a run, and a run owns the three glyphs for its duration - so this
# resets the phases rather than merging into whatever the last one left behind. The task id is
# reset with them: keeping it would caption the new run with the old run's task, which is the one
# way this badge could actively mislead.
cmd_seed() {
  local skill="${1:-}" args="${3:-}"
  resolve_sid "${2:-}" || exit 0
  st_task=""
  st_url=""
  st_leaf=""
  st_capture="pending"
  st_plan="pending"
  st_ship="pending"
  # /f10:capture is one phase from end to end. /f10:plan and /f10:ship route on their argument, and
  # a description routes them through capture first - so the phase opens here, where the argument
  # is, rather than waiting for a step that may never think to mention it.
  case "$skill" in
    capture) st_capture="running" ;;
    plan | ship) is_description "$args" && st_capture="running" ;;
  esac
  # A plan-path argument names the task in its basename - that is the naming rule the PostToolUse
  # arm already relies on - and the plan-file route skips fetch, so no `task` call ever arrives to
  # record it. This is also the one route where the seed itself holds positive evidence of earlier
  # work (a plan file exists, so capture happened wherever the plan did), which is cmd_task's bar
  # for `prior` rather than backfill's later "nobody said" shrug of `skipped`.
  if [ "$skill" = "plan" ] || [ "$skill" = "ship" ]; then
    # shellcheck disable=SC2086 # deliberate: the reference, when there is one, is the first token
    set -- ${args//--dry-run/}
    case "${1:-}" in
      *.md)
        st_task="${1##*/}"
        st_task="${st_task%.md}"
        [ "$st_capture" = "pending" ] && st_capture="prior"
        ;;
    esac
  fi
  save
  exit 0
}

cmd_show() {
  resolve_sid || {
    echo "no session id - nothing to show"
    exit 0
  }
  load || {
    echo "no state for session $sid"
    exit 0
  }
  printf 'task     %s\n' "${st_task:--}"
  printf 'url      %s\n' "${st_url:--}"
  local p status
  for p in $phases; do
    case "$p" in
      capture) status="$st_capture" ;;
      plan) status="$st_plan" ;;
      *) status="$st_ship" ;;
    esac
    glyph_for "$status"
    printf '%-8s %s %s\n' "$p" "$_glyph" "$status"
  done
  [ -n "$st_leaf" ] && printf 'leaf     %s\n' "$st_leaf"
  printf 'updated  %ss ago\n' "$(($(date +%s) - st_updated))"
  exit 0
}

cmd_clear() {
  resolve_sid || exit 0
  rm -f "$file" 2>/dev/null
  exit 0
}

# The status line is configured once, by hand, in the user's own settings - but the plugin root it
# would have to name moves with every plugin update (a marketplace install lives under a version
# directory). So the settings file names a stable path here instead, and every session start
# re-points that path at whatever the current plugin root is. Invoked under that name, the script
# renders: see the dispatch at the foot of the file.
link_statusline() {
  local self="$0" link="$HOME/.claude/f10/statusline"
  case "$self" in /*) ;; *) self="$PWD/$self" ;; esac
  [ -f "$self" ] || return 0
  [ "$self" = "$link" ] && return 0 # never point the link at itself
  mkdir -p "${link%/*}" 2>/dev/null || return 0
  ln -sfn "$self" "$link" 2>/dev/null
  return 0
}

# Sessions end without telling anyone - a window closes, a machine reboots - so nothing deletes a
# state file at the right moment and every one of them outlives its run. A week is far past the TTL
# that stops them rendering, so this only reclaims files that already showed nothing.
cmd_prune() {
  [ -d "$state_dir" ] || exit 0
  find "$state_dir" -type f -name '*.state' -mtime +7 -delete 2>/dev/null
  exit 0
}

cmd_doctor() {
  local sid_src="none"
  [ -n "${CLAUDE_CODE_SESSION_ID:-}" ] && sid_src="CLAUDE_CODE_SESSION_ID"
  [ -n "${F10_SESSION_ID:-}" ] && sid_src="F10_SESSION_ID"
  resolve_sid
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
  printf 'jq          %s\n' "$(command -v jq >/dev/null 2>&1 && echo present || echo absent)"
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

# One entry point for every hook event, dispatching on the payload's own hook_event_name rather
# than on an argument, so hooks.json registers the same command everywhere and a new event costs a
# case arm instead of a new script. Always exits 0: a badge is never a reason to interrupt a run.
cmd_hook() {
  [ -t 0 ] && exit 0
  json="$(cat)"
  [ -n "$json" ] || exit 0

  if [ -n "${F10_STATE_DEBUG:-}" ]; then
    mkdir -p "$state_dir" 2>/dev/null
    printf '%s\n' "$json" >>"$state_dir/hooks.log" 2>/dev/null
  fi

  local event hook_sid skill finalize
  event="$(json_get hook_event_name)"
  hook_sid="$(json_get session_id)"

  case "$event" in
    UserPromptExpansion)
      # the typed path: /f10:ship reaches the model as an expansion, never as a Skill tool call
      skill="$(f10_skill_of "$(json_get command_name)")"
      [ -n "$skill" ] || exit 0
      cmd_seed "$skill" "$hook_sid" "$(json_get command_args)"
      ;;
    PreToolUse)
      # the other path: the model calling the skill itself, which the expansion event never sees
      [ "$(json_get tool_name)" = "Skill" ] || exit 0
      skill="$(f10_skill_of "$(json_get skill tool_input)")"
      [ -n "$skill" ] || exit 0
      cmd_seed "$skill" "$hook_sid" "$(json_get args tool_input)"
      ;;
    PostToolUse)
      # the plan file *is* the plan step's output (conventions/context.md), so writing one is the
      # one phase transition that needs no one to remember to report it - and its name is the task
      # id, which is how a run reached from a task id gets its caption for free.
      local path
      path="$(json_get file_path tool_input)"
      case "$path" in
        */.f10/plans/*.md) ;;
        *) exit 0 ;;
      esac
      resolve_sid "$hook_sid" || exit 0
      load
      local base="${path##*/}"
      st_task="${base%.md}"
      backfill plan
      st_plan="done"
      st_leaf=""
      save
      ;;
    Stop)
      # The turn is over, so nothing is still running - whatever the last step forgot to say. This
      # is the backstop for the instruction a run is most likely to drop: the final `set ship done`
      # arrives after the pipeline, the PR and the report, which is precisely where an agent stops
      # following prose. Left to the steps alone, a finished run sits on a spinning glyph until the
      # ttl retires it.
      #
      # `failed` and `partial` are left alone (failure.md marks them before reporting, and that is
      # the outcome), and a pause mid-run - a gap questionnaire, a confirmation - reads as done
      # until the next step says otherwise. Overstating by one glyph for the length of a question
      # is the cheaper error.
      resolve_sid "$hook_sid" || exit 0
      load || exit 0
      finalize=0
      if [ "$st_capture" = "running" ]; then
        st_capture="done"
        finalize=1
      fi
      if [ "$st_plan" = "running" ]; then
        st_plan="done"
        finalize=1
      fi
      if [ "$st_ship" = "running" ]; then
        st_ship="done"
        finalize=1
      fi
      [ "$finalize" = 1 ] || exit 0
      st_leaf=""
      save
      ;;
    SessionStart)
      link_statusline
      cmd_prune
      ;;
  esac
  exit 0
}

# Which f10 skill a command or Skill-tool name refers to - empty for anything else, which the
# caller reads as "not ours, do nothing". Both paths carry the plugin's own prefix (`/f10:ship`,
# `Skill(f10:ship)`), so the prefixed form is what this really matches; the bare form is accepted
# only because a `capture`/`plan`/`ship` reaching *these* hooks at all is overwhelmingly f10's, and
# the cost of being wrong is one wrong badge for one turn.
f10_skill_of() {
  case "${1:-}" in
    f10:capture | capture) printf '%s' "capture" ;;
    f10:plan | plan) printf '%s' "plan" ;;
    f10:ship | ship) printf '%s' "ship" ;;
  esac
}

# Invoked through the status-line symlink there is no subcommand to give - the name is the verb.
[ "${0##*/}" = "statusline" ] && set -- render "$@"

case "${1:-}" in
  set)
    shift
    cmd_set "$@"
    ;;
  task)
    shift
    cmd_task "$@"
    ;;
  seed)
    shift
    cmd_seed "$@"
    ;;
  render)
    shift
    render "${1:-}"
    ;;
  show) cmd_show ;;
  clear) cmd_clear ;;
  prune) cmd_prune ;;
  doctor) cmd_doctor ;;
  hook) cmd_hook ;;
  *)
    cat >&2 <<'USAGE'
f10-state.sh - where an f10 run is right now, for the status line to render.

  set <capture|plan|ship> <pending|running|done|failed|partial|skipped|prior> [<leaf>]
  task <id> [<url>]        record the task this run is about
  seed <skill>             begin a fresh run - hooks call this, steps do not
  render [<session id>]    the status-line segment; status-line JSON on stdin
  show                     the current run, human-readable
  clear | prune            drop this session's state | drop long-dead sessions
  doctor                   is any of this actually wired up?
  hook                     dispatch one hook event; hook JSON on stdin
USAGE
    exit 2
    ;;
esac
