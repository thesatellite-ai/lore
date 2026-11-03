#!/usr/bin/env python3
"""paste.py: refresh the generated blocks in docs/team/**/*.md.

The docs never hold hand-typed command output. Each block of real output sits
between two markers, and this script rewrites what is between them:

  <!-- example: agency#link -->          commands and output from
  ...                                     tests/team/docs/transcripts/agency.txt,
  <!-- /example -->                       section "### link" (run-examples.sh)

  <!-- example: agency#link only=1,3 -->  only the 1st and 3rd command of it
  <!-- example: agency#link lines=6 -->   at most 6 output lines per command (then "…")
  <!-- example: agency#link keep=RE -->   only output lines matching RE (no spaces; gaps shown as "…")

  <!-- help: task next -->                `lore task next --help`: usage and flags
  <!-- /help -->

  <!-- errors -->                         every lore-for-teams error code with its exit
  <!-- /errors -->                        status and description (lore errors list +
                                          the exit table in errcodes/registry.go)

Usage (from the repo root, after tests/team/docs/run-examples.sh):

  LORE_BIN=/path/to/lore python3 tests/team/docs/paste.py          # rewrite in place
  LORE_BIN=/path/to/lore python3 tests/team/docs/paste.py --check  # exit 1 if a block is stale

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
REPO = HERE.parents[2]
DOCS = REPO / "docs" / "team"
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
TEAM_CODES_START = "Lore for teams"
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
        raise SystemExit(f"paste: no transcript {path} (run tests/team/docs/run-examples.sh {name})")
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
    # A command whose only flag is -h (dropped above) would print a bare "Flags:" heading.
    body = re.sub(r"\n*^Flags:[ \t]*$(?=\n*\Z|\n\n)", "", body, flags=re.M).strip()
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
    src = REGISTRY.read_text()
    team_block = src[src.index(TEAM_CODES_START):]
    team = re.findall(r"Code = \"(E_[A-Z_]+)\"", team_block.split("\n)", 1)[0])
    rows = ["| Code | Exit status | Meaning |", "|---|---|---|"]
    for line in listing.splitlines():
        code, _, desc = line.partition(" ")
        if code in team:
            rows.append(f"| `{code}` | {codes.get(code, 1)} | {desc.strip()} |")
    return "\n".join(rows) + "\n"


def main() -> int:
    check = "--check" in sys.argv[1:]
    stale = []
    for page in sorted(DOCS.rglob("*.md")):
        text = page.read_text()
        new = BLOCK.sub(lambda m: m.group(1) + (render_example(m.group(3)) if m.group(2) == "example" else render_help(m.group(3))) + m.group(5), text)
        new = ERRORS.sub(lambda m: m.group(1) + render_errors() + m.group(3), new)
        if new != text:
            stale.append(page.relative_to(REPO))
            if not check:
                page.write_text(new)
    for p in stale:
        print(f"{'stale' if check else 'updated'}: {p}")
    return 1 if (check and stale) else 0


if __name__ == "__main__":
    sys.exit(main())
