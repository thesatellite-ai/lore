# Which file your coding tool reads

**In short:** `lore render` writes your most important notes into one file, `.lore/LORE.md`, and puts a short pointer to it at the top of the file each of your coding tools reads when it starts. No tool is the default: when a setup applies to the folder, `lore init` wires the tools that setup picked (Claude Code gets `CLAUDE.md`); otherwise lore wires the tool files your repo already has (`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, a Cursor rule, the Copilot instructions), and `AGENTS.md` when it finds none. Name another tool or file with `--target`; it always wins. Notes from private stores (your company's store, your personal store) go to files outside the repo, so they are never committed.

## The options in one table

| You want | Command | What it writes |
|---|---|---|
| The default | `lore render` | `.lore/LORE.md` and `.lore/LORE.full.md`, plus a pointer block in every tool file that already carries one; in a repo where none does yet, in the tool files that exist, else in `AGENTS.md` |
| A tool lore knows | `lore render --target cursor` (profiles: `claude_code`, `codex`, `cursor`, `gemini_cli`, `copilot`, `generic`) | a pointer block in that tool's file (`.cursor/rules/lore.mdc` here) |
| Any other file | `lore render --target AGENTS.md`, or a profile of your own under `context.profiles` in `~/.lore/config.yaml` | a pointer block in that file |
| Two tools at once | `lore render --target claude_code,codex` (or `--target` twice) | one pointer in each file, this render only |
| One more tool, kept | `lore render --add-target claude_code` | adds the tool to the repo's `context.targets` (keeping the ones there) and writes its file; every later plain `lore render` keeps it |
| The notes file somewhere else | `lore render --out docs/agent-notes.md` | `docs/agent-notes.md`, and the pointer names it |
| Only the notes file, no pointer | `lore render --no-pointer` | `.lore/LORE.md`; no agent file is touched |
| Only the "use lore" instructions | `lore directive install --target AGENTS.md` | the directive block at the top of `AGENTS.md`, no pointer |
| A look before writing anything | `lore render --dry-run` | nothing; prints the file instead |
| Notes from private stores (company, personal) | `lore render` (nothing extra) | one file per store under `~/.lore/render/<repo name>-<repo id>/`, named `<store name>-<store id>.md` (with an `index.md` listing them), outside the repo, and an import of each in the tool's private file (`CLAUDE.local.md` for `claude_code`, a Cursor rule for `cursor`), hidden from git |
| To check the size in CI | `lore render --check` | nothing, not even inside `.git` (no hook, config key or `info/exclude` line); fails when `.lore/LORE.md` would be over its budget or a committed rendered file is stale (a file kept only on this machine, such as the private render under `~/.lore/render`, is never in a clone, so it is listed as a note and never fails the check); over the warn level (80% of the budget) it prints a warning and passes; a repo that never rendered passes with a note, as `lore doctor` lists it |
| A tool that cannot follow an import line | `lore context --internal` | nothing; prints the private notes for this repo as text |
| One short skill file from a folder of markdown | `lore skill compile` | a draft, `SKILL-<target>-draft.md`, for you to review |

A **pointer** is a short note that says "read that file too". It is a few lines between `<!-- lore:pointer:start -->` and `<!-- lore:pointer:end -->`. Its main line is `@.lore/LORE.md`, which tools that support imports follow; a comment under it tells other tools to read the file. Your own text outside the markers is never touched.

## If this, then that

- **If the repo already has your tool's file,** run `lore render` and commit that file and `.lore/LORE.md` with your notes. `lore init` already wired it and said which tools it found.
- **If you add a tool later,** run `lore render --add-target <profile>`: it saves the tool in `context.targets`, so a plain `lore render` keeps its file current. `--target` wires a file for one render only (use it for a file that is not a tool profile). lore never wires a new tool file behind your back; a plain `lore render` names the file and the command.
- **If your tool is not in the list,** add a profile for it (`name`, `pointer_file`, `import`, `budget`) under `context.profiles` in `~/.lore/config.yaml`; `lore config check` reports mistakes in it.
- **If you keep your own text in the agent file,** write it outside the pointer markers; render replaces only what is between them.
- **If a note must be seen in every session,** make it a `must` rule. Render pins (always copies into the file) only `must` rules and `critical` or `high` hotfixes. The rest is found with `lore search`, which the directive (the "use lore" instructions block) tells the tool to run.
- **If you work in a client's repo,** run `lore render` as usual. The committed `.lore/LORE.md` holds only the repo's own notes and those of stores marked `shareable`; your company's and personal notes go to `~/.lore/render/`, imported through the tool's private file.
- **If your tool has no private file** (`codex`, `gemini_cli`, `copilot`), or your CI job can read only one file, feed it the output of `lore context --internal`, and never paste that output anywhere the client can read.
- **If you want to know what the tool was given,** run `lore why-context`.

## Pitfalls

- **Never edit `.lore/LORE.md` by hand.** The next render replaces it. Change the notes and render again.
- **Render again after you add or change notes.** When a `git pull` brings in a teammate's notes, lore refreshes `.lore/LORE.md` by itself, but only at the default path and only when you run lore from the repo root. A file moved with `--out` and your own new notes wait for the next `lore render`.
- **The private file is for this machine only.** Lore hides `CLAUDE.local.md` (and the other tools' private files) through `.git/info/exclude` (a private ignore list that stays on your machine), never through the client's `.gitignore`, so the client never sees that it exists. Do not commit it.
- **Each tool has a budget.** Render prints a `must` rule in full or as one line by how much room the tool's budget leaves; `lore render --report` shows what was cut, and `lore pin set --id <id> --context full` keeps one rule in full.
- **`lore skill compile` sends your text to an outside service.** It needs network access and an account there, and every run gives slightly different output. Treat the result as a draft.

## Guides and examples for each option

| Option | Guide | Example |
|---|---|---|
| `lore render`, `--target`, `--out`, `--no-pointer`, `--dry-run`, directive | [Render and agent files](../knowledge/render-and-agent-files.md) | [`render`](../../tests/docs/sections/render.sh) ([output](../../tests/docs/transcripts/render.txt)) |
| A first render | [Your first project](../knowledge/first-project.md) | [`start`](../../tests/docs/sections/start.sh) ([output](../../tests/docs/transcripts/start.txt)) |
| Private notes in a client repo | [How it works: rendering context outside the checkout](../team/how-it-works.md#rendering-context-outside-the-checkout) | [`agency`](../../tests/team/docs/transcripts/agency.txt) |
| `lore context --internal` | [Use lore in CI](../operations/ci.md#step-5--give-tools-the-notes-they-cannot-import) | [`ops`](../../tests/docs/sections/ops.sh) ([output](../../tests/docs/transcripts/ops.txt)) |
| `lore skill compile` | [Render and agent files: build a skill file](../knowledge/render-and-agent-files.md#build-a-skill-file) | none: it needs an outside service |

## Related

- [Search](../knowledge/search.md): how the tool finds the notes that are not pinned.
- [Which org and identity setup](org-and-identity.md)
- [All decision guides](README.md)
