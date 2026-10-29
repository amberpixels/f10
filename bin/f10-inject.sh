#!/usr/bin/env bash
# f10 · inject - tell the model when to load the conventions, so no model decides whether it holds them.
#
# A hook, JSON on stdin. A session's first f10 skill that runs a step, typed (UserPromptExpansion)
# or model-invoked (PreToolUse on Skill), gets one line of context: run f10-load-conventions.sh. So does a
# SessionStart with source `compact` in a session that had them, since compaction drops the text.
# The model cannot judge that itself: after compaction the summary remembers the load, not the text.
#
# The load also prints the command reference (`f10 help --agent`), so it arrives on the same triggers.
# One line, not the conventions themselves: Claude Code caps one hook's context at 10,000 characters and
# the conventions are over twice that.
#
# Whether the session holds them is a marker beside the run state, `<session>.conventions`.
# The binary's seed and clear never touch it (a new run is not a new context); its prune and
# f10-state.sh's doctor read it. The session-id helper mirrors f10-state.sh's, which runs on every
# status-line tick and so sources nothing.
#
# Usage:  f10-inject.sh   (from hooks/hooks.json only)

set -uo pipefail

state_dir="${F10_STATE_DIR:-$HOME/.claude/f10/state}"

# The skills that run a step and so read the conventions. status, board, read, start, drive and finish run the binary
# alone; --help loads nothing (modes/help.md).
skills="brainstorm capture catchup cleanup debug demo explain judge plan resolve review ship standup"

# shellcheck disable=SC2016 # the backticks are markdown for the model, not a command
line='This context does not hold the f10 conventions: run `f10-load-conventions.sh` before anything else.'

json=""
json_get() { # json_get <key> [<parent object>], jq where it exists, sed otherwise
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

skill_of() { # the skill a payload names, as `f10:plan` or `plan`, if it reads the conventions
  # a bare `debug` is as likely another plugin's
  [ "$1" = "debug" ] && return 0
  local n="${1#f10:}"
  case " $skills " in *" $n "*) printf '%s' "$n" ;; esac
}

emit() { # the hook verdict; the line holds no character JSON would need escaped
  printf '{"hookSpecificOutput":{"hookEventName":"%s","additionalContext":"%s"}}\n' "$1" "$line"
}

[ -t 0 ] && exit 0
json="$(cat)"
[ -n "$json" ] || exit 0

sid="$(json_get session_id)"
sid="${sid:-${F10_SESSION_ID:-${CLAUDE_CODE_SESSION_ID:-}}}"
[ -n "$sid" ] || exit 0
sid="${sid//[^A-Za-z0-9._-]/_}"
marker="$state_dir/$sid.conventions"

event="$(json_get hook_event_name)"
skill=""
args=""
case "$event" in
  UserPromptExpansion)
    skill="$(skill_of "$(json_get command_name)")"
    args="$(json_get command_args)"
    ;;
  PreToolUse)
    [ "$(json_get tool_name)" = "Skill" ] || exit 0
    skill="$(skill_of "$(json_get skill tool_input)")"
    args="$(json_get args tool_input)"
    ;;
  SessionStart)
    [ "$(json_get source)" = "compact" ] && [ -f "$marker" ] && emit "$event"
    exit 0
    ;;
  *) exit 0 ;;
esac

[ -n "$skill" ] || exit 0
case " $args " in *" --help "*) exit 0 ;; esac
[ -f "$marker" ] && exit 0

emit "$event"
mkdir -p "$state_dir" 2>/dev/null || exit 0
date +%s >"$marker" 2>/dev/null
exit 0
