#!/usr/bin/env python3
"""commands_index.py: write the command index in docs/reference/commands.md from the
real binary, so the page can never list a command that does not exist or miss one
that does.

It walks `lore --help` and every `lore <command> --help` below it, takes each
command's one-line help, and adds the guide link that docs/reference/features.md
gives for that command (or for its parent command). The result replaces what sits
between these two markers in docs/reference/commands.md:

  <!-- commands -->
  <!-- /commands -->

Usage (from the repo root):

  LORE_BIN=/path/to/lore python3 tests/docs/commands_index.py          # rewrite the block
  LORE_BIN=/path/to/lore python3 tests/docs/commands_index.py --check  # exit 1 if it is stale

Nothing else in the page is touched. `help` and the four `completion <shell>`
commands are folded into their parent, the way cobra lists them.
"""

import os
import re
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
PAGE = REPO / "docs" / "reference" / "commands.md"
FEATURES = REPO / "docs" / "reference" / "features.md"
BLOCK = re.compile(r"(<!-- commands -->\n)(.*?)(<!-- /commands -->)", re.S)

# A few one-line help texts use words the public docs avoid (vendor and model names);
# the page shows these plain replacements instead. Each entry is reported so the help
# text itself gets fixed; once it is, the entry can go (the generator says when an
# override is no longer used).
SHORT_OVERRIDES = {
    "task triage": "Show proposed tasks (suggested, not yet committed)",
    "why-context": "Show the last rendered context (what the coding tool saw)",
    "skill compile": "Compress the canonical bundle into a smaller .md with a model",
}
# Optional word list (one regular expression per line) that must never reach the page
# from help text, checked after the overrides. It is kept outside the repo; point
# DOCS_WORDLIST at it to turn the check on.
WORDLIST = os.environ.get("DOCS_WORDLIST", "")


def banned():
    if not WORDLIST:
        return None
    words = [w.strip() for w in Path(WORDLIST).read_text().splitlines() if w.strip()]
    return re.compile("|".join(f"(?:{w})" for w in words), re.I) if words else None
# Commands the old binary still registers while it is being emptied, but that the
# 0.1.18 design does not list (CLI_REFERENCE.md has no row for them), so the release
# binary (cmd/lore) does not have them either. They are left out with their whole
# subtree, so the page is the same whichever binary writes it. Remove an entry once
# the old binary stops registering that command.
DROPPED = {
    "backup", "bench", "errors", "finding", "learn", "learn-from", "mount-alias",
    "plugin", "querylog", "renderhistory", "repair", "restore", "tables", "team",
    "directive remove", "identity anonymize", "org unlink",
    "run list", "run replay", "run show", "run step",
    "sync dupes", "sync export", "sync install-git", "sync uninstall-action",
}
# Children cobra adds by itself; listed under their parent, not as rows of their own.
SKIP_CHILDREN = {"help"}
FOLD_CHILDREN = {"completion"}


def lore_help(path):
    binary = os.environ.get("LORE_BIN", "lore")
    env = {"PATH": os.environ.get("PATH", ""), "HOME": os.environ.get("HOME", "/tmp"), "NO_COLOR": "1"}
    return subprocess.run([binary, *path, "--help"], capture_output=True, text=True, env=env, check=True, stdin=subprocess.DEVNULL).stdout


def subcommands(text):
    """(name, short) pairs of the 'Available Commands:' block."""
    out, inside = [], False
    for line in text.splitlines():
        if line.rstrip() == "Available Commands:":
            inside = True
            continue
        if inside:
            if not line.strip():
                break
            m = re.match(r"\s+(\S+)\s+(.*)", line)
            if m:
                out.append((m.group(1), m.group(2).strip()))
    return out


def walk():
    """Every command as (path, short, has_children), depth first in help order."""
    rows = []

    def visit(path, short):
        kids = [] if (path and path[-1] in FOLD_CHILDREN) else [k for k in subcommands(lore_help(path)) if k[0] not in SKIP_CHILDREN and " ".join(path + [k[0]]) not in DROPPED]
        if path:
            rows.append((path, short, bool(kids)))
        for name, s in kids:
            visit(path + [name], s)

    visit([], "")
    return rows


def guide_map():
    """command path ("task add") -> guide cell of features.md."""
    out = {}
    for line in FEATURES.read_text().splitlines():
        if not line.startswith("| `lore "):
            continue
        cells = [c.strip() for c in line.strip().strip("|").split(" | ")]
        if len(cells) < 4:
            continue
        name, guide = cells[0], cells[3]
        if guide in ("", "—"):
            continue
        # "`lore rule` add · list" -> rule add, rule list; "`lore org link` / `lore org unlink`" -> both.
        bases = re.findall(r"`lore ([^`]+)`", name)
        tail = name.rsplit("`", 1)[-1]
        words = [w.strip() for w in tail.split("·") if w.strip()]
        for base in bases:
            out.setdefault(base, guide)
        if words and bases:
            for w in words:
                out.setdefault(f"{bases[-1]} {w}", guide)
    return out


def guide_text(guides, path):
    """The guide cell for path: its own row in features.md, else its nearest parent's."""
    probe = path[:]
    while probe:
        g = guides.get(" ".join(probe))
        if g:
            return g
        probe = probe[:-1]
    return "—"


def cell(s):
    return s.replace("|", "\\|")


def render():
    rows = walk()
    guides = guide_map()
    deny = banned()
    used = set()
    tops = [r for r in rows if len(r[0]) == 1]
    lines = ["Jump to: " + " · ".join(f"[{p[0]}](#lore-{p[0]})" for p, _, _ in tops), ""]
    for top, top_short, _ in tops:
        lines += [f"### lore {top[0]}", ""]
        lines += ["| Command | What it does | Guide |", "|---|---|---|"]
        for path, short, kids in rows:
            if path[0] != top[0]:
                continue
            key = " ".join(path)
            if key in SHORT_OVERRIDES:
                short = SHORT_OVERRIDES[key]
                used.add(key)
            if deny and deny.search(short + " " + guide_text(guides, path)):
                raise SystemExit(f"commands_index: `lore {key}` matches DOCS_WORDLIST; add its help text to SHORT_OVERRIDES or fix the guide link: {short}")
            guide = guide_text(guides, path)
            name = f"`lore {key}`" + (" …" if kids else "")
            lines.append(f"| {name} | {cell(short)} | {guide} |")
        lines.append("")
    for key in sorted(set(SHORT_OVERRIDES) - used):
        print(f"commands_index: override for `lore {key}` is no longer used; remove it", file=sys.stderr)
    leaves = sum(1 for r in rows if not r[2])
    lines.insert(0, f"{len(rows)} commands, {leaves} of them leaf commands (the ones you run; a command marked … only groups others).")
    lines.insert(1, "")
    return "\n".join(lines)


def main():
    check = "--check" in sys.argv[1:]
    text = PAGE.read_text()
    m = BLOCK.search(text)
    if not m:
        raise SystemExit(f"commands_index: no <!-- commands --> block in {PAGE}")
    new = text[: m.start()] + m.group(1) + render() + "\n" + m.group(3) + text[m.end():]
    if new == text:
        return 0
    if check:
        print(f"stale: {PAGE.relative_to(REPO)}")
        return 1
    PAGE.write_text(new)
    print(f"updated: {PAGE.relative_to(REPO)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
