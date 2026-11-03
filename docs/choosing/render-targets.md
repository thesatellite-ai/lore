# Which file your coding tool reads

**In short:** `lore render` writes your most important notes into one file, `.lore/LORE.md`, and puts a short pointer to it at the top of the file your coding tool reads when it starts (`CLAUDE.md` by default). If your tool reads a different file, name it with `--target`. In a client's repo, the agency's private notes go to a file outside the repo, so they never reach the client.

## The options in one table

| You want | Command | What it writes |
|---|---|---|
| The default | `lore render` | `.lore/LORE.md`, plus a pointer block in `CLAUDE.md` |
| A tool that reads `AGENTS.md` or another file | `lore render --target AGENTS.md` | the same `.lore/LORE.md`, plus a pointer block in `AGENTS.md` |
| Two tools with two files | `lore render`, then `lore render --target AGENTS.md` | one pointer in each file; `--target` takes one file per run |
| The notes file somewhere else | `lore render --out docs/agent-notes.md` | `docs/agent-notes.md`, and the pointer names it |
| Only the notes file, no pointer | `lore render --no-pointer` | `.lore/LORE.md`; no agent file is touched |
| Only the "use lore" instructions | `lore directive install --target AGENTS.md` | the directive block at the top of `AGENTS.md`, no pointer |
| A look before writing anything | `lore render --dry-run` | nothing; prints the file instead |
| The agency's private notes, in a client repo | `lore render` (nothing extra) | `~/.lore/render/<project-id>.md` outside the repo, and a one-line `CLAUDE.local.md` that git ignores |
| A tool that cannot follow an import line | `lore context --internal` | nothing; prints the private notes for this repo as text |
| One short skill file from a folder of markdown | `lore skill compile` | a draft, `SKILL-<target>-draft.md`, for you to review |

A **pointer** is a short note that says "read that file too". It is a few lines between `<!-- lore:pointer:start -->` and `<!-- lore:pointer:end -->`. Its main line is `@.lore/LORE.md`, which tools that support imports follow; a comment under it tells other tools to read the file. Your own text outside the markers is never touched.

## If this, then that

- **If your tool reads `CLAUDE.md`,** run `lore render` and commit `CLAUDE.md` and `.lore/LORE.md` with your notes.
- **If your tool reads `AGENTS.md` (or any other file),** run `lore render --target AGENTS.md`. Use both commands if two tools share the repo.
- **If you keep your own text in the agent file,** write it outside the pointer markers; render replaces only what is between them.
- **If a note must be seen in every session,** make it a `must` rule. Render pins (always copies into the file) only `must` rules and `critical` or `high` hotfixes. The rest is found with `lore search`, which the directive (the "use lore" instructions block) tells the tool to run.
- **If you work in a client's repo,** run `lore render` as usual. The committed `.lore/LORE.md` holds only the repo's own notes; your company's and personal notes go to `~/.lore/render/`, imported through `CLAUDE.local.md`.
- **If your tool or CI job can read only one file,** feed it the output of `lore context --internal`, and never paste that output anywhere the client can read.
- **If you want to know what the tool was given,** run `lore why-context`.

## Pitfalls

- **Never edit `.lore/LORE.md` by hand.** The next render replaces it. Change the notes and render again.
- **Render again after you add or change notes.** When a `git pull` brings in a teammate's notes, lore refreshes `.lore/LORE.md` by itself, but only at the default path and only when you run lore from the repo root. A file moved with `--out` and your own new notes wait for the next `lore render`.
- **`CLAUDE.local.md` is for this machine only.** Lore hides it through `.git/info/exclude` (a private ignore list that stays on your machine), never through the client's `.gitignore`, so the client never sees that it exists. Do not commit it.
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
