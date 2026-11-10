# Keep the lore skill current: `lore skill install` and `lore skill status`

**In short:** the lore skill is the set of instructions a coding tool loads to use lore well. `lore skill install` copies the skill that is built into your lore binary into each tool's skill folder and stamps it with the binary's version. `lore skill status` tells you, per tool, whether the skill is there, which lore version it came from, and whether someone edited it by hand.

> **Status: in development.** These commands are being built for lore 0.1.18 and are not in a released lore yet. The output below comes from real runs of a development build in a throwaway home folder (a development build reports its version as `dev`).

## What it is and when you want it

A coding tool that loads the lore skill knows lore's commands, its row kinds and when to search before it answers. The skill ships inside the lore binary, so a copy in a tool's folder is only as current as the lore that wrote it. Install it once per machine, and again after each `lore upgrade` (lore prints a hint when the installed copy is older than the binary).

| Tool profile | Skill folder |
|---|---|
| `claude_code` | `~/.claude/skills/lore` |
| `codex` | `~/.codex/skills/lore` |

A tool profile of your own (`context.profiles`) can name its own `skill_dir`; a tool with no skill folder is skipped with the reason.

## Before you start

- lore installed (see [Install](../../README.md#install)). You do not need a repo: the skill folders are in your home folder.
- The examples below run with a development build, which reports its version as `dev`; a release prints its version number instead.

## Step 1 — See what is installed

`lore skill status` prints the binary's version, then one line per tool. On a fresh machine every tool says `not installed`, with the command that fixes it:

<!-- example: render#skill-status-before -->
```console
# alice, in website
$ lore skill status
lore dev
claude_code  warn  not installed; run lore skill install --tool claude_code
codex        warn  not installed; run lore skill install --tool codex
```
<!-- /example -->

## Step 2 — Look before you write

`--dry-run` lists the folders lore would write and writes nothing. `--all` means every tool that has a skill folder; `--tool <profile>` (repeatable) picks tools. Without either, lore installs for the tools of the active [setup](../team/guided-setup.md), else for the tools it finds on this machine. The setup page's coding tools step runs the same install.

<!-- example: render#skill-install-dry-run -->
```console
# alice, in website
$ lore skill install --all --dry-run
claude_code: would install lore dev skill in ~/.claude/skills/lore
codex: would install lore dev skill in ~/.codex/skills/lore
```
<!-- /example -->

## Step 3 — Install

Install for one tool. The folder holds the whole skill (several markdown files), and `SKILL.md` carries a `lore_version` line with the binary's version. Running the install again with nothing changed writes nothing: `--json` reports `"changed":false`.

<!-- example: render#skill-install -->
```console
# alice, in website
$ lore skill install --tool claude_code
claude_code: installed lore dev skill in ~/.claude/skills/lore
$ ls ~/.claude/skills/lore | head -5; grep -n '^lore_version' ~/.claude/skills/lore/SKILL.md
COMMANDS.md
DECISION-TREE.md
ERRORS.md
GLOSSARY.md
PLAYBOOKS.md
17:lore_version: dev
$ lore skill install --tool claude_code --json
{
  "installed": [
    {
      "tool": "claude_code",
      "dir": "~/.claude/skills/lore",
      "version": "dev",
      "changed": false
    }
  ],
  "skipped": []
}
```
<!-- /example -->

| Flag | Effect |
|---|---|
| `--tool <profile>` | install for this tool (repeat it) |
| `--all` | install for every tool that has a skill folder |
| `--dry-run` | show what would be written; write nothing |
| `--link replace` | the skill folder is a link: put lore's own copy in its place (the link's target is not touched) |
| `--link update` | the skill folder is a link: write this lore's skill into the link's target |
| `--json` | per tool: `tool`, `dir`, `version`, `changed`, `backup`, and `link` and `via` for a linked folder |

How it writes:

- The whole folder is written beside the old one and then swapped in, so a tool never sees a half-written skill. Files you added to the folder yourself are kept.
- A skill folder that is a symbolic link (a shared-skills tool linking `~/.claude/skills/lore` to `~/.agents/skills/lore`, or a link to a source checkout of lore) is read at the end of its links: `skill status` shows it installed with its real version and `-> <target>`. If the target already holds this lore's skill, install says it is up to date and writes nothing.
- lore never writes through such a link on its own. When the target holds an older or a hand-edited lore skill, another tool's skill, or nothing, install stops with `E_SKILL_LINKED`, naming the link and its target, and you pick one: `--link replace` puts lore's own copy where the link was (the target is not touched), `--link update` writes this lore's skill into the target, keeping your other files there and a `.bak` copy of any hand-edited file. With `--all`, a linked folder is skipped with the same message and the other tools are still installed.

## Step 4 — Check it again

<!-- example: render#skill-status-after -->
```console
# alice, in website
$ lore skill status
lore dev
claude_code  skip dev  ~/.claude/skills/lore  skill is linked from a source checkout
codex        warn  not installed; run lore skill install --tool codex
```
<!-- /example -->

A development build compares nothing, so the installed tool shows `skip` with the reason; with a release, the same line says `ok` and the version. The table under [Status](#status) lists every status.

## Try it: a hand edit is kept

Add a line to the installed `SKILL.md`, then check and install again. `skill status` marks the copy `(edited by hand)`, and because the copy on disk is from the same lore version, the install keeps your file as `<file>.bak-<date>` before writing the fresh one (the time is added when that name exists):

<!-- example: render#skill-edited -->
```console
# alice, in website
$ lore skill status --tool claude_code
lore dev
claude_code  skip dev  ~/.claude/skills/lore  (edited by hand)  skill is linked from a source checkout
$ lore skill install --tool claude_code
claude_code: installed lore dev skill in ~/.claude/skills/lore
  kept your edited copy as ~/.claude/skills/lore/SKILL.md.bak-2026-11-02
```
<!-- /example -->

## The `lore_version` line

Every installed `SKILL.md` carries one extra line in its front matter, the version of the lore binary that installed it:

```yaml
---
name: lore
description: |
  …
license: same as lore
lore_version: 0.1.18
---
```

lore compares that line with its own version. It is also how lore tells a hand edit from a newer skill: two copies are "the same skill" when they differ only in this line. A `SKILL.md` without the line is one lore did not stamp: lore's own source `skills/` folder (a link into a lore checkout), a skill installed by lore 0.1.17, or a hand copy. lore then compares the files themselves: when they equal this binary's skill, the skill is current.

## Status

The first line is the binary's version; then one line per tool: its status (`ok`, `warn` or `skip`), the installed version, the folder, `(edited by hand)` when the skill differs from the binary's copy at the same version, and the reason. `--tool` limits the list; `--json` prints `{"binary_version":…,"tools":[{"tool":…,"dir":…,"installed":…,"version":…,"modified":…,"same":…,"status":…,"detail":…}]}`, where `same` says the files equal this binary's skill (the version line aside). `tools.skill_version` and `tools.skill_modified` both read that one compare, so they never contradict each other.

| Status and detail | Meaning | Fix |
|---|---|---|
| `ok` | the skill is from this lore release | nothing |
| `warn` not installed | no `SKILL.md` in the tool's folder | `lore skill install --tool <tool>` |
| `warn` skill from lore 0.1.17, binary 0.1.18 | the skill is older than the binary | `lore skill install --tool <tool>` |
| `warn` skill is newer than the binary | the skill came from a newer lore | `lore upgrade`, or reinstall the skill |
| `ok` current (same as lore 0.1.18's skill; no version line) | no version line, but the files are this binary's skill (for example a link into lore's source `skills/` folder; the detail then ends "linked to lore's source folder") | nothing |
| `warn` no version line, and it differs from lore 0.1.18's skill (older, or edited by hand) | no version line and other files | `lore skill install --tool <tool>` |
| `skip` skill is linked from a source checkout | the skill's `lore_version` is `dev` | nothing to compare |
| `skip` binary is a development build | this lore is not a release | nothing to compare |

`lore doctor --all` reports the same results as the `tools.skill_installed`, `tools.skill_version` and `tools.skill_modified` checks ([every check](../operations/doctor-all.md#every-check)). After `lore upgrade`, lore prints a hint when an installed skill is older than the new binary: `skills are older than lore 0.1.18: lore skill install --all`.

## Pitfalls

- **An edit to an older skill is not kept.** The `.bak` copy is made only when the skill on disk has the same `lore_version` as the binary; when it is from an older lore, its differences are taken to be the old skill, not your edit, and the install replaces it. Keep your own instructions in a separate file of that folder (lore keeps files it did not install) or in your rules.
- **One machine, one copy.** The skill folders are in your home folder, not in the repo; install on each machine.

## Related

- [Render and agent files](render-and-agent-files.md): what your coding tool reads in each repo.
- [Guided setup](../team/guided-setup.md): the coding tools step.
- [The lore skill](../../skills/README.md).
