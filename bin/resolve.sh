#!/usr/bin/env bash
# f10 · resolve — one-shot deterministic context resolution.
#
# Does, in a single call, everything context.md's loading order describes: locate
# .f10/instructions/ (with main-worktree fallback), cat project.md + the requested
# per-step overlays, and run the inference probes (remote host, stack, verify tooling).
# A skill runs this once and reads the bundle, instead of spending five round trips
# reading/globbing/greping by hand. It never fails — absence is a valid result.
#
# Usage:  resolve.sh <step> [<step> ...]
#   e.g.  resolve.sh capture        |  resolve.sh fetch plan  |  resolve.sh implement pr review

set -uo pipefail
cwd="$(pwd)"

# --- locate the instructions dir: current checkout, main-worktree fallback, out-of-tree ---
instr=""
src="none"
storage=".f10"
main="$(git worktree list --porcelain 2>/dev/null | awk '/^worktree /{print $2; exit}')"
proj="$(basename "${main:-$cwd}")"
if [ -d ".f10/instructions" ]; then
  instr=".f10/instructions"; src="direct"
elif [ -n "${main:-}" ] && [ -d "$main/.f10/instructions" ]; then
  instr="$main/.f10/instructions"; src="worktree-fallback ($main)"
elif [ -d "$HOME/.f10/$proj/instructions" ]; then
  instr="$HOME/.f10/$proj/instructions"; src="out-of-tree (~/.f10/$proj)"
  storage="$HOME/.f10/$proj"
fi

echo "=== f10 resolve @ $cwd ==="
echo "steps requested: $*"
echo "instructions source: $src"
echo "storage root: $storage  (plans -> $storage/plans/)"
echo

echo "--- project.md ---"
if [ -n "$instr" ] && [ -f "$instr/project.md" ]; then
  cat "$instr/project.md"
else
  echo "ABSENT — no project.md. Infer from the signals below; suggest creating one."
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
  n="$(ls "$storage"/plans/*.md 2>/dev/null | wc -l | tr -d ' ')"
  echo "existing plans: $n file(s) in $storage/plans/"
fi
echo
echo "=== end resolve (nothing was fetched, written, or created) ==="
