# Flags

**In short:** a flag is an option you add to a command, such as `--json` in `lore rule list --json`. Some flags work on every command, and many commands share the same flags with the same meaning. This page lists those shared flags once. For every flag of one command, run `lore <command> --help`.

## Flags every command takes

| Flag | What it does |
|---|---|
| `--color auto\|always\|never` | colours in the output; `auto` (the default) uses colour only on a terminal and follows `NO_COLOR` and `FORCE_COLOR` |
| `--trace` | print every git command lore runs for live rows, with timing (same as `LORE_TRACE=1`) |
| `-h`, `--help` | show the command's help and stop |
| `-v`, `--version` | only on `lore` itself: print the version (`lore version` prints more) |

## Where a command reads and writes

Nearly every command that opens the notes database takes these four. A flag wins over the matching [environment variable](environment.md), which wins over the files in `.lore/`.

| Flag | What it does |
|---|---|
| `--db <path>` | refused on row commands with `E_UNSUPPORTED`: their rows are files read from the repository, not from `lore.db`, so run the command inside the repository instead (`LORE_DB` is refused the same way) |
| `--project <id or name>` | use this project of the database (same as `LORE_PROJECT_ID`) |
| `--repo <name or id>` | work in one repo of a multi-repo project, by its mount name or `rep_` id (same as `LORE_REPO`) |
| `--read-only` | refuse every write with `E_READ_ONLY` and take no lock (same as `LORE_READ_ONLY=1`) |

<!-- example: reference#db-flag -->
```console
# alice, in ~
$ lore rule list --db work/website/.lore/lore.db
ERROR: [E_UNSUPPORTED] --db is not supported by this command: its rows are row files read from the repository, not lore.db
  hint: run the command inside the repository (or name a store with --store / --to)
(exit 23)
$ lore rule list
ERROR: [E_NOT_A_STORE] git: the folder is not inside a git work tree
(exit 19)
$ cd work/website && lore rule list
rul_162d…9666 [must/always] Run the tests before you push
```
<!-- /example -->

A few commands use `--project` for something of their own: `lore org link`, `org unlink` and `org set` take `--project` for the internal project folder in the org's store. `lore task next --repo` picks tasks of one repo of the group.

## Output

| Flag | What it does |
|---|---|
| `--json` | print the result as JSON for scripts instead of text. List, show and search commands take it, plus `doctor`, `errors list`, `version`, `whoami`, `context`, `sync` and more. Errors are still printed as text (see [error codes](errors.md)) |
| `--limit <n>` | at most this many results (search commands: default 20; `lore audit log`: default 50, 0 means all) |
| `--include-archived` | also show archived rows (search commands) |
| `--column <name>` | search only in this field, such as the title (search commands; repeat the flag for more) |

## Repo scope in a multi-repo project

A project can hold several repos. A row either belongs to one repo or to the whole project (lore calls the whole project *master*). With `--repo`, a command sees that repo's rows and the whole-project rows.

| Flag | What it does |
|---|---|
| `--all-repos` | list or search across every repo and ignore `--repo` |
| `--master-only` | only rows of the whole project, no repo's own rows |
| `--no-inherit` | only rows of `--repo`, without the whole-project rows |
| `--rebind-repo <name>` | on `edit`: move the row to that repo |
| `--rebind-master` | on `edit`: move the row to the whole project |

## Where a new row goes (teams)

These matter when you work for an org in client repos. A **store** is a separate notes-only repo that only your company can open; your **personal store** is yours alone.

| Flag | What it does | Taken by |
|---|---|---|
| `--to repo\|<org>\|<org>/<folder>\|me\|me/<org>` | where the row is written: this repo, the org's linked internal project, one folder of the org's store, or your personal store | `rule add`, `memory add`, `decision add`, `comment add`, `mission add`, `task add`, `task note`; required by `move` and `share` |
| `--create` | with `--to <org>/<folder>`: make that store folder if it does not exist yet (otherwise an unknown folder is refused) | `rule add`, `memory add`, `decision add`, `comment add` |
| `--everywhere` | with `--to me`: file the note under `everywhere/`, so it loads in every repo | the same commands as `--to` except `move` and `share` |
| `--from repo\|<org>\|<org>/<folder>` | which place an id belongs to, when it could be in more than one | `task show`, `edit`, `done`, `cancel`, `merged`, `note`, `claim`, `release`, `revoke`, `reassign`, `gate request`, `gate approve` |
| `--live` | share this row with everyone at once on the live list, before any commit | the `add` and `edit` commands of most row kinds |
| `--no-live` | keep this row with the code: it travels with this branch's next commit | the same commands as `--live` |

`lore task reassign --from @handle` is different: there `--from` names a person, and every open task of that person is reassigned.

## Confirming and trying first

| Flag | What it does |
|---|---|
| `--yes` | answer yes to the question lore would ask (`rule add`, `memory add`, `decision add`, `comment add`, `org import`, `org link`, `org unlink`, `move`, `share`). For `move` and `share` it is never accepted from an agent |
| `--confirm` | required by commands that delete or overwrite for good: every `delete` (except `task view delete`, which takes `--yes`), `restore`, `repair`, `sync purge` |
| `--dry-run` | show what would happen and change nothing (`land`, `render`, `org link`, `org unlink`, `live compact`, `sync promote`, `sync purge`, `sync fix-projects`, `sync install-action`, `sync ci-merge`, `skill compile`) |
| `--auto` | opt in to automatic runs that spend GitHub Actions minutes (`sync install-action`: adds the pull request and push triggers; without it the workflow runs only when started by hand, and a re-run keeps the triggers the file already has) |

## Writing a row

| Flag | What it does |
|---|---|
| `--body <text>` | the row's text |
| `--created-by <actor id>` | who wrote it (an `act_` id); defaults to you |
| `--supersedes <id>` | on `rule`, `memory`, `decision`, `hotfix` and `pattern add`: the older row this one replaces. Rows linked by older versions point the wrong way; `lore doctor --fix` repairs them once |
| `--yes` on a secret warning | on `rule add`, `memory add` and the other `add`/`edit` commands: store the text as not a secret even though it looks like it holds a password or key (the commit hook still refuses a file with a credential pattern) |

## Pitfalls

- `--json` is not on every command; when a command does not take it, lore stops with `unknown flag: --json`.
- `--color` is spelled the American way; `--colour` is an unknown flag.
- `--repo` only scopes a command inside a multi-repo project. To run a command on another checkout, run it inside that checkout.

Here `LORE_ORG` names `freelance`, but the repo is linked to the `agency` store, so the write is refused. `--force-org` no longer exists; name the store the note goes to with `--to` instead:

<!-- example: reference#force-org -->
```console
# alice, in website
$ LORE_ORG=freelance lore memory add --body 'The client pays invoices in 30 days'
ERROR: [E_ORG_DISAGREES] LORE_ORG chose freelance, but this repo is linked in agency
  hint: name the org the write goes to with --to <org>, or fix the choice with `lore org use` (or unset LORE_ORG)
(exit 17)
$ lore memory add --force-org --body 'The client pays invoices in 30 days'
ERROR: [E_INVALID_INPUT] unknown flag: --force-org
(exit 20)
$ lore memory add --to freelance --body 'The client pays invoices in 30 days'
✓ mem_d431…313a [freelance]
  scope: [master]
```
<!-- /example -->

## Related

- [Command index](commands.md): every command, with a link to its guide.
- [Environment variables](environment.md): the same switches for a whole shell.
- [Team command reference](../team/commands.md): the team flags in context.
