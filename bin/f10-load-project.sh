#!/usr/bin/env bash
# f10 · load-project - one-shot deterministic context loading.
#
# The project-specific half of a run's context, in one call (read per conventions/context.md): locate
# .f10/instructions/ (a linked worktree's layered on main's), list each layer, cat the generic
# steps this run executes plus each layer's project.md and overlays in precedence order, and run
# the inference probes (remote host, stack, verify tooling). It never fails; absence is a result.
#
# It concatenates and probes, never interprets, except for the worktree layer's `Layering`
# declaration, which decides what gets concatenated.
#
# Conventions live in f10-load-conventions.sh because they are universal and load once per
# context. Steps are static prose too but selected per run, so they print here rather than cost a
# third call.
#
# Usage:  f10-load-project.sh <step> [<step> ...]   the named generic steps + project.md + overlays
#         f10-load-project.sh --all                 every generic step + project.md + every overlay
#         f10-load-project.sh                       facts, layering, listing and probes, no steps
#   e.g.  f10-load-project.sh capture  |  f10-load-project.sh fetch plan  |  f10-load-project.sh --all
#
# --all exists for /f10:ship, whose steps are the pipeline declared in the project.md this script
# prints, so ship cannot name them before the call. Do not replace it with a fixed step list.

set -uo pipefail
cwd="$(pwd)"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# A string, not an array: bash 3.2 trips over "${empty[*]}" under `set -u`.
all=0
steps=""
for a in "$@"; do
  case "$a" in
    --all) all=1 ;;
    *) steps="${steps:+$steps }$a" ;;
  esac
done

# physical form of a path: `pwd` is logical, git's output is already resolved, and the two are
# compared below
canon() { if [ -d "$1" ]; then (cd "$1" && pwd -P); else printf '%s\n' "$1"; fi; }

# hostname of a git remote url (scp-like, ssh://, https://, userinfo, port). Matching the whole
# url would send a gitlab remote whose path contains "github.com" to gh.
host_of() {
  local h="$1"
  h="${h#*://}"
  h="${h#*@}"
  h="${h%%/*}"
  printf '%s\n' "${h%%:*}"
}

# Is the CLI logged in to this host? Covers GitHub Enterprise and self-hosted GitLab with no
# hostname allowlist. Both files hold OAuth tokens: match and return a status, never cat them.
knows_host() {
  [ -f "$1" ] || return 1
  local esc="${2//./\\.}"
  grep -qE "^[[:space:]]*${esc}:" "$1"
}

# project_prefix <name> - the task id prefix for a project with no declared id format: a short
# name with a digit as is (f10 -> F10), initials across segments (git-undo -> GU), else the first
# three characters (herdr -> HER). One letter or a leading digit prints nothing. Must match
# facts.projectPrefix in cli/; a parity test there holds both to the same names.
project_prefix() {
  local lower seg n=0 first="" initials="" out=""
  lower="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -- '-_.' '   ')"
  for seg in $lower; do
    seg="$(printf '%s' "$seg" | tr -cd 'a-z0-9')"
    [ -n "$seg" ] || continue
    n=$((n + 1))
    [ "$n" = 1 ] && first="$seg"
    initials="$initials${seg:0:1}"
  done
  if [ "$n" = 1 ]; then
    case "$first" in
      *[0-9]*) if [ "${#first}" -le 4 ]; then out="$first"; else out="${first:0:3}"; fi ;;
      *) out="${first:0:3}" ;;
    esac
  elif [ "$n" -gt 1 ]; then
    out="$initials"
  fi
  out="$(printf '%s' "$out" | tr '[:lower:]' '[:upper:]')"
  printf '%s' "$out" | grep -qE '^[A-Z][A-Z0-9]{1,9}$' && printf '%s\n' "$out"
}

# --- locate the instructions layer(s): main's, a linked worktree's, or both ---
# Anchored to `here`, the checkout root, so a run from a subdirectory sees what one from the top
# does. Both in-tree layers load (main's, then the worktree's) unless the worktree replaces main.
# Out-of-tree is a single layer and a last resort.
src="none"
here="$(git rev-parse --show-toplevel 2>/dev/null)"
here="$(canon "${here:-$(pwd -P)}")"
main="$(git worktree list --porcelain 2>/dev/null | awk '/^worktree /{print $2; exit}')"
if [ -n "${main:-}" ]; then main="$(canon "$main")"; fi
proj="$(basename "${main:-$here}")"
storage="$here/.f10"

# the base layer is main's, or simply this checkout's where git cannot name a main worktree
base_label="main"
base_root="${main:-$here}"
[ -n "${main:-}" ] || base_label="checkout"

base_instr=""
wt_instr=""
[ -d "$base_root/.f10/instructions" ] && base_instr="$base_root/.f10/instructions"
[ "$here" != "$base_root" ] && [ -d "$here/.f10/instructions" ] && wt_instr="$here/.f10/instructions"

# frontmatter_key <file> <key>: a top-level key's value from the leading `---` block, or nothing.
# Matches layout.layeringDecl; plain awk, so the loader needs neither the binary nor yq.
frontmatter_key() {
  awk -v key="$2" 'NR == 1 && $0 !~ /^---[[:space:]]*$/ { exit }
    NR > 1 && /^---[[:space:]]*$/ { exit }
    NR > 1 && index($0, key ":") == 1 { sub("^" key ":[[:space:]]*", ""); print; exit }' "$1" 2>/dev/null
}

# `layering: replaces` in the worktree's frontmatter (or, for one release, a `Layering - replaces
# main` prose line) drops main's layer; anything else extends it. Always extending would leak
# main's stack into a worktree that rewrote it.
layering="extends"
if [ -n "$wt_instr" ] && [ -n "$base_instr" ]; then
  decl="$(frontmatter_key "$wt_instr/project.md" layering)"
  if [ -z "$decl" ]; then
    # shellcheck disable=SC2016 # deliberate: the backtick is a literal code mark around the word
    decl="$(grep -im1 -E '^[[:space:]]*[-*#]*[[:space:]]*[*_`]*Layering[*_`]*[[:space:]]*[-:]' \
      "$wt_instr/project.md" 2>/dev/null)"
  fi
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

# the .md basenames a layer holds, requested or not. Without it an unrequested overlay is
# invisible (layer blocks and `absent:` cover only requested names), and so is any
# project-defined step.
list_md() {
  local out=""
  shopt -s nullglob
  for f in "$1"/*.md; do out="${out:+$out, }${f##*/}"; done
  shopt -u nullglob
  printf '%s\n' "${out:-none}"
}

# print one layer: its project.md, then its overlays (all under --all, else the requested ones)
print_layer() {
  echo "--- layer: $1 ($2) ---"
  if [ -f "$2/project.md" ]; then
    echo "--- project.md ---"
    cat "$2/project.md"
    echo
    if [ "$(head -n1 "$2/project.md" | tr -d '[:space:]')" != "---" ]; then
      echo "note: this project.md has no frontmatter; the binary reads its facts from prose for one release (\`f10 init --migrate\`)"
    fi
  fi
  if [ "$all" = 1 ]; then
    shopt -s nullglob
    for f in "$2"/*.md; do
      b="${f##*/}"
      [ "$b" = "project.md" ] && continue # already printed above
      echo "--- overlay: $b ---"
      cat "$f"
      echo
    done
    shopt -u nullglob
  else
    for s in $steps; do
      if [ -f "$2/$s.md" ]; then
        echo "--- overlay: $s.md ---"
        cat "$2/$s.md"
        echo
      fi
    done
  fi
}

supplied() { # supplied <file.md>
  { [ -n "$l1_dir" ] && [ -f "$l1_dir/$1" ]; } && return 0
  { [ -n "$l2_dir" ] && [ -f "$l2_dir/$1" ]; } && return 0
  return 1
}

echo "=== f10 project @ $cwd ==="
echo "checkout root: $here"
if [ "$all" = 1 ]; then
  echo "steps requested: --all (every overlay each layer holds)"
else
  echo "steps requested: ${steps:-none - facts only, no overlays}"
fi
echo "instructions source: $src"
if [ -n "$l1_dir" ]; then
  files="$l1_label: $(list_md "$l1_dir")"
  [ -n "$l2_dir" ] && files="$files | $l2_label: $(list_md "$l2_dir")"
  echo "instructions files: $files"
fi
echo "storage root: $storage  (plans -> $storage/plans/)"
# a config below the root is not a per-directory config - say so rather than pass it over silently
if [ "$(canon "$cwd")" != "$here" ] && [ -d "$cwd/.f10/instructions" ]; then
  echo "note: ignoring nested $cwd/.f10/instructions - loading is anchored to the checkout root"
fi
echo

# --- the generic steps this run executes: the base every overlay extends ---
# Ahead of the instructions, matching precedence. Printed here so no skill spends a round trip
# on `cat steps/<name>.md` before it starts (conventions/latency.md).
print_step() {
  if [ -f "$root/steps/$1.md" ]; then
    echo "--- step: $1 (generic) ---"
    cat "$root/steps/$1.md"
    echo
  else
    echo "--- step: $1 - no generic step: project-defined, so its instructions file IS the step ---"
    echo
  fi
}

if [ "$all" = 1 ] || [ -n "$steps" ]; then
  echo "--- generic steps (plugin prose; the instructions below extend them, later wins) ---"
  echo
  if [ "$all" = 1 ]; then
    # all of them: ship's pipeline is declared in this output, and a free-text ship runs capture,
    # fetch and plan before the pipeline names the rest
    shopt -s nullglob
    for f in "$root"/steps/*.md; do
      b="${f##*/}"
      print_step "${b%.md}"
    done
    shopt -u nullglob
  else
    for s in $steps; do
      print_step "$s"
    done
  fi
fi

# --- the instructions, in precedence order: later in this output wins ---
if [ -z "$l1_dir" ]; then
  echo "--- instructions ---"
  echo "ABSENT - no .f10/instructions/ found. Infer from the signals below; suggest \`f10 init\`,"
  echo "which writes $here/.f10/instructions/project.md from these same probes."
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
# the prefix names plan files and finds a branch's task, so once used it should be declared
# (f10 init writes it) rather than move with a rename
prefix="$(project_prefix "$proj")"
if [ -n "$prefix" ]; then
  echo "task id prefix: $prefix  (derived from the project name \"$proj\"; applies where project.md declares no id format; \`f10 init\` freezes it)"
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
echo "=== end f10 project (nothing was fetched, written, or created) ==="
