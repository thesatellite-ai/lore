#!/usr/bin/env bash
# parallel-sections.sh: run the sections of a docs example runner (tests/docs/run-examples.sh,
# tests/team/docs/run-examples.sh) at the same time, each as its own run of the runner.
#
# Source it from a runner, then:
#
#   parallel_sections JOBS LOGDIR TRANSCRIPTS RUNNER SECTION...
#
# Every SECTION runs as `RUNNER SECTION` (a separate process: its own scratch folder under
# /tmp, its own world, HOMEs and transcript, so no two sections share a file), JOBS at a time.
# The sections whose transcript in TRANSCRIPTS is largest start first: a transcript's size
# follows how many commands its section runs, so the longest sections never start last and
# leave the other job slots idle at the end. Each section's output goes to LOGDIR/<section>.log
# and is printed in the order the sections were given once all have ended, so the log reads
# the same as a serial run. Returns 1 when any section failed, naming each.
#
# Why: run serially, the 20 + 21 sections took 460 s wall on an idle machine while using one
# core; each section only waits on its own lore and git processes.

# section_transcript TRANSCRIPTS SECTION: the transcript file of SECTION (team section
# functions are named with _, their transcripts with -).
section_transcript() {
  printf '%s/%s.txt' "$1" "${2//_/-}"
}

parallel_sections() {
  local jobs="$1" logs="$2" transcripts="$3" runner="$4"
  shift 4
  mkdir -p "$logs"
  local s size
  # size<TAB>section, largest first; a section without a transcript yet (new) counts as 0.
  for s in "$@"; do
    size=$(wc -c <"$(section_transcript "$transcripts" "$s")" 2>/dev/null || echo 0)
    printf '%s\t%s\n' "$((size + 0))" "$s"
  done | sort -t "$(printf '\t')" -k1,1nr | cut -f2 |
    xargs -P "$jobs" -I{} bash -c 'start=$SECONDS; "$1" "$2" >"$3/$2.log" 2>&1; rc=$?; printf "%s %s\n" "$rc" "$((SECONDS - start))" >"$3/$2.rc"' _ "$runner" {} "$logs"
  local failed=() rc secs
  for s in "$@"; do
    rc=1 secs=?
    [ -f "$logs/$s.rc" ] && read -r rc secs <"$logs/$s.rc"
    echo "──── section $s: exit $rc, ${secs}s"
    cat "$logs/$s.log" 2>/dev/null
    [ "$rc" = 0 ] || failed+=("$s")
  done
  if [ "${#failed[@]}" -gt 0 ]; then
    echo "run-examples: ${#failed[@]} section(s) failed: ${failed[*]}" >&2
    return 1
  fi
  return 0
}
