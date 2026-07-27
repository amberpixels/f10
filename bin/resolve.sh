#!/usr/bin/env bash
# f10 · resolve - one-shot deterministic context resolution.
#
# Does, in a single call, everything conventions/context.md's loading order describes: emit the
# conventions this run needs, locate .f10/instructions/ (layering a linked worktree's on top of
# main's), cat each layer's project.md + the requested per-step overlays in precedence order, and
# run the inference probes (remote host, stack, verify tooling). A skill runs this once and reads
# the bundle, instead of spending five round trips reading/globbing/greping by hand. It never
# fails - absence is a valid result.
#
# It concatenates and probes; it does not interpret. The single exception is the worktree layer's
# `Layering` declaration, which has to be read before anything can be concatenated.
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

# hostname of a git remote url - scp-like (git@host:path), ssh://, https://, userinfo and port
# all handled. Matching the whole url instead would route a gitlab remote whose *path* happens to
# contain "github.com" to gh, picking the wrong adapter silently.
host_of() {
  local h="$1"
  h="${h#*://}"
  h="${h#*@}"
  h="${h%%/*}"
  printf '%s\n' "${h%%:*}"
}

# Is this host one the CLI is already logged in to? gh lists each host as a top-level key of
# hosts.yml, glab nests them under `hosts:` in config.yml. That covers GitHub Enterprise and
# self-hosted GitLab without an allowlist of hostnames.
# Both files hold OAuth tokens, so this matches and returns a status - never cat them into the
# bundle, however much the rest of this script cats things.
knows_host() {
  [ -f "$1" ] || return 1
  local esc="${2//./\\.}"
  grep -qE "^[[:space:]]*${esc}:" "$1"
}

# --- locate the instructions layer(s): main's, a linked worktree's, or both ---
# Everything anchors to `here`, the root of the checkout the run started in (main or a linked
# worktree), so a run from a subdirectory reads and writes the same root a run from the top does.
# Both in-tree layers are read when both exist - main's first, the worktree's on top - unless the
# worktree declares that it replaces main. Out-of-tree is per-project by construction, so it is a
# single layer and only a last resort.
src="none"
here="$(git rev-parse --show-toplevel 2>/dev/null)"
here="$(canon "${here:-$(pwd -P)}")"
main="$(git worktree list --porcelain 2>/dev/null | awk '/^worktree /{print $2; exit}')"
if [ -n "${main:-}" ]; then main="$(canon "$main")"; fi
proj="$(basename "${main:-$here}")"
storage="$here/.f10"
steps="$*"

# the base layer is main's, or simply this checkout's where git cannot name a main worktree
base_label="main"
base_root="${main:-$here}"
[ -n "${main:-}" ] || base_label="checkout"

base_instr=""
wt_instr=""
[ -d "$base_root/.f10/instructions" ] && base_instr="$base_root/.f10/instructions"
[ "$here" != "$base_root" ] && [ -d "$here/.f10/instructions" ] && wt_instr="$here/.f10/instructions"

# `Layering - replaces main` in the worktree's own project.md takes main's place wholesale;
# anything else - another value, no line, no project.md - extends it. Always-extend would leak
# main's stack into a worktree that deliberately rewrote it.
layering="extends"
if [ -n "$wt_instr" ] && [ -n "$base_instr" ]; then
  decl="$(grep -im1 -E '^[[:space:]]*[-*#]*[[:space:]]*\**Layering\**[[:space:]]*[-:]' \
    "$wt_instr/project.md" 2>/dev/null)"
  case "$decl" in *[Rr]eplaces*) layering="replaces" ;; esac
fi

l1_label=""
l1_dir=""
l2_label=""
l2_dir=""
if [ -n "$base_instr" ] && [ -n "$wt_instr" ] && [ "$layering" = "extends" ]; then
  l1_label="$base_label"
  l1_dir="$base_instr"
  l2_label="worktree"
  l2_dir="$wt_instr"
  src="layered - $base_label ($base_root) + worktree ($here)"
elif [ -n "$wt_instr" ]; then
  l1_label="worktree"
  l1_dir="$wt_instr"
  src="worktree ($here)"
  [ -n "$base_instr" ] && src="$src - replaces main"
elif [ -n "$base_instr" ]; then
  l1_label="$base_label"
  l1_dir="$base_instr"
  src="$base_label ($base_root)"
  [ "$here" != "$base_root" ] && src="$src via worktree fallback"
elif [ -d "$HOME/.f10/$proj/instructions" ]; then
  l1_label="out-of-tree"
  l1_dir="$HOME/.f10/$proj/instructions"
  src="out-of-tree (~/.f10/$proj)"
  storage="$HOME/.f10/$proj"
fi

# print one layer: its project.md, then the requested overlays, existing files only
print_layer() {
  echo "--- layer: $1 ($2) ---"
  if [ -f "$2/project.md" ]; then
    echo "--- project.md ---"
    cat "$2/project.md"
    echo
  fi
  for s in $steps; do
    if [ -f "$2/$s.md" ]; then
      echo "--- overlay: $s.md ---"
      cat "$2/$s.md"
      echo
    fi
  done
}

# supplied <file.md> - did any layer supply it?
supplied() {
  { [ -n "$l1_dir" ] && [ -f "$l1_dir/$1" ]; } && return 0
  { [ -n "$l2_dir" ] && [ -f "$l2_dir/$1" ]; } && return 0
  return 1
}

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
case " $* " in *" plan "* | *" implement "*) convs+=(gaps) ;; esac
case " $* " in *" capture "* | *" plan "* | *" pr "* | *" push "* | *" deploy "*) convs+=(report) ;; esac
for c in "${convs[@]}"; do
  echo "--- convention: $c ---"
  if [ -f "$root/conventions/$c.md" ]; then
    cat "$root/conventions/$c.md"
  else
    echo "MISSING - $root/conventions/$c.md not found"
  fi
  echo
done

# --- the instructions, in precedence order: later in this bundle wins ---
if [ -z "$l1_dir" ]; then
  echo "--- instructions ---"
  echo "ABSENT - no .f10/instructions/ found. Infer from the signals below; suggest creating"
  echo "$here/.f10/instructions/project.md."
  echo
else
  if [ -n "$l2_dir" ]; then
    echo "layering: $l1_label -> worktree, later wins ($l1_label/project.md ->"
    echo "  $l1_label/<step>.md -> worktree/project.md -> worktree/<step>.md)"
  else
    echo "layering: single layer - $l1_label/project.md -> $l1_label/<step>.md, later wins"
  fi
  echo
  print_layer "$l1_label" "$l1_dir"
  [ -n "$l2_dir" ] && print_layer "$l2_label" "$l2_dir"
  absent=""
  supplied project.md || absent="project.md"
  for s in $steps; do
    supplied "$s.md" || absent="${absent:+$absent, }overlay: $s.md"
  done
  echo "absent: ${absent:-none}"
  echo
fi

echo "--- inferred signals (deterministic; use only where project.md is silent) ---"
origin="$(git remote get-url origin 2>/dev/null || true)"
if [ -n "${origin:-}" ]; then
  echo "remote: $origin"
  rhost="$(host_of "$origin")"
  gh_hosts="${GH_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/gh}/hosts.yml"
  glab_cfg="${GLAB_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/glab-cli}/config.yml"
  if [ -z "$rhost" ]; then
    echo "host: none (local path remote) -> no PR/issue CLI"
  elif knows_host "$gh_hosts" "$rhost"; then
    echo "host: $rhost  -> PR/issue CLI: gh   (gh is logged in to this host)"
  elif knows_host "$glab_cfg" "$rhost"; then
    echo "host: $rhost  -> PR/issue CLI: glab (glab is logged in to this host)"
  else
    case "$rhost" in
      github.com) echo "host: $rhost  -> PR/issue CLI: gh" ;;
      gitlab.com) echo "host: $rhost  -> PR/issue CLI: glab" ;;
      *) echo "host: $rhost  -> PR/issue CLI: unknown (neither gh nor glab is logged in to it) - confirm with user" ;;
    esac
  fi
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
