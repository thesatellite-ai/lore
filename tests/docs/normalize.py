#!/usr/bin/env python3
"""normalize.py TRANSCRIPT: make one transcript identical on every run.

Row ids, claim tokens, agent session ids and git object ids are random (or depend on
content that contains them), so each run of run-examples.sh would print new ones and
every docs page would change. This rewrites each distinct value, in order of first
appearance in the file, to a stable stand-in of the same shape: the 1st task id seen
becomes the same tsk_… on every run, the 2nd the next one, and so on. Equal values
stay equal, so an id printed by one command still matches the id the next command uses.
Times are already stable: run-examples.sh runs every section on the test clock.

Hex ids that are the same object printed at different lengths (a commit as 7, 8 and 40
characters; a 64-character fingerprint and its 12-character prefix) get one stand-in,
cut to each length, so they still read as one object. A run of digits followed by
" bytes" is a size, not an id, and is left alone.
"""

import hashlib
import re
import sys

# One alternation, so a value is matched once by its most specific shape:
#   row ids (tsk_ + 32 hex), claim tokens (ct_ + 5 hex, cut by lib.sh),
#   agent sessions (s- + 8 hex), git object ids (7–40 hex with a digit and a letter).
TOKEN = re.compile(
    r"\b(?P<row>[a-z]{2,4})_(?P<rowhex>[0-9a-f]{32})\b"
    r"|\bct_(?P<ct>[0-9a-f]{5})"
    r"|\bs-(?P<sess>[0-9a-f]{8})\b"
    # git object ids and fingerprints: 7–64 hex digits standing alone between spaces,
    # brackets, commas or a tab ("[branch sha]" too); dates, times and paths never match.
    r"|(?:(?<=^)|(?<=[\s(,\[]))(?P<sha>[0-9a-f]{7,64})(?=$|[\s),;:…\]])"
)
# Wall-clock times lore records with nanoseconds (created_at / updated_at of a row):
# the test clock does not reach them, so each distinct one gets a stable stand-in.
NANOTIME = re.compile(r"\b2\d{3}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{6,9}Z")
# Rows of `lore mission status` with the same state: lore orders ties by id, which is
# random, so a run of such lines is sorted by its text after the id.
STATUS_ROW = re.compile(r"^  (done|ready|waiting|claimed|expired|blocked|gated)\s+\S+\s+(.*)$")
# `lore why-context` (created_at) and `lore memory show` after `memory invalidate`
# (valid-until) print wall-clock times: those writes do not read the test clock.
# Each distinct one gets a stable stand-in.
VALID_UNTIL = re.compile(r"(?m)^(  valid-until: )2\d{3}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
RENDER_TIME = re.compile(r"(?m)^(created_at:\s+)2\d{3}-\d\d-\d\d \d\d:\d\d:\d\dZ$")
# `lore render` fingerprints its own output (a 16-hex canary, a sha256 in why-context);
# the output holds random row ids, so both change every run: stable stand-ins.
CANARY = re.compile(r"\brnd_([0-9a-f]{16})\b")
SHA256 = re.compile(r"\b[0-9a-f]{64}\b")
# `lore audit log` rows start with a wall-clock time (the audit log does not read the
# test clock); `lore restore` / `lore repair` name the set-aside database file with one.
AUDIT_TIME = re.compile(r"(?m)^2\d{3}-\d\d-\d\dT\d\d:\d\d:\d\dZ(?=  [a-z_]+\.)")
BROKEN_DB = re.compile(r"lore\.db\.broken\.\d{8}-\d{6}")
# The wall-clock stamp in the name a damaged cache is moved aside under (lore-cache.db.corrupt-<UTC>).
CORRUPT_CACHE = re.compile(r"lore-cache\.db\.corrupt-\d{8}T\d{6}Z")
# Digits right before " bytes" are a size (`lore backup` prints "(1441792 bytes)").
SIZE_AFTER = re.compile(r" bytes\b")
# `lore search` hits with the same score: lore orders ties by row id, which is random,
# so a run of hit lines is sorted by score (high first), then by text after the id.
SEARCH_HIT = re.compile(r"^\[(?P<kind>[a-z ]+)\] \S+ (?P<text>.*)\(score=(?P<score>[0-9.]+)\)$")
# Wall-clock stamps lore writes outside the test clock (cache backups before a sync).
BACKUP = re.compile(r"\d{8}-\d{6}\.\d+-pre-sync")
# `--trace` / LORE_TRACE=1 prints how long each git command took ("trace: git … (3ms, ok)");
# the time differs on every run, so it becomes "<time>".
TRACE_TIME = re.compile(r"(?m)^(trace: .* \()[0-9.]+(?:ns|µs|ms|s|m[0-9.]+s)(, [^)]*\))$")


# Each person's HOME in the example world. lib.sh already prints it as "~"; this is the
# safety net for any path that slipped past it (written without the slash after "home" so
# the repo-wide local-path grep stays empty). The docs must never show a machine's home
# folder, so main() fails when one survives.
HOME_PATH = re.compile(r"(?<![\w.~])/(?:home|Users)(?=/)/[A-Za-z0-9._-]+")
LOCAL_PATH = re.compile(r"/(?:home|Users|Volumes|space)(?=/)")


def stand_in(kind: str, n: int, length: int) -> str:
    return hashlib.sha256(f"{kind}:{n}".encode()).hexdigest()[:length]


def hex_stand_ins(text: str) -> dict[str, str]:
    """One stand-in per object: every hex id that is a prefix of a longer one shares the
    longest one's stand-in, cut to its own length. Sizes ("N bytes") are skipped.
    Order of first appearance keeps it stable."""
    found: list[str] = []
    for m in TOKEN.finditer(text):
        v = m.group("sha")
        if v and v.isdigit() and SIZE_AFTER.match(text, m.end()):
            continue
        if v and v not in found:
            found.append(v)
    for m in SHA256.finditer(text):
        if m.group(0) not in found:
            found.append(m.group(0))
    # canonical = the longest id this value is a prefix of (or itself).
    def canonical(v: str) -> str:
        return max((w for w in found if w.startswith(v)), key=len)
    groups: dict[str, str] = {}
    out: dict[str, str] = {}
    for v in found:
        c = canonical(v)
        if c not in groups:
            groups[c] = stand_in("sha", len(groups) + 1, 64)
        out[v] = groups[c][: len(v)]
    return out


def sort_hits(lines: list[str]) -> list[str]:
    out, run = [], []

    def key(l: str):
        m = SEARCH_HIT.match(l)
        return (-float(m.group("score")), m.group("kind"), m.group("text"))

    for line in lines + [""]:
        if SEARCH_HIT.match(line):
            run.append(line)
            continue
        out.extend(sorted(run, key=key))
        run.clear()
        out.append(line)
    return out[:-1]


def sort_ties(lines: list[str]) -> list[str]:
    out, run = [], []

    def flush() -> None:
        run.sort(key=lambda l: (STATUS_ROW.match(l).group(1), STATUS_ROW.match(l).group(2)))
        out.extend(run)
        run.clear()

    for line in lines:
        if STATUS_ROW.match(line):
            run.append(line)
        else:
            flush()
            out.append(line)
    flush()
    return out


def main(path: str) -> None:
    seen: dict[tuple[str, str], str] = {}
    counts: dict[str, int] = {}

    def repl(m: re.Match) -> str:
        if m.group("row"):
            kind, value, prefix = "row", m.group("rowhex"), m.group("row") + "_"
        elif m.group("ct"):
            kind, value, prefix = "ct", m.group("ct"), "ct_"
        elif m.group("sess"):
            kind, value, prefix = "sess", m.group("sess"), "s-"
        else:
            kind, value, prefix = "sha", m.group("sha"), ""
        if kind == "sha":
            return sha_map.get(value, value)  # sizes are absent from the map: unchanged
        key = (kind, value)
        if key not in seen:
            counts[kind] = counts.get(kind, 0) + 1
            seen[key] = stand_in(kind, counts[kind], len(value))
        return prefix + seen[key]

    text = open(path, encoding="utf-8").read()
    text = "\n".join(sort_hits(sort_ties(text.split("\n"))))
    sha_map = hex_stand_ins(text)
    text = TOKEN.sub(lambda m: repl(m), text)
    times: dict[str, str] = {}
    text = NANOTIME.sub(lambda m: times.setdefault(m.group(0), f"2026-11-02T10:00:00.{len(times) + 1:09d}Z"), text)
    rtimes: dict[str, str] = {}
    text = RENDER_TIME.sub(lambda m: m.group(1) + rtimes.setdefault(m.group(0), f"2026-11-02 10:00:{len(rtimes):02d}Z"), text)
    vtimes: dict[str, str] = {}
    text = VALID_UNTIL.sub(lambda m: m.group(1) + vtimes.setdefault(m.group(0), f"2026-11-02T10:30:{len(vtimes):02d}Z"), text)
    fp: dict[str, str] = {}
    text = CANARY.sub(lambda m: "rnd_" + fp.setdefault(m.group(0), stand_in("canary", len(fp) + 1, 16)), text)
    # 64-hex values glued to other text (key=…): same mapping as the free-standing ones.
    text = SHA256.sub(lambda m: sha_map.get(m.group(0), m.group(0)), text)
    # One fixed stand-in: how many distinct seconds a run spans depends on the machine.
    text = AUDIT_TIME.sub("2026-11-02T10:00:00Z", text)
    broken: dict[str, str] = {}
    text = BROKEN_DB.sub(lambda m: broken.setdefault(m.group(0), f"lore.db.broken.20261102-1000{len(broken):02d}"), text)
    text = CORRUPT_CACHE.sub("lore-cache.db.corrupt-20261102T100000Z", text)
    text = BACKUP.sub("20261102-100000.000000000-pre-sync", text)
    text = TRACE_TIME.sub(lambda m: m.group(1) + "<time>" + m.group(2), text)
    text = HOME_PATH.sub("~", text)
    left = LOCAL_PATH.search(text)
    if left:
        line = text.count("\n", 0, left.start()) + 1
        sys.exit(f"normalize.py: {path}:{line}: a local path survived: {text.splitlines()[line - 1]!r}")
    open(path, "w", encoding="utf-8").write(text)


if __name__ == "__main__":
    main(sys.argv[1])
