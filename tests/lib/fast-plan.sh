#!/usr/bin/env bash
# fast-plan.sh: what `task check:fast` runs for the change in this work tree.
#
#   FAST_MAP="<table>" tests/lib/fast-plan.sh steps       # the e2e gate steps to add, one per line
#   RACE_PKGS="<patterns>" tests/lib/fast-plan.sh race    # the race-set packages the change touches
#   tests/lib/fast-plan.sh files                          # the changed files
#
# The change is every file that differs from the merge base with FAST_BASE (default
# origin/main): committed on this branch, staged, unstaged or untracked (not ignored).
#
# steps: each changed file is matched against FAST_MAP (Taskfile.lore.yml), one rule per line,
# "<bash glob> <step> [<step>...]" ("-" for no e2e step), first match wins. A file that no rule
# matches is the safe fallback: every step of the map's "*" rule, which must be the last line
# and names every e2e step. Unsure means run it.
#
# race: the packages of RACE_PKGS (./x/... or ./x) that hold a changed .go file. A change to
# go.mod or go.sum, or a base that cannot be found, selects the whole race set.
set -uo pipefail

mode="${1:-}"
base_ref="${FAST_BASE:-origin/main}"

changed_files() {
  local base
  if ! base=$(git merge-base HEAD "$base_ref" 2>/dev/null); then
    echo "fast-plan: no merge base with $base_ref; treating every file as changed" >&2
    git ls-files
    return
  fi
  {
    git diff --name-only "$base"
    git diff --name-only
    git diff --name-only --cached
    git ls-files --others --exclude-standard
  } | sort -u
}

case "$mode" in
files)
  changed_files
  ;;
steps)
  if [ -z "${FAST_MAP:-}" ]; then
    echo "fast-plan: FAST_MAP is empty" >&2
    exit 2
  fi
  rules=$(printf '%s\n' "$FAST_MAP" | grep -v '^[[:space:]]*\(#\|$\)')
  last=$(printf '%s\n' "$rules" | tail -1)
  if [ "${last%% *}" != "*" ]; then
    echo "fast-plan: the last FAST_MAP rule must be '*' (the fallback that runs every step)" >&2
    exit 2
  fi
  changed_files | while IFS= read -r f; do
    [ -n "$f" ] || continue
    while read -r glob steps; do
      # shellcheck disable=SC2053 # the rule is a glob on purpose
      if [[ "$f" == $glob ]]; then
        for s in $steps; do [ "$s" = "-" ] || echo "$s"; done
        break
      fi
    done <<EOF
$rules
EOF
  done | sort -u
  ;;
race)
  files=$(changed_files)
  if printf '%s\n' "$files" | grep -qxE 'go\.(mod|sum)' || ! git merge-base HEAD "$base_ref" >/dev/null 2>&1; then
    printf '%s\n' ${RACE_PKGS:-}
    exit 0
  fi
  # A deleted file still names its package while the folder is there.
  dirs=$(printf '%s\n' "$files" | grep '\.go$' | while IFS= read -r f; do d=$(dirname "$f"); [ -d "$d" ] && echo "$d"; done | sort -u)
  for pat in ${RACE_PKGS:-}; do
    root="${pat#./}"
    for d in $dirs; do
      case "$pat" in
      */...) [ "$root" = "..." ] && echo "./$d" && continue
        r="${root%/...}"
        if [ "$d" = "$r" ] || [ "${d#"$r"/}" != "$d" ]; then echo "./$d"; fi ;;
      *) [ "$d" = "$root" ] && echo "./$d" ;;
      esac
    done
  done | sort -u
  ;;
*)
  echo "usage: $0 files|steps|race" >&2
  exit 2
  ;;
esac
