#!/usr/bin/env python3
"""paste.py: refresh the generated blocks in docs/**/*.md (all but docs/team, which
tests/team/docs/paste.py owns).

The docs never hold hand-typed command output. Each block of real output sits
between two markers, and this script rewrites what is between them:

  <!-- example: start#init -->          commands and output from
  ...                                     tests/docs/transcripts/start.txt,
  <!-- /example -->                       section "### init" (run-examples.sh)

  <!-- example: start#init only=1,3 -->   only the 1st and 3rd command of it
  <!-- example: start#init lines=6 -->    at most 6 output lines per command (then "…")
  <!-- example: start#init keep=RE -->    only output lines matching RE (no spaces; gaps shown as "…")

  <!-- help: task next -->                `lore task next --help`: usage and flags
  <!-- /help -->

  <!-- errors -->                         every lore error code with its exit status and
  <!-- /errors -->                        description (lore errors list + the exit table
                                          in errcodes/registry.go)

Spec words: a heading slug uses "-" for spaces (### first rule -> start#first-rule).

Usage (from the repo root, after tests/docs/run-examples.sh):

  LORE_BIN=/path/to/lore python3 tests/docs/paste.py          # rewrite in place
  LORE_BIN=/path/to/lore python3 tests/docs/paste.py --check  # exit 1 if a block is stale
  LORE_BIN=/path/to/lore python3 tests/docs/paste.py docs/knowledge   # only pages under these paths

A block whose transcript or section is missing is reported ("error: …") and left as it
is; the other pages are still refreshed, and the run exits 1.

Row ids are shortened (tsk_01a19d82ec2f7ba5968210b0efbee6fe → tsk_01a1…e6fe) and the
noise every fresh clone prints once (cache build, backups) is dropped; nothing else
in the output is changed.
"""

import os
import re
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
DOCS = REPO / "docs"
# docs/team pages are refreshed by tests/team/docs/paste.py from its own transcripts.
SKIP = DOCS / "team"
TRANSCRIPTS = HERE / "transcripts"
REGISTRY = REPO / "saas" / "pkg" / "aicoder" / "errcodes" / "registry.go"

# Lines every first command in a fresh clone prints; they say nothing about the example.
NOISE = re.compile(
    r"^lore sync: (built \.lore/lore\.db|adopted \.lore/data|backup of lore\.db|configured git for lore data)"
)
# Row ids are 32 hex digits after a table prefix; shortened so examples stay readable.
ROW_ID = re.compile(r"\b([a-z]{2,4})_([0-9a-f]{32})\b")
# Long hashes in output (commit ids, blob ids) beyond what a reader needs.
LONG_SHA = re.compile(r"\b([0-9a-f]{12})[0-9a-f]{28}\b")
# A transcript command line: "who (dir)$ cmd" or "who$ cmd".
PROMPT = re.compile(r"^([a-z0-9-]+)(?: \(([^)]*)\))?\$ (.*)$")
BLOCK = re.compile(r"(<!-- (example|help): ([^>]*?) -->\n)(.*?)(<!-- /\2 -->)", re.S)
ERRORS = re.compile(r"(<!-- errors -->\n)(.*?)(<!-- /errors -->)", re.S)
ELLIPSIS = "…"


def short_id(m: re.Match) -> str:
    return f"{m.group(1)}_{m.group(2)[:4]}{ELLIPSIS}{m.group(2)[-4:]}"


def tidy(line: str) -> str:
    line = ROW_ID.sub(short_id, line)
    return LONG_SHA.sub(lambda m: m.group(1), line)


def read_section(name: str, heading: str):
    """Commands of one '### heading' section: [(who, dir, cmd, [output lines])]."""
    path = TRANSCRIPTS / f"{name}.txt"
    if not path.exists():
        raise SystemExit(f"paste: no transcript {path} (run tests/docs/run-examples.sh {name})")
    cmds, inside = [], False
    for raw in path.read_text().splitlines():
        if raw.startswith("### "):
            inside = raw[4:].strip() == heading
            continue
        if not inside:
            continue
        m = PROMPT.match(raw)
        if m:
            cmds.append((m.group(1), m.group(2) or "", m.group(3), []))
        elif cmds and raw.strip() != "":
            cmds[-1][3].append(raw)
    if not cmds:
        raise SystemExit(f"paste: section '### {heading}' of {name}.txt is empty or missing")
    return cmds


def render_example(spec: str) -> str:
    parts = spec.split()
    name, _, heading_slug = parts[0].partition("#")
    opts = dict(p.split("=", 1) for p in parts[1:])
    heading = heading_slug.replace("-", " ")
    cmds = read_section(name, heading)
    if "only" in opts:
        picks = [int(i) for i in opts["only"].split(",")]
        cmds = [cmds[i - 1] for i in picks]
    max_lines = int(opts["lines"]) if "lines" in opts else None
    out, last_who = [], None
    for who, where, cmd, lines in cmds:
        place = where.removeprefix("work/").removeprefix("work") or "~"
        label = f"# {who}, in {place}"
        if label != last_who:
            if out:
                out.append("")
            out.append(label)
            last_who = label
        out.append(f"$ {tidy(cmd)}")
        kept = [tidy(l) for l in lines if not NOISE.match(l)]
        if "keep" in opts:
            pattern, picked = re.compile(opts["keep"]), []
            for l in kept:
                if pattern.search(l):
                    picked.append(l)
                elif not picked or picked[-1] != "  " + ELLIPSIS:
                    picked.append("  " + ELLIPSIS)
            kept = picked
        if max_lines is not None and len(kept) > max_lines:
            kept = kept[:max_lines] + [ELLIPSIS]
        out.extend(kept)
    return "```console\n" + "\n".join(out) + "\n```\n"


def lore(*args: str) -> str:
    binary = os.environ.get("LORE_BIN", "lore")
    env = {"PATH": os.environ.get("PATH", ""), "HOME": os.environ.get("HOME", "/tmp"), "NO_COLOR": "1"}
    return subprocess.run([binary, *args], capture_output=True, text=True, env=env, check=True).stdout


def render_help(spec: str) -> str:
    words = spec.split()
    text = lore(*words, "--help")
    # The first paragraph of the help text, joined into one line (help text is
    # wrapped at ~80 columns; markdown prose must never be).
    first_para = text.strip().split("\n\n", 1)[0]
    short = " ".join(l.strip() for l in first_para.splitlines()) if not first_para.startswith("Usage:") else ""
    keep, skip = [], False
    for line in text.splitlines():
        if line.startswith("Usage:"):
            skip = False
        if line.startswith("Global Flags:"):
            skip = True
        if skip or line.startswith("Use \"lore"):
            continue
        if re.match(r"^\s+-h, --help", line):
            continue
        # "[flags]" / "[command]" on a usage line are cobra boilerplate, not an argument; dropped so the
        # usage line is itself a valid invocation (TestDocsMatchCLI checks it).
        keep.append(line.rstrip().replace(" [flags]", "").replace(" [command]", " <command>"))
    start = next(i for i, l in enumerate(keep) if l.startswith("Usage:"))
    body = "\n".join(keep[start:]).strip()
    body = re.sub(r"\n{3,}", "\n\n", body)
    lead = f"{short}\n\n" if short else ""
    return f"{lead}```text\n{body}\n```\n"


def exit_codes() -> dict:
    src = REGISTRY.read_text()
    names = dict(re.findall(r"^\s+(\w+)\s+Code = \"(E_[A-Z_]+)\"", src, re.M))
    table = re.search(r"var exitCodes = map\[Code\]int\{(.*?)\n\}", src, re.S).group(1)
    return {names[k]: int(v) for k, v in re.findall(r"(\w+):\s+(\d+),", table)}


def render_errors() -> str:
    codes = exit_codes()
    listing = lore("errors", "list")
    rows = ["| Code | Exit status | Meaning |", "|---|---|---|"]
    for line in listing.splitlines():
        code, _, desc = line.partition(" ")
        if code.startswith("E_"):
            rows.append(f"| `{code}` | {codes.get(code, 1)} | {desc.strip()} |")
    return "\n".join(rows) + "\n"


class BlockError(Exception):
    """A marked block that cannot be rendered (missing transcript or section)."""


def render_block(m: re.Match) -> str:
    try:
        body = render_example(m.group(3)) if m.group(2) == "example" else render_help(m.group(3))
    except SystemExit as e:
        raise BlockError(str(e)) from None
    except (IndexError, ValueError, KeyError, StopIteration) as e:
        # A bad option (only=9 on a 3-command section, a malformed key=value).
        raise BlockError(f"bad block <!-- {m.group(2)}: {m.group(3)} -->: {type(e).__name__} {e}") from None
    return m.group(1) + body + m.group(5)


def main() -> int:
    args = sys.argv[1:]
    check = "--check" in args
    roots = [(Path.cwd() / a).resolve() for a in args if not a.startswith("--")]
    stale, errors = [], []
    for page in sorted(DOCS.rglob("*.md")):
        if SKIP in page.parents:
            continue
        if roots and not any(page == r or r in page.parents for r in roots):
            continue
        text = page.read_text()
        try:
            new = BLOCK.sub(render_block, text)
        except BlockError as e:
            errors.append(f"error: {page.relative_to(REPO)}: {e}")
            continue
        new = ERRORS.sub(lambda m: m.group(1) + render_errors() + m.group(3), new)
        if new != text:
            stale.append(page.relative_to(REPO))
            if not check:
                page.write_text(new)
    for p in stale:
        print(f"{'stale' if check else 'updated'}: {p}")
    for e in errors:
        print(e, file=sys.stderr)
    return 1 if (errors or (check and stale)) else 0


if __name__ == "__main__":
    sys.exit(main())
