# Knowledge guides

**In short:** these pages show how to write down what your project knows (rules, memories, decisions and more), find it again, and hand it to your coding tool. Start with [Your first project](first-project.md); it takes five minutes and needs no team.

## Start here

- [Your first project](first-project.md): `lore init`, one rule, `lore render`, and a look at `CLAUDE.md`. Also `lore doctor`, `lore sql` and shell completion.

## Write things down

- [Rules, memories and decisions](rules-memories-decisions.md): the three kinds you use most; add, list, change, replace (`--supersedes`), retire (`archive`, `invalidate`), delete, move to another store (`memory move`), and why a password is stopped (`--not-a-secret --reason "<why>" --yes` to mark a sample value as not a secret).
- [Other kinds of notes](other-row-kinds.md): hotfixes, patterns, playbooks, prompts, snapshots, incidents, behaviours, architecture notes, cookbook recipes, suggestions, taste preferences, tech docs, workflows and workspaces; show, edit, search, archive, and linking an incident to the row that fixed it.
- [Comments and tags](comments-and-tags.md): reply to a row, search and delete comments, label rows so they can be found together, and rename a tag or give it files (`lore tag edit`).
- [Bring in notes you already have](import-existing-notes.md): turn an old notes file into rules, decisions and memories, and `lore external-source`.
- [Variables and metadata](variables-and-metadata.md): `lore vars` for one named value many notes point at (`${var:api_host}`), and `lore meta` for extra fields on any row (`client=acme`), with allowed values, bulk changes, export and history.

## Find and use it

- [Search](search.md): one search over every kind, word rules, repo scope flags, and keeping the index healthy.
- [Render and agent files](render-and-agent-files.md): `lore render`, `--target` and `--out`, the directive block (the standing instructions at the top of the file), `lore why-context`, render history, pinning how a rule appears (`lore pin`), rules for one folder (`lore scope`) and `lore skill compile`.
- [Keep the lore skill current](skill-install.md): `lore skill install`, `lore skill status`.
- [Browse your notes in the terminal](tui.md): `lore tui`, and `lore show <id>` for any row with `--origin`.
- [Edit a row in your editor](editing-rows.md): `lore edit <id>` opens any row as Markdown in `$EDITOR`; `lore path <id>` prints its file.

## Every example is a real run

The command output on these pages is pasted from real runs of the lore binary in throwaway folders, not typed by hand. The scripts live in [tests/docs](../../tests/docs/README.md); sections `start`, `knowledge`, `search`, `render` and `meta` belong to these pages.

## Which page do I need?

| You want to | Read |
|---|---|
| say "always" or "never" to every coding session | [Rules, memories and decisions](rules-memories-decisions.md), then [Render](render-and-agent-files.md) |
| keep a fact that is true today and may change | a memory in [Rules, memories and decisions](rules-memories-decisions.md) |
| write one value (a host, a port) once and use it in many notes | [Variables and metadata](variables-and-metadata.md) |
| sort or count rows by client, cost or plan | [Variables and metadata](variables-and-metadata.md) |
| warn every session about a live problem | a hotfix in [Other kinds of notes](other-row-kinds.md) |
| find a note again | [Search](search.md) or [lore tui](tui.md) |
| give one folder its own rules | [Render › Rules for one folder](render-and-agent-files.md#rules-for-one-folder) |

## Related

- [Tasks, missions and plans](../tasks/README.md)
- [Git sync](../sync/README.md): how notes travel with your commits.
- [Operations](../operations/README.md): doctor, backups, audit log, CI.
- [Reference](../reference/README.md): every command, flag, config key and error code.
- [Lore for teams](../team/README.md): orgs (your company's group of people and repos), stores (private note repos), live lists every teammate sees at once, and agents.
