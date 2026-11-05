# Knowledge guides

**In short:** these pages show how to write down what your project knows (rules, memories, decisions and more), find it again, and hand it to your coding tool. Start with [Your first project](first-project.md); it takes five minutes and needs no team.

## Start here

- [Your first project](first-project.md): `lore init`, one rule, `lore render`, and a look at `CLAUDE.md`. Also `lore setup`, `lore doctor`, `lore tables` and shell completion.

## Write things down

- [Rules, memories and decisions](rules-memories-decisions.md): the three kinds you use most; add, list, change, replace (`--supersedes`), retire (`archive`, `invalidate`), delete, and why a password is stopped (`--not-a-secret --reason "<why>" --yes` to mark a sample value as not a secret).
- [Other kinds of notes](other-row-kinds.md): hotfixes, patterns, playbooks, prompts, snapshots, incidents, behaviours, architecture notes, cookbook recipes, suggestions, taste preferences, tech docs, workflows and workspaces.
- [Comments and tags](comments-and-tags.md): reply to a row, and label rows so they can be found together.
- [Bring in notes you already have](import-existing-notes.md): `lore learn-from`, the review tray (`lore learn`), and `lore external-source`.

## Find and use it

- [Search](search.md): one search over every kind, word rules, repo scope flags, and keeping the index healthy.
- [Render and agent files](render-and-agent-files.md): `lore render`, `--target` and `--out`, the directive block (the standing instructions at the top of the file), `lore why-context`, render history and `lore skill compile`.
- [Browse your notes in the terminal](tui.md): `lore tui`.

## Every example is a real run

The command output on these pages is pasted from real runs of the lore binary in throwaway folders, not typed by hand. The scripts live in [tests/docs](../../tests/docs/README.md); sections `start`, `knowledge`, `search` and `render` belong to these pages.

## Related

- [Tasks, missions and plans](../tasks/README.md)
- [Git sync](../sync/README.md): how notes travel with your commits.
- [Operations](../operations/README.md): doctor, backups, audit log, CI.
- [Reference](../reference/README.md): every command, flag, config key and error code.
- [Lore for teams](../team/README.md): orgs (your company's group of people and repos), stores (private note repos), live lists every teammate sees at once, and agents.
