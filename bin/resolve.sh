#!/usr/bin/env bash
# f10 · resolve - one-shot deterministic context resolution.
#
# Does, in a single call, everything conventions/context.md's loading order describes: emit the
# conventions this run needs, locate .f10/instructions/ (with main-worktree fallback), cat
# project.md + the requested per-step overlays, and run the inference probes (remote host,
# stack, verify tooling). A skill runs this once and reads the bundle, instead of spending five
# round trips reading/globbing/greping by hand. It never fails - absence is a valid result.
#
# Conventions are composed here, at read time, so each lives in exactly one file and no skill
# carries one it does not use. context + failure always apply; gaps only where a step records
# or consumes them.
#
# Usage:  resolve.sh <step> [<step> ...]
#   e.g.  resolve.sh capture        |  resolve.sh fetch plan  |  resolve.sh implement pr review

set -uo pipefail
cwd="$(pwd)"
# plugin root, derived from this script's own location (CLAUDE_PLUGIN_ROOT is not guaranteed here)
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# physical form of a path: `pwd` is logical, git's output is already resolved, and the two are
# compared below
canon() { if [ -d "$1" ]; then (cd "$1" && pwd -P); else printf '%s\n' "$1"; fi; }

# --- locate the instructions dir: this checkout, main-worktree fallback, out-of-tree ---
# Everything anchors to `here`, the root of the checkout the run started in (main or a linked
# worktree), so a run from a subdirectory reads and writes the same root a run from the top does.
instr=""
src="none"
here="$(git rev-parse --show-toplevel 2>/dev/null)"
here="$(canon "${here:-$(pwd -P)}")"
main="$(git worktree list --porcelain 2>/dev/null | awk '/^worktree /{print $2; exit}')"
if [ -n "${main:-}" ]; then main="$(canon "$main")"; fi
proj="$(basename "${main:-$here}")"
storage="$here/.f10"
if [ -d "$here/.f10/instructions" ]; then
  instr="$here/.f10/instructions"; src="direct ($here)"
elif [ -n "${main:-}" ] && [ "$main" != "$here" ] && [ -d "$main/.f10/instructions" ]; then
  instr="$main/.f10/instructions"; src="worktree-fallback ($main)"
elif [ -d "$HOME/.f10/$proj/instructions" ]; then
  instr="$HOME/.f10/$proj/instructions"; src="out-of-tree (~/.f10/$proj)"
  storage="$HOME/.f10/$proj"
fi

echo "=== f10 resolve @ $cwd ==="
echo "checkout root: $here"
echo "steps requested: $*"
echo "instructions source: $src"
echo "storage root: $storage  (plans -> $storage/plans/)"
# a config below the root is not a per-directory config - say so rather than pass it over silently
if [ "$(canon "$cwd")" != "$here" ] && [ -d "$cwd/.f10/instructions" ]; then
  echo "note: ignoring nested $cwd/.f10/instructions - resolution is anchored to the checkout root"
fi
echo

# --- conventions: context + failure always; gaps and report only for steps that touch them ---
convs=(context failure)
case " $* " in *" plan "*|*" implement "*) convs+=(gaps) ;; esac
case " $* " in *" capture "*|*" plan "*|*" pr "*|*" push "*|*" deploy "*) convs+=(report) ;; esac
for c in "${convs[@]}"; do
  echo "--- convention: $c ---"
  if [ -f "$root/conventions/$c.md" ]; then
    cat "$root/conventions/$c.md"
  else
    echo "MISSING - $root/conventions/$c.md not found"
  fi
  echo
done

echo "--- project.md ---"
if [ -n "$instr" ] && [ -f "$instr/project.md" ]; then
  cat "$instr/project.md"
else
  echo "ABSENT - no project.md. Infer from the signals below; suggest creating one."
fi
echo

for s in "$@"; do
  echo "--- overlay: $s.md ---"
  if [ -n "$instr" ] && [ -f "$instr/$s.md" ]; then
    cat "$instr/$s.md"
  else
    echo "none"
  fi
  echo
done

echo "--- inferred signals (deterministic; use only where project.md is silent) ---"
origin="$(git remote get-url origin 2>/dev/null || true)"
if [ -n "${origin:-}" ]; then
  echo "remote: $origin"
  case "$origin" in
    *github.com*) echo "host: github  -> PR/issue CLI: gh" ;;
    *gitlab.com*) echo "host: gitlab  -> PR/issue CLI: glab" ;;
    *)            echo "host: unknown -> confirm CLI with user" ;;
  esac
else
  echo "remote: none (no origin)"
fi
for f in go.mod Gemfile package.json; do
  [ -f "$f" ] && echo "stack signal: $f present"
done
ls ./*.gemspec >/dev/null 2>&1 && echo "stack signal: *.gemspec present"
{ [ -f justfile ] || [ -f Justfile ]; } && echo "verify: justfile present (try: just lint / just test)"
[ -f Makefile ] && echo "verify: Makefile present"
if [ -d "$storage/plans" ]; then
  shopt -s nullglob
  plans=("$storage"/plans/*.md)
  shopt -u nullglob
  echo "existing plans: ${#plans[@]} file(s) in $storage/plans/"
fi
echo
echo "=== end resolve (nothing was fetched, written, or created) ==="
