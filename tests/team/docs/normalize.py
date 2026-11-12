#!/usr/bin/env python3
"""normalize.py TRANSCRIPT: make one transcript identical on every run.

Row ids, claim tokens, agent session ids and git object ids are random (or depend on
content that contains them), so each run of run-examples.sh would print new ones and
every docs page would change. This rewrites each distinct value, in order of first
appearance in the file, to a stable stand-in of the same shape: the 1st task id seen
becomes the same tsk_… on every run, the 2nd the next one, and so on. Equal values
stay equal, so an id printed by one command still matches the id the next command uses.
Times are already stable: run-examples.sh runs every section on the test clock.

Machine ids (mch_ + 64 hex) depend on the host that runs the examples, so they get
stand-ins too, and so do their short forms (`mch_fac0df3a`, the first 8 hex): a short
id on the same transcript as its full id gets the full id's stand-in cut to 8, the same
way tests/docs/normalize.py does it.
"""

import hashlib
import re
import sys

# One alternation, so a value is matched once by its most specific shape:
#   row ids (tsk_ + 32 hex), claim tokens (ct_ + 5 hex, cut by lib.sh),
#   agent sessions (s- + 8 hex), anonymous ids (anon: + 12 hex), git object ids (7–40 hex with a digit and a letter).
TOKEN = re.compile(
    r"\b(?P<row>[a-z]{2,4})_(?P<rowhex>[0-9a-f]{32}|[0-9a-f]{64})\b"
    r"|\bct_(?P<ct>[0-9a-f]{5})"
    r"|\bs-(?P<sess>[0-9a-f]{8})\b"
    # anonymous-mode actor ids (anon: + 12 hex, random per `lore identity anonymize on`).
    r"|\banon:(?P<anon>[0-9a-f]{12})\b"
    # git object ids: any 7–40 hex digits (a short id can be all digits) standing alone
    # between spaces, brackets, commas or a tab ("[branch sha]" too), or as a whole JSON
    # string (the provenance "git": {"commit": "…"} of a row file: the commit's tree holds
    # random row ids, so its sha differs on every run); dates, times and paths never match.
    # A backup ref (refs/lore/backup/compact-<epoch>-<sha12>) ends in one after "<digit>-".
    r'|(?:(?<=^)|(?<=[\s(,\["])|(?<=\d-))(?P<sha>[0-9a-f]{7,40})(?=$|[\s),;:…\]"])'
)
# A row or machine id printed short: its first 8 hex (`mch_fac0df3a`) or an ellipsis and
# its last 4 (`sal_…4e57`). Mapped after the full ids, so a short id on the same
# transcript as its full id gets the full id's stand-in, cut the same way.
SHORT_ID = re.compile(r"\b(?P<p>[a-z]{2,4})_(?:(?P<head>[0-9a-f]{8})|…(?P<tail>[0-9a-f]{4}))\b")
# Wall-clock times lore records with nanoseconds (created_at / updated_at of a row):
# the test clock does not reach them, so each distinct one gets a stable stand-in.
NANOTIME = re.compile(r"\b2\d{3}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{6,9}Z")
# Rows of `lore mission status` with the same state: lore orders ties by id, which is
# random, so a run of such lines is sorted by its text after the id.
STATUS_ROW = re.compile(r"^  (done|ready|waiting|claimed|expired|blocked|gated)\s+\S+\s+(.*)$")
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
            # 64 hex is a machine id (mch_…): its own counter, so row stand-ins keep their order.
            kind = "row" if len(m.group("rowhex")) == 32 else "row64"
            value, prefix = m.group("rowhex"), m.group("row") + "_"
        elif m.group("ct"):
            kind, value, prefix = "ct", m.group("ct"), "ct_"
        elif m.group("sess"):
            kind, value, prefix = "sess", m.group("sess"), "s-"
        elif m.group("anon"):
            kind, value, prefix = "anon", m.group("anon"), "anon:"
        else:
            kind, value, prefix = "sha", m.group("sha"), ""
        key = (kind, value)
        if key not in seen:
            # A short sha is a prefix of a longer one seen earlier (or later): keep them related.
            if kind == "sha":
                for (k, v), new in list(seen.items()):
                    if k == "sha" and (v.startswith(value) or value.startswith(v)):
                        full = new if len(new) >= len(value) else None
                        if full:
                            seen[key] = full[: len(value)]
                            break
            if key not in seen:
                counts[kind] = counts.get(kind, 0) + 1
                seen[key] = stand_in(kind, counts[kind], len(value) if kind != "sha" else 40)[: len(value)]
        return prefix + seen[key]

    def short_repl(m: re.Match) -> str:
        # The full id this short form names: same prefix, matching head or tail. When none
        # (or more than one) is on the transcript, the short form gets a stand-in of its own.
        p, head, tail = m.group("p"), m.group("head"), m.group("tail")
        part = head or tail
        fulls = [(k, v) for (k, v) in seen if k in ("row", "row64") and full_prefix.get(v) == p
                 and (v.startswith(part) if head else v.endswith(part))]
        if len(fulls) == 1:
            full = seen[fulls[0]]
        else:
            key = ("short", p + "_" + ("" if head else "…") + part)
            if key not in seen:
                counts["short"] = counts.get("short", 0) + 1
                seen[key] = stand_in("short", counts["short"], 64)
            full = seen[key]
        return f"{p}_{full[:8]}" if head else f"{p}_…{full[-4:]}"

    text = open(path, encoding="utf-8").read()
    # The prefix (tsk, act, mch, …) each full id was printed with, for short_repl.
    full_prefix = {m.group("rowhex"): m.group("row") for m in TOKEN.finditer(text) if m.group("row")}
    text = TOKEN.sub(lambda m: repl(m), "\n".join(sort_ties(text.split("\n"))))
    text = SHORT_ID.sub(lambda m: short_repl(m), text)
    times: dict[str, str] = {}
    text = NANOTIME.sub(lambda m: times.setdefault(m.group(0), f"2026-11-02T10:00:00.{len(times) + 1:09d}Z"), text)
    text = BACKUP.sub("20261102-100000.000000000-pre-sync", text)
    # A code repo's cache under the lore home: <repo folder>-<12 hex of the repo path>.db (the path is
    # under a random temp folder), and the wall-clock stamp of one moved aside.
    text = re.sub(r"(cache/repo/[A-Za-z0-9._-]+?)-[0-9a-f]{12}(?=\.db)", lambda m: m.group(1) + "-<hash>", text)
    text = re.sub(r"\.db\.corrupt-\d{8}T\d{6}Z", ".db.corrupt-20261102T100000Z", text)
    text = TRACE_TIME.sub(lambda m: m.group(1) + "<time>" + m.group(2), text)
    text = HOME_PATH.sub("~", text)
    left = LOCAL_PATH.search(text)
    if left:
        line = text.count("\n", 0, left.start()) + 1
        sys.exit(f"normalize.py: {path}:{line}: a local path survived: {text.splitlines()[line - 1]!r}")
    open(path, "w", encoding="utf-8").write(text)


if __name__ == "__main__":
    main(sys.argv[1])
