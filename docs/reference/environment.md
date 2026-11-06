# Environment variables

**In short:** an environment variable is a setting you give one shell or one command, like `LORE_READ_ONLY=1 lore doctor`. lore reads a few of them to change what one run does: which database to use, whether it may write, whether it may use the network. None of them is needed for normal use.

Set one for a single command by writing it in front (`LORE_SYNC_QUIET=1 lore sync`), or for the whole shell with `export LORE_SYNC_QUIET=1`. Unless a row says otherwise, a switch is on only with the exact value `1` and off only with the exact value `0`; any other value is the same as not setting it.

## Which database, project and repo

A row command reads its rows from the repository it runs in. It picks its project and repo in this order, and the first one found wins: the flag (`--project`, `--repo`), then the variable below, then `.lore/lore.yaml` in the current folder, then `.lore/lore.db` in the current folder. lore never looks in parent folders.

| Variable | What it does |
|---|---|
| `LORE_DB` | refused on row commands with `E_UNSUPPORTED`, like `--db`: rows are files read from the repository, so run the command inside it |
| `LORE_PROJECT_ID` | use this project id instead of looking it up |
| `LORE_REPO` | work in this repo of a multi-repo project (its mount name or `rep_` id) instead of the whole project |
| `LORE_HOME` | the value of `${LORE_HOME}` inside `db_path` of an old shared-database pointer (`.lore/lore.yaml`), read when `lore migrate run` converts it (default `~/.lore`). It does **not** move lore's own folder: config, identity and caches always stay in `~/.lore` |
| `LORE_ALLOW_DB_PATH_OUTSIDE` | `1` lets `lore migrate run` read an old pointer whose `db_path` has a `..` part (normally refused, so a pointer file cannot point somewhere unexpected) |

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

Older lore could point a repo at a shared database file with a pointer, `.lore/lore.yaml`; 0.1.18 converts such a repo with `lore migrate run` ([One store for several repos](../sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database)). The pointer below names `${LORE_HOME}`, so `LORE_HOME` decides which database file is converted, and the new store lands beside it. lore's own folder stays `~/.lore` either way:

<!-- example: environment#lore-home -->
```console
# alice, in docs
$ cat .lore/lore.yaml
db_path: ${LORE_HOME}/client-docs.db
project_id: prj_d431…313a
$ lore migrate run --dry-run
ERROR: [E_INTERNAL] internal error
(exit 1)
$ LORE_HOME=~/shared-dbs lore migrate run
count: 2
rows: 
steps: mode-b: repos/rep_b4f4…a5b0 → store shared (~/shared-dbs/stores/blog), mode-b: rules/rul_28f8…03df → store shared (~/shared-dbs/stores/blog), mode-b: .lore/stores.yaml: add store shared file://~/shared-dbs/stores/blog, mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
journal: team lock: this repo has no remote origin; nobody to race, none taken, backup: refs/lore-backup/migrate/2026-11-02 (local only, never pushed), undo: lore migrate rollback, team lock released
backup_ref: refs/lore-backup/migrate/2026-11-02
$ lore rule list
rul_28f8…03df  Posts go out on Tuesdays  [shared]

# alice, in ~
$ ls ~/shared-dbs ~/shared-dbs/stores
~/shared-dbs:
client-docs.db
stores
~/shared-dbs/stores:
blog
```
<!-- /example -->

A `..` in `db_path` is refused until `LORE_ALLOW_DB_PATH_OUTSIDE=1` is set for the command:

<!-- example: environment#db-path-outside -->
```console
# alice, in blog
$ cat .lore/lore.yaml
db_path: ../client.db
project_id: prj_d431…313a
$ lore migrate run --dry-run
ERROR: [E_INTERNAL] internal error
(exit 1)
$ LORE_ALLOW_DB_PATH_OUTSIDE=1 lore migrate run
count: 2
rows: 
steps: mode-b: repos/rep_b4f4…a5b0 → store shared (~/work/stores/blog), mode-b: rules/rul_28f8…03df → store shared (~/work/stores/blog), mode-b: .lore/stores.yaml: add store shared file://~/work/stores/blog, mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
journal: team lock: this repo has no remote origin; nobody to race, none taken, backup: refs/lore-backup/migrate/2026-11-02 (local only, never pushed), undo: lore migrate rollback, team lock released
backup_ref: refs/lore-backup/migrate/2026-11-02
$ lore rule list
rul_28f8…03df  Posts go out on Tuesdays  [shared]
```
<!-- /example -->

## Read-only and safety switches

| Variable | What it does |
|---|---|
| `LORE_READ_ONLY` | `1` refuses every write in this shell with `E_READ_ONLY`, like `--read-only`; lore also takes no lock and changes no git settings. Useful in CI |
| `LORE_ALLOW_ROOT` | `1` lets lore run as the root user. Refused by default, because files made as root stay unwritable for you (`E_ROOT_REFUSED`) |
| `LORE_ALLOW_NETWORK_FS` | `1` lets the database sit on a network or cloud-synced drive. Refused by default, because the database file can break silently there (`E_NETWORK_FS`) |
| `LORE_NO_STALE_WARN` | ignored in 0.1.18: there is no "run `lore setup` again" warning to hide (the local cache is kept in step by lore itself); still accepted so older CI scripts run |

<!-- example: reference#read-only only=1 -->
```console
# alice, in website
$ lore rule add --body 'Never put passwords in the code'
ERROR: [E_READ_ONLY] command requires write access but read-only mode is active
  hint: unset --read-only and LORE_READ_ONLY
(exit 23)
```
<!-- /example -->

A repo inside a folder a sync app owns (here `Dropbox`) is refused with `E_NETWORK_FS`; `LORE_ALLOW_NETWORK_FS=1` lets one command through:

<!-- example: environment#network-drive -->
```console
# alice, in Dropbox/website
$ lore rule list
ERROR: [E_NETWORK_FS] rowfs: the store is inside a "dropbox" folder (cloud sync)
  hint: move the store to a local folder, or set LORE_ALLOW_NETWORK_FS=1 to accept the risk
(exit 23)
$ LORE_ALLOW_NETWORK_FS=1 lore rule list
(no rules)
```
<!-- /example -->

## Sync with git (`.lore/data`)

lore copies your notes into `.lore/data`, the folder git commits, before and after each command. These switches change that.

<!-- ds:block id=sync-env-qmha7pjc -->
| Variable | What it does |
|---|---|
| `LORE_SYNC` | `0` turns sync off for this command or shell: lore uses only the local database, writes nothing to `.lore/data`, and live rows are off too |
| `LORE_SYNC_GIT` | `0` keeps syncing the files but stops lore from running git setup by itself: no hooks, no `.gitattributes`, no git config |
| `LORE_SYNC_QUIET` | `1` hides sync's progress lines; errors still print |
| `LORE_SYNC_ALLOW_SECRETS` | removed in 0.1.18 and ignored; `lore doctor` warns while it is still set. A row you confirmed with `--not-a-secret --reason "<why>" --yes` is already in `.lore/data`; lore's commit hook still refuses a file that holds a credential pattern |

## Live rows and the network

A **live row** is a note everyone sees at once, before any commit. lore keeps live rows on a hidden list on the git server that is not a branch (git calls it a *ref*).

<!-- ds:block id=team-env-live-hc3w5qby -->
| Variable | What it does |
|---|---|
| `LORE_LIVE` | `0` turns live rows off in this shell: every row waits for a commit, as before live rows existed |
| `LORE_LIVE_AUTOPUSH` | `0` means lore never uses the network by itself (same as `live.autopush: false`): live writes and store writes wait on your machine until `lore sync`, and writes that must reach the server at once, such as claiming a task, fail with `E_OFFLINE` |
| `LORE_LIVE_REMOTE` | the git remote that holds the live rows (default `origin`), for forks and tests |
| `LORE_TRACE` | `1` prints every git command lore runs for live rows, with timing, like `--trace` |

`LORE_LIVE=0` keeps a new task out of the live list: it is written to `.lore/data` and waits for a commit, while the next task without the switch goes live as usual:

<!-- example: environment#live-off -->
```console
# alice, in website
$ LORE_LIVE=0 lore task add 'Update the privacy page'
✓ tsk_7a67…9504 — Update the privacy page [medium]
$ git status --short
?? .lore/data/task_lists/
?? .lore/data/tasks/
$ lore task add 'Check the contact form on a phone'
✓ tsk_2ad3…f891 — Check the contact form on a phone [medium]
lore live: (live, pushed)
$ lore live show
tasks      tsk_2ad3…f891     live      since 2026-11-02 10:00
1 live rows in _root (epoch 1)
```
<!-- /example -->

`LORE_LIVE_AUTOPUSH=0` keeps a live write on this machine until `lore sync` pushes it, and refuses a claim, which must reach the server at once:

<!-- example: environment#autopush-off -->
```console
# alice, in website
$ LORE_LIVE_AUTOPUSH=0 lore task add 'Renew the SSL certificate'
✓ tsk_162d…9666 — Renew the SSL certificate [medium]
lore live: (live, not pushed yet: 1 waiting; autopush is off, `lore sync` pushes)
$ lore live diff
local     601d2850ffd0
tracking  86dbae476bd6
seen      86dbae476bd6
pending   1 write(s) not confirmed by the remote
  _root/tasks/tsk_162d…9666.json
lore live: pushed waiting live writes
$ LORE_LIVE_AUTOPUSH=0 lore task claim tsk_162d…9666
ERROR: [E_OFFLINE] could not confirm the coordination write (autopush is off (live.autopush / LORE_LIVE_AUTOPUSH=0))
  hint: coordination writes are never queued; reconnect (or turn live.autopush on) and run the command again
(exit 15)
$ lore sync
· already in sync
```
<!-- /example -->

In a clone whose remote is not called `origin`, lore finds no live rows until `LORE_LIVE_REMOTE` names the remote:

<!-- example: environment#live-remote -->
```console
# alice, in ~
$ git clone -q -o upstream https://github.com/client/website website-fork && cd website-fork && git remote
upstream

# alice, in website-fork
$ lore task list
(no tasks)
$ LORE_LIVE_REMOTE=upstream lore task list
○ tsk_2ad3…f891 Check the contact form on a phone [medium]
○ tsk_162d…9666 Renew the SSL certificate [medium]
```
<!-- /example -->

`LORE_TRACE=1` prints each git command with how long it took (the times are shown as `<time>` here, because they differ on every run):

<!-- example: environment#trace -->
```console
# alice, in website
$ LORE_TRACE=1 lore task list 2>&1 | grep '^trace:' | head -6
trace: git rev-parse --is-bare-repository --show-object-format (<time>, ok)
trace: git rev-parse --show-toplevel (<time>, ok)
trace: git rev-parse --path-format=absolute --git-common-dir (<time>, ok)
trace: git config --get remote.origin.url (<time>, ok)
trace: git config --get user.name (<time>, ok)
trace: git config --get user.email (<time>, ok)
```
<!-- /example -->

## Who you are and which org

<!-- ds:block id=team-env-org-m38knvc4 -->
<!-- ds:block id=team-env-actor-53pv3fuc -->
| Variable | What it does |
|---|---|
| `LORE_ORG` | use this org entry (a name under `orgs:` in `~/.lore/config.yaml`) for this shell or one command; it is checked first, before every other way lore picks an org |
| `LORE_ACTOR` | who is writing, for agents: `agent:<label>`. `lore agent start` prints the line to export. In an org repo the agent must be on the org's people list (the roster). Approving a gate is refused while it is set, because a gate is approved by a person, never by an agent |
| `LORE_SESSION` | the agent's session id, recorded on its claims; `lore agent start` prints it too |

## Output

| Variable | What it does |
|---|---|
| `NO_COLOR` | any non-empty value turns colours off (when `--color` is `auto`) |
| `FORCE_COLOR` | any non-empty value turns colours on (when `--color` is `auto` and `NO_COLOR` is not set) |
| `OLLAMA_HOST` | address of a local model server for `lore skill compile` (default `localhost:11434`; `http://` is added when missing) |

Into a pipe lore prints no colours; `FORCE_COLOR` turns them on (`cat -v` shows the colour codes), and `NO_COLOR` wins when both are set. The example shell sets `NO_COLOR` for every command, hence the `unset`:

<!-- example: environment#colour -->
```console
# alice, in website
$ unset NO_COLOR; lore doctor | head -1 | cat -v
Status: HEALTHY
$ unset NO_COLOR; FORCE_COLOR=1 lore doctor | head -1 | cat -v
Status: ^[[92mHEALTHY^[[0m
$ NO_COLOR=1 FORCE_COLOR=1 lore doctor | head -1 | cat -v
Status: HEALTHY
```
<!-- /example -->

<!-- ds:block id=team-ci-markers-t4gm77bb -->
**CI variables.** When any of `CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `BUILDKITE`, `CIRCLECI`, `JENKINS_URL`, `TF_BUILD` or `BITBUCKET_BUILD_NUMBER` is set, lore treats its output as something other people may read: messages about stores and leak checks then show `<redacted>` instead of repo, store and org names. The same happens when the output does not go to a terminal (for example into a file or a pipe).

## Set by lore itself

| Variable | What it is |
|---|---|
| `LORE_RIDE_ACTIVE` | lore sets it while it makes a ride-along commit (a commit that carries waiting notes to main), so its own git hooks do not run twice. Never set it by hand |
| `LORE_NOW` | a fixed clock for lore's own test builds only; normal lore ignores it |

## Pitfalls

- `LORE_SYNC=0` also turns live rows off, so `LORE_LIVE=1` does nothing next to it.
- `LORE_LIVE_AUTOPUSH=0` does not queue claims: a claim that cannot reach the server fails with `E_OFFLINE` instead of waiting.
- `LORE_HOME` is easy to misread. Setting it does not move `~/.lore`; use a different `HOME` if you need a separate lore setup.
- `TRUE`, `yes` or `on` are not `1`: lore ignores them and the switch stays at its default.
- An empty `NO_COLOR=` counts as not set.

## Related

- [Flags](flags.md): `--db`, `--project`, `--repo`, `--read-only`, `--trace` and `--color` do the same as some of these variables, for one command.
- [Config files](config.md): the lasting settings in `~/.lore/config.yaml`.
- [Error codes](errors.md): the codes named above.
- [Team command reference](../team/commands.md#environment-variables).
