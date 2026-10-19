#!/usr/bin/env bash
# f10 · state - where a run is right now, so the status line can show it.
#
# Steps write a phase of the capture, plan, ship chain as they enter and leave it; the status line
# reads the file back as three glyphs. Without it a run's position is invisible until the plan file
# lands.
#
# **Cosmetic by construction.** Nothing in the pipeline reads this state, so a missing, stale or
# unwritable file exits 0 silently. Only a malformed call (an unknown phase or status) exits 2:
# that is a typo in step prose, and staying silent would freeze the badge.
#
# Usage:
#   f10-state.sh set <capture|plan|ship> <pending|running|done|failed|partial|skipped|prior|blocked> [<leaf>]
#   f10-state.sh task <id> [<url>]      record the task this run is about
#   f10-state.sh note <reason> [<next>] why a run stopped, and what unblocks it
#   f10-state.sh ask <text>             the question a driven run stopped on, options included
#   f10-state.sh final <step>           declare the ship pipeline's last step
#   f10-state.sh seed <skill>           begin a fresh run - hooks call this, steps do not
#   f10-state.sh render [<session id>]  the status-line segment; status-line JSON on stdin
#   f10-state.sh show                   the current run, human-readable (`f10 status` says more)
#   f10-state.sh clear | prune          drop this session's state | drop long-dead sessions
#   f10-state.sh doctor                 is any of this actually wired up?
#   f10-state.sh hook                   dispatch one hook event; hook JSON on stdin
#
# A fixed path, not ${CLAUDE_PLUGIN_DATA}: the renderer runs from the user's `statusLine` command,
# which never sees that variable, so writers and reader would disagree on the file.
#
# Keyed by session, not repo: two sessions over one checkout are two runs, and `/clear` mints a new
# id that retires the badge. Kept outside the repo for stealth mode (conventions/context.md).
#
# People type it, step prose names it bare, and the statusline symlink points at it. The
# `<session>.conventions` marker beside the run state belongs to f10-inject.sh; prune and doctor
# read it here, seed and clear never touch it.

set -uo pipefail

state_dir="${F10_STATE_DIR:-$HOME/.claude/f10/state}"
ttl="${F10_STATE_TTL:-86400}" # a badge older than this reads as stale and renders as nothing
phases="capture plan ship"

# --- the state file: `key value` lines, one per key ---------------------------------------------
# Flat text, not JSON: the status line reads it on every message and refresh tick, and a `while
# read` loop costs no process. Parsed, never sourced, so a half-written file cannot run as shell.

st_task=""
st_url=""
st_root=""
st_leaf=""
st_final=""
st_note=""
st_next=""
st_ask=""
st_updated=0
st_capture="pending"
st_plan="pending"
st_ship="pending"

sid=""
file=""

# session id: the hook payload's ($1), then the F10_SESSION_ID override, then the one Claude Code
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
      task) st_task="$v" ;;
      url) st_url="$v" ;;
      root) st_root="$v" ;;
      leaf) st_leaf="$v" ;;
      final) st_final="$v" ;;
      note) st_note="$v" ;;
      next) st_next="$v" ;;
      ask) st_ask="$v" ;;
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

# Whole-file rewrite through a temp file, so the polling status line never reads a torn file.
save() {
  [ -n "$file" ] || return 1
  mkdir -p "$state_dir" 2>/dev/null || return 1
  # The seed records the root; a run seeded by hand has none. Steps and hooks both run with the
  # checkout as cwd, so the first save fills it in.
  if [ -z "$st_root" ]; then
    st_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd -P)"
  fi
  local tmp="$file.$$"
  {
    echo "v 1"
    echo "task $st_task"
    echo "url $st_url"
    echo "root $st_root"
    echo "capture $st_capture"
    echo "plan $st_plan"
    echo "ship $st_ship"
    echo "leaf $st_leaf"
    echo "final $st_final"
    echo "note $st_note"
    echo "next $st_next"
    echo "ask $st_ask"
    echo "updated $(date +%s)"
  } >"$tmp" 2>/dev/null && mv -f "$tmp" "$file" 2>/dev/null
}

# Entering a phase retires the pending ones before it: a run from a task id never captures, one
# from a plan file never plans, and their leading circles would never fill. `skipped` means
# "nobody said"; positive evidence of earlier work (cmd_task, a saved plan) upgrades to `prior`.
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
  # by-hand fallback. Parsed with string operations, not json_get, to spare a `jq` per refresh.
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

# --- reading hook payloads ---------------------------------------------------------------------
# jq where it exists, sed otherwise, so hooks still work without jq. Only flat scalar fields are
# read, so sed's greedy match on a duplicate key never comes up.
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
    pending | running | done | failed | partial | skipped | prior | blocked) ;;
    *)
      echo "f10-state: unknown status '$status'" >&2
      exit 2
      ;;
  esac

  find_sid || exit 0
  load
  backfill "$phase"
  case "$phase" in
    capture) st_capture="$status" ;;
    plan) st_plan="$status" ;;
    ship) st_ship="$status" ;;
  esac
  # A stop state keeps the leaf (the argument, else the last `running` one): "blocked at judge" is
  # the answer to "ship blocked". A phase running again retires the old note and question.
  case "$status" in
    running)
      st_leaf="$leaf"
      st_note=""
      st_next=""
      st_ask=""
      ;;
    failed | partial | blocked) [ -n "$leaf" ] && st_leaf="$leaf" ;;
    *) st_leaf="" ;;
  esac
  save
  exit 0
}

# Why a run stopped and what unblocks it, written alongside the stop state. Newlines are flattened:
# the file is one `key value` per line, so a newline would start an unknown key.
cmd_note() {
  local reason="${1:-}" next="${2:-}"
  [ -n "$reason" ] || {
    echo "f10-state: note needs a reason" >&2
    exit 2
  }
  find_sid || exit 0
  load
  st_note="$(printf '%s' "$reason" | tr '\n' ' ')"
  st_next="$(printf '%s' "$next" | tr '\n' ' ')"
  save
  exit 0
}

# The questionnaire a driven run stopped on (modes/driven.md), options included, default first: no
# one is in its pane to answer. The driving session reads it via `f10 status <task-id>` and sends
# the answer as the next prompt. Flattened to one line, like note.
cmd_ask() {
  local text="${1:-}"
  [ -n "$text" ] || {
    echo "f10-state: ask needs the question" >&2
    exit 2
  }
  find_sid || exit 0
  load
  st_ask="$(printf '%s' "$text" | tr '\n' ' ')"
  save
  exit 0
}

# The ship pipeline's last step, for the Stop backstop: a turn ending on it is a dropped `set ship
# done` and promotes; mid-pipeline is a pause. Undeclared, Stop always promotes.
cmd_final() {
  local step="${1:-}"
  [ -n "$step" ] || {
    echo "f10-state: final needs a step name" >&2
    exit 2
  }
  find_sid || exit 0
  load
  st_final="$step"
  save
  exit 0
}

cmd_task() {
  local id="${1:-}" url="${2:-}"
  [ -n "$id" ] || {
    echo "f10-state: task needs an id" >&2
    exit 2
  }
  find_sid || exit 0
  load
  st_task="$id"
  [ -n "$url" ] && st_url="$url"
  # A tracker task id means the task predates this run, so an unreported capture becomes `prior`.
  # `done` and `running` stay: capture.md reports those itself.
  case "$st_capture" in
    pending | skipped) st_capture="prior" ;;
  esac
  save
  exit 0
}

# Does the argument read as a description (so the run will capture)? A first token that is a task
# id (ABC-1042, 1042), plan path or url is a reference whatever follows, since the model appends
# context to skill args ("ABC-1042 (task already fetched; ...)"). Otherwise one token is a
# reference and a sentence a description, as the skills route; capture's own report overrides it.
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

# The task the argument's first token names: a task id as is, a plan path by basename (the
# PostToolUse arm's rule). Empty for a url, a description or nothing.
ref_task_of() {
  local a="${1:-}"
  a="${a//--dry-run/}"
  # shellcheck disable=SC2086 # deliberate: the reference, when there is one, is the first token
  set -- $a
  local t="${1:-}"
  case "$t" in
    *.md)
      t="${t##*/}"
      printf '%s' "${t%.md}"
      return 0
      ;;
    http://* | https://*) return 0 ;;
  esac
  [[ "$t" =~ ^[A-Za-z]+-[0-9]+$ || "$t" =~ ^[0-9]+$ ]] && printf '%s' "$t"
  return 0
}

# A skill invocation begins a run and resets the phases, except a chain over one task (plan, then
# ship) whose argument names the task already held: that carries settled phases (done, prior,
# partial) forward, or ship would show this session's plan as `skipped`. A new run resets the task
# id too, so the badge never captions it with the old task.
cmd_seed() {
  local skill="${1:-}" args="${3:-}" cwd="${4:-}"
  find_sid "${2:-}" || exit 0
  # --help prints the skill's arguments and runs nothing, so the badge keeps the last real run.
  case " $args " in *" --help "*) exit 0 ;; esac
  # A leading pipeline name (`ship local: ABC-1042`, as f10 start --local sends) picks the
  # pipeline, not the task: the reference or description is what follows it.
  if [[ "$args" =~ ^[[:space:]]*[A-Za-z][A-Za-z0-9_-]*:[[:space:]]+(.*)$ ]]; then
    args="${BASH_REMATCH[1]}"
  fi

  # Not for capture, which always mints a new task. `ship 1042` continues ABC-1042.
  local ref up_ref up_task chain=0
  ref="$(ref_task_of "$args")"
  if [ "$skill" != "capture" ] && [ -n "$ref" ] && load && [ -n "$st_task" ]; then
    up_ref="$(printf '%s' "$ref" | tr '[:lower:]' '[:upper:]')"
    up_task="$(printf '%s' "$st_task" | tr '[:lower:]' '[:upper:]')"
    if [ "$up_ref" = "$up_task" ] || { [[ "$ref" =~ ^[0-9]+$ ]] && [[ "$up_task" == *"-$ref" ]]; }; then
      chain=1
    fi
  fi

  st_leaf=""
  st_final=""
  st_note=""
  st_next=""
  st_ask=""
  st_ship="pending"
  # The checkout root, so a terminal with no session id can still find its runs, and a session
  # launched from a subdirectory matches one from the top. Outside git the cwd is the root.
  if [ -n "$cwd" ]; then
    st_root="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null || printf '%s' "$cwd")"
  fi
  if [ "$chain" = 1 ]; then
    # settled phases stay; a leftover `running` or `failed` is stale and resets
    case "$st_capture" in done | prior | partial) ;; *) st_capture="pending" ;; esac
    if [ "$skill" = "plan" ]; then
      st_plan="pending" # the plan skill is here to (re)plan; a kept `done` would lie over it
    else
      case "$st_plan" in done | prior | partial) ;; *) st_plan="pending" ;; esac
    fi
  else
    st_task=""
    st_url=""
    st_capture="pending"
    st_plan="pending"
  fi

  # Open the first phase here, where the argument is, rather than trust a step to report it. A
  # description routes plan and ship through capture; a reference sends plan to fetch, whose
  # `set plan running` never arrives when the task is already in context.
  case "$skill" in
    capture) st_capture="running" ;;
    plan) if is_description "$args"; then st_capture="running"; else st_plan="running"; fi ;;
    ship) is_description "$args" && st_capture="running" ;;
  esac
  # A plan path skips fetch, so no `task` call records the task; its basename names it. The file
  # is evidence of earlier work, so pending capture and plan become `prior`. A chain-kept `done`
  # outranks it, and under /f10:plan the phase is already `running`.
  if [ "$skill" = "plan" ] || [ "$skill" = "ship" ]; then
    # shellcheck disable=SC2086 # deliberate: the reference, when there is one, is the first token
    set -- ${args//--dry-run/}
    case "${1:-}" in
      *.md)
        st_task="${1##*/}"
        st_task="${st_task%.md}"
        [ "$st_capture" = "pending" ] && st_capture="prior"
        [ "$st_plan" = "pending" ] && st_plan="prior"
        ;;
    esac
  fi
  save
  exit 0
}

# The loaded run, one `label value` line per fact in `f10 status` order, for a machine without the
# binary. A phase carries its step where known: `running: implement`, `blocked at judge`.
print_run() {
  printf 'task     %s\n' "${st_task:--}"
  printf 'url      %s\n' "${st_url:--}"
  [ -n "$st_root" ] && printf 'root     %s\n' "$st_root"
  local p status
  for p in $phases; do
    case "$p" in
      capture) status="$st_capture" ;;
      plan) status="$st_plan" ;;
      *) status="$st_ship" ;;
    esac
    glyph_for "$status"
    printf '%-8s %s %s\n' "$p" "$_glyph" "$(phase_text "$p" "$status")"
  done
  [ -n "$st_note" ] && printf 'note     %s\n' "$st_note"
  [ -n "$st_next" ] && printf 'next     %s\n' "$st_next"
  [ -n "$st_ask" ] && printf 'ask      %s\n' "$st_ask"
  [ -n "$st_final" ] && printf 'final    %s\n' "$st_final"
  printf 'updated  %ss ago\n' "$(($(date +%s) - st_updated))"
  return 0
}

# A status with its step attached. The leaf is a pipeline step, so only ship carries one.
phase_text() { # phase_text <phase> <status>
  if [ "$1" != "ship" ] || [ -z "$st_leaf" ]; then
    printf '%s' "$2"
    return 0
  fi
  case "$2" in
    running) printf '%s: %s' "$2" "$st_leaf" ;;
    failed | partial | blocked) printf '%s at %s' "$2" "$st_leaf" ;;
    *) printf '%s' "$2" ;;
  esac
}

cmd_show() {
  find_sid || {
    echo "no session id - nothing to show"
    exit 0
  }
  load || {
    echo "no state for session $sid"
    exit 0
  }
  print_run
  exit 0
}

cmd_clear() {
  find_sid || exit 0
  rm -f "$file" 2>/dev/null
  exit 0
}

# The plugin root moves with every update (installs live under a version directory), so the user's
# statusLine names a stable symlink that each session start re-points here. Invoked under that
# name, the script renders (see the dispatch at the foot of the file).
link_statusline() {
  local self="$0" link="$HOME/.claude/f10/statusline"
  case "$self" in /*) ;; *) self="$PWD/$self" ;; esac
  [ -f "$self" ] || return 0
  [ "$self" = "$link" ] && return 0 # never point the link at itself
  mkdir -p "${link%/*}" 2>/dev/null || return 0
  ln -sfn "$self" "$link" 2>/dev/null
  return 0
}

# `<session>.started` is when this session loaded the plugin; `f10 doctor` compares it with the
# install's update time to tell a session that needs a restart. Compact and clear keep the process
# and its loaded plugin, so only startup and resume mark it.
mark_started() {
  case "$(json_get source)" in startup | resume) ;; *) return 0 ;; esac
  find_sid "$1" || return 0
  mkdir -p "$state_dir" 2>/dev/null && : >"$state_dir/$sid.started" 2>/dev/null
  return 0
}

# Sessions end without notice, so every state file outlives its run. A week is far past the TTL,
# so this only reclaims files that already render nothing.
cmd_prune() {
  [ -d "$state_dir" ] || exit 0
  find "$state_dir" -type f \( -name '*.state' -o -name '*.conventions' -o -name '*.started' \) -mtime +7 -delete 2>/dev/null
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

# One entry point for every hook event, dispatching on the payload's hook_event_name, so a new
# event is a case arm, not a script. Nothing here fails a run. A bare `/f10:status` is answered by
# a blocking verdict that ends the turn before the model runs, costing no model turn.
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
      # the typed path: /f10:ship reaches the model as an expansion, never as a Skill tool call.
      # Bare status only: `/f10:status <task-id>` may relay a question, which needs a model turn.
      local name
      name="$(json_get command_name)"
      case "$name" in
        f10:status | status) [ -n "$(json_get command_args)" ] || answer_status "$hook_sid" ;;
      esac
      skill="$(f10_skill_of "$name")"
      [ -n "$skill" ] || exit 0
      cmd_seed "$skill" "$hook_sid" "$(json_get command_args)" "$(json_get cwd)"
      ;;
    PreToolUse)
      # the other path: the model calling the skill itself, which the expansion event never sees
      [ "$(json_get tool_name)" = "Skill" ] || exit 0
      skill="$(f10_skill_of "$(json_get skill tool_input)")"
      [ -n "$skill" ] || exit 0
      cmd_seed "$skill" "$hook_sid" "$(json_get args tool_input)" "$(json_get cwd)"
      ;;
    PostToolUse)
      # the plan file is the plan step's output (conventions/context.md), so writing one marks
      # plan done unreported, and its name is the task id
      local path
      path="$(json_get file_path tool_input)"
      case "$path" in
        */.f10/plans/*.md) ;;
        *) exit 0 ;;
      esac
      find_sid "$hook_sid" || exit 0
      load
      local base="${path##*/}"
      st_task="${base%.md}"
      backfill plan
      st_plan="done"
      st_leaf=""
      save
      ;;
    Stop)
      # Backstop for the most-dropped call, the final `set ship done` after the PR and report.
      # Stop states are left alone. Single-turn capture and plan promote; ship spans many turns,
      # so it promotes only on the declared final step, or when no `final` or leaf is known
      # (overstating is the cheaper error). A run that died mid-pipeline is the ttl's to retire.
      find_sid "$hook_sid" || exit 0
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
        if [ -z "$st_final" ] || [ -z "$st_leaf" ] || [ "$st_leaf" = "$st_final" ]; then
          st_ship="done"
          st_leaf=""
          finalize=1
        fi
      fi
      [ "$finalize" = 1 ] || exit 0
      save
      ;;
    SessionStart)
      link_statusline
      mark_started "$hook_sid"
      cmd_prune
      ;;
  esac
  exit 0
}

# The answer to a bare `/f10:status`, as a blocking verdict on stdout: exit 2 is the error path,
# which wraps stderr in the hook command and echoes the prompt. The binary answers when on PATH;
# print_run is the fallback.
answer_status() {
  local text=""
  if command -v f10 >/dev/null 2>&1; then
    text="$(F10_SESSION_ID="$1" f10 status 2>&1)"
  elif find_sid "$1" && load; then
    text="$(print_run)"
  fi
  [ -n "$text" ] || text="no f10 run in this session"

  printf '{"decision":"block","reason":"%s","hookSpecificOutput":{"hookEventName":"UserPromptExpansion","suppressOriginalPrompt":true}}\n' \
    "$(json_string "$(titled "$text")")"
  exit 0
}

# The block under its own title, so the eye lands there and not on Claude Code's `blocked by hook`
# line; the "no model turn" tag says nothing went wrong. Rule width follows the widest row,
# clamped to 44-72 columns.
titled() {
  local w pad rule
  w="$(printf '%s\n' "$1" | awk '
    { if (length($0) > w) w = length($0) }
    END { if (w < 44) w = 44; if (w > 72) w = 72; print w }
  ')"
  pad=$((w - 25)) # 12 columns of title, 13 of tag
  [ "$pad" -ge 2 ] || pad=2
  rule="$(awk -v n="$w" 'BEGIN { while (i++ < n) printf "─" }')"

  printf 'f10 · status%*sno model turn\n%s\n%s\n' "$pad" "" "$rule" "$1"
}

# Text as the body of a JSON string: backslash, quote and newline escaped, tabs to spaces, other
# control bytes dropped, since one raw control byte makes the verdict unparseable.
json_string() {
  printf '%s' "$1" |
    tr '\t' ' ' |
    sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/[[:cntrl:]]//g' |
    awk 'BEGIN { ORS = "" } { if (NR > 1) print "\\n"; print }'
}

# Which f10 skill a command or Skill-tool name refers to; empty means not ours. The bare form is
# accepted too: a wrong match costs one wrong badge for one turn.
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
  note)
    shift
    cmd_note "$@"
    ;;
  ask)
    shift
    cmd_ask "$@"
    ;;
  final)
    shift
    cmd_final "$@"
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

  set <capture|plan|ship> <pending|running|done|failed|partial|skipped|prior|blocked> [<leaf>]
  task <id> [<url>]        record the task this run is about
  note <reason> [<next>]   why a run stopped, and what unblocks it
  ask <text>               the question a driven run stopped on, options included
  final <step>             declare the ship pipeline's last step
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
