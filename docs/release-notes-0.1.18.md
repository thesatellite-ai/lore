# lore 0.1.18

lore 0.1.18 puts a new core under every command and is the first release of lore under the Apache License 2.0. Your notes now live as plain files in your repository, every command works the same from the terminal and from a local API, and a new guided setup gets a machine ready for one or several GitHub accounts and teams.

The full list of changes is in [CHANGELOG.md](../CHANGELOG.md).

## Highlights

- **Your files are the truth.** Every note, rule, decision and task is one small JSON file under `.lore/data` (or in a store you attach). The local cache is rebuilt from those files whenever it is missing or damaged, so there is nothing to back up beyond your git repository.
- **Stores.** Keep notes in the repo, in a separate git store (pushed at once, so two clones agree), or in a local folder. `--to` picks where a note is written; `.lore/stores.yaml` lists what a repo attaches.
- **One file per coding tool.** lore writes `.lore/LORE.md` and points each tool at it through the file that tool reads: `CLAUDE.md`, `AGENTS.md`, `.cursor/rules/lore.mdc`, `GEMINI.md` or `.github/copilot-instructions.md`. No tool is the default; with no tool file in the repo, `lore init` writes `AGENTS.md`.
- **A small, focused context.** `lore render` keeps `.lore/LORE.md` inside a size budget (must-follow rules, critical warnings, an index of topics) and `lore context --for-file`, `--for-task` and `--topic` fetch the rest when the agent needs it.
- **Guided setup from the command line.** `lore setup plan`, `check` and `apply` set up a machine step by step: identity, GitHub account and how lore reaches it, personal store, team, coding tools, secrets manager and folders. Several setups can live on one machine and lore picks the right one from the folder you are in. `lore doctor --all` checks every setup and names the fix for each problem; `lore setup repair` runs the safe ones.
- **Teams.** Invites (`lore team invite`, `lore setup join`), people with profiles, contacts, company periods and project roles, and leak checks that keep internal notes out of client repos.
- **Trash and undo.** `delete` moves a note to the trash; `lore trash list|restore|purge` and `lore batch undo` bring it back or remove it for good.
- **A local API.** `lore api` serves every command over HTTP on 127.0.0.1 with a token and an OpenAPI description, built from the same list of operations as the CLI, so the inputs, checks and errors are the same.
- **More to keep track of.** Metadata, resources, variables and references to secrets kept in your vault (never the secret itself), task trees and templates, and where each note came from.
- **A local setup page.** `lore setup` on a terminal opens a small local page with strict safety rules. In this open-source build it lists the commands that do the same work.

## Upgrading from 0.1.17

- Run `lore migrate run` once in each repository that holds v0.1.17 data. It converts the notes to the new format, keeps a journal so an interrupted run can resume, and supports `--dry-run` and `rollback`. Old config files are moved aside as `*.migrated`, never deleted.
- `lore skill install --all` updates the agent skill for every coding tool on the machine; `lore skill status` shows which ones are out of date.
- Some changes break old habits, all listed under "Changed" in the changelog: exit codes now follow the kind of error, names of tags and metadata keys are snake_case, `--db` is refused, and `lore tui` is a read-only browser (add and edit with the commands).
- Commands that were removed, and what replaces them, are listed under "Removed" in the changelog.

## License

From 0.1.18, lore is licensed under the Apache License 2.0 (see `LICENSE` and `NOTICE`). Releases v0.1.17 and earlier remain under the PolyForm Perimeter License 1.0.0, the license they were published under.

## Install

```sh
brew install thesatellite-ai/tap/lore
```

or

```sh
curl -sL https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.sh | sh
```

On Windows:

```powershell
irm https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.ps1 | iex
```
