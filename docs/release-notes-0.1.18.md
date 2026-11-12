# lore 0.1.18

lore 0.1.18 puts a new core under every command and is the first release whose source is under the Apache License 2.0. Your notes now live as plain files in your repository, every command works the same from the terminal and from a local API, and a new guided setup gets a machine ready for one or several GitHub accounts and teams.

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
- **A local setup page.** `lore setup` on a terminal opens a small local page with strict safety rules. The prebuilt binaries serve the guided setup page; a build from source serves a plain page that lists the commands doing the same work.

## Upgrading from 0.1.17

- Run `lore fix` once in each repository that holds v0.1.17 data (`lore upgrade` runs it for the repo you are in). It converts the notes to the new format the way `lore migrate run` does (a journal so an interrupted run can resume, `lore migrate rollback` to undo), renders `LORE.md` again and repairs what `lore doctor` finds; `lore fix --dry-run` shows the steps and writes nothing. Old config files are moved aside as `*.migrated`, never deleted.
- `lore skill install --all` updates the agent skill for every coding tool on the machine; `lore skill status` shows which ones are out of date.
- Some changes break old habits, all listed under "Changed" in the changelog: exit codes now follow the kind of error, names of tags and metadata keys are snake_case, `--db` is refused, and `lore tui` is a read-only browser (add and edit with the commands).
- Commands that were removed, and what replaces them, are listed under "Removed" in the changelog.
- **Upgrade every machine of every teammate together.** The first lore command on 0.1.18 (a read included; also `lore fix` and the first write in a project with 0.1.17 rows) moves the shared stores it finds to the new layout and pushes that commit, printing right before it that everyone using the store with 0.1.17 must upgrade. lore 0.1.17 cannot read the new layout and cannot be told it is newer: a teammate (or another machine of yours) still on 0.1.17 gets `E_NOT_A_STORE … is not a lore store` (with the hint to run `lore store init`, which is wrong for them) on every lore write to that store, their `git commit` in a repo linked to an org store is refused by the 0.1.17 pre-commit hook, and their reads leave out the store's notes. Nothing is lost or corrupted: they install 0.1.18 and run `lore fix`; until then `git commit --no-verify` lets them commit code. See [Upgrade lore](operations/upgrade.md).

## License

From 0.1.18, the lore source code is licensed under the Apache License 2.0 (see `LICENSE` and `NOTICE`). Releases v0.1.17 and earlier remain under the PolyForm Perimeter License 1.0.0, the license they were published under.

The prebuilt binaries (the archives on GitHub Releases, the Homebrew formula and the install scripts) contain this source and also proprietary components, such as the guided setup page; they are free to use, including for commercial work, under `BINARY-LICENSE.txt`, which ships in every archive beside `LICENSE-core-Apache-2.0` and `NOTICE-core`. A build from source is Apache-2.0 alone.

## Install

Each of these installs the prebuilt binary for your platform from this release's archives (see License above), with the agent skill.

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

Or download an archive from [GitHub Releases](https://github.com/thesatellite-ai/lore/releases) and check it against `checksums.txt`. To build from source instead: `go install github.com/thesatellite-ai/lore/cmd/lore@v0.1.18`.
