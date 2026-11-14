# Environment variables

**In short:** an environment variable is a setting you give one shell or one command, like `LORE_READ_ONLY=1 lore doctor`. lore reads a few of them to change what one run does: which database to use, whether it may write, whether it may use the network. None of them is needed for normal use.

Set one for a single command by writing it in front (`LORE_SYNC_QUIET=1 lore sync`), or for the whole shell with `export LORE_SYNC_QUIET=1`. Unless a row says otherwise, a switch is on only with the exact value `1` and off only with the exact value `0`; any other value is the same as not setting it.

## Which database, project and repo

A row command reads its rows from the repository it runs in. It picks its project and repo in this order, and the first one found wins: the flag (`--project`, `--repo`), then the variable below, then `.lore/lore.yaml`, then the `.lore/data` of the git checkout the current folder is in.

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
  hint: cd into a git checkout; a write can also name its store from here (--to <org>/<folder>, --to me; lore store list shows them)
(exit 19)
$ cd work/website && lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
```
<!-- /example -->

`LORE_DB` is refused the same way as `--db`. `LORE_PROJECT_ID` picks the project by its `prj_` id (from `lore project list`); a value that names no project stops the command with `E_NOT_FOUND`, it never falls back to the folder's project:

<!-- example: reference#project-id -->
```console
# alice, in website
$ LORE_DB=.lore/lore.db lore rule list
ERROR: [E_UNSUPPORTED] LORE_DB is not supported by this command: its rows are row files read from the repository, not lore.db
  hint: unset LORE_DB and run the command inside the repository (or name a store with --store / --to)
(exit 23)
$ lore project list
prj_d431…313a  website  https://github.com/client/website
$ LORE_PROJECT_ID=$(lore project list | cut -d' ' -f1) lore rule list
rul_54fe…28f2 [must/always] Run the tests before you push
$ LORE_PROJECT_ID=prj_website lore rule list
ERROR: [E_NOT_FOUND] rule: no project "prj_website"
  hint: run `lore project list` to see the projects
(exit 27)
```
<!-- /example -->

Older lore could point a repo at a shared database file with a pointer, `.lore/lore.yaml`; 0.1.18 converts such a repo with `lore migrate run` ([One store for several repos](../sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database)). The pointer below names `${LORE_HOME}`, so `LORE_HOME` decides which database file is converted, and the new store lands beside it. lore's own folder stays `~/.lore` either way:

<!-- example: environment#lore-home -->
```console
# alice, in docs
$ cat .lore/lore.yaml
db_path: ${LORE_HOME}/client-docs.db
project_id: prj_f147…5139
$ lore migrate run --dry-run
ERROR: [E_NOT_FOUND] migrate: the shared lore.db ~/.lore/client-docs.db named by ~/work/docs/.lore/lore.yaml is not there
(exit 27)
$ LORE_HOME=~/shared-dbs lore migrate run
lore: added .lore/lore.db, .lore/lore.db-shm, .lore/lore.db-wal, .lore/lore.db.broken.*, .lore/state/, .lore/backups/, .lore/lock to this clone's .git/info/exclude (never committed) so lore's local files stay out of git status; this repo holds no lore data, so lore changes none of its tracked files (`lore where` lists everything lore keeps here)
Upgraded 1 row
  mode-b: rules/rul_2537…6bbb → store shared (~/shared-dbs/stores/blog)
  mode-b: .lore/stores.yaml: add store shared file://~/shared-dbs/stores/blog
  mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
team lock: none (this machine's migration lock only; nothing written to the remote for it); `--team-lock` also holds refs/lore/signals on the remote while the run lasts, so a teammate cannot migrate at the same time
backup: refs/lore-backup/migrate/2026-11-02 (local only, never pushed)
undo: none for a shared-database conversion (`lore migrate rollback` refuses it); the shared lore.db was not changed
$ lore rule list
lore rule list: git is not set up for lore's row files in this clone: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/docs/.git/config; hook blocks in ~/work/docs/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
rul_2537…6bbb [must/always] Posts go out on Tuesdays [shared]

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
project_id: prj_f147…5139
$ lore migrate run --dry-run
ERROR: [E_BAD_PATH] migrate: db_path "../client.db" has a `..` part
  hint: set LORE_ALLOW_DB_PATH_OUTSIDE=1 to allow it
(exit 20)
$ LORE_ALLOW_DB_PATH_OUTSIDE=1 lore migrate run
lore: added .lore/lore.db, .lore/lore.db-shm, .lore/lore.db-wal, .lore/lore.db.broken.*, .lore/state/, .lore/backups/, .lore/lock to this clone's .git/info/exclude (never committed) so lore's local files stay out of git status; this repo holds no lore data, so lore changes none of its tracked files (`lore where` lists everything lore keeps here)
Upgraded 1 row
  mode-b: rules/rul_2537…6bbb → store shared (~/work/stores/blog)
  mode-b: .lore/stores.yaml: add store shared file://~/work/stores/blog
  mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
team lock: none (this machine's migration lock only; nothing written to the remote for it); `--team-lock` also holds refs/lore/signals on the remote while the run lasts, so a teammate cannot migrate at the same time
backup: refs/lore-backup/migrate/2026-11-02 (local only, never pushed)
undo: none for a shared-database conversion (`lore migrate rollback` refuses it); the shared lore.db was not changed
$ lore rule list
lore rule list: git is not set up for lore's row files in this clone: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/blog/.git/config; hook blocks in ~/work/blog/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
rul_2537…6bbb [must/always] Posts go out on Tuesdays [shared]
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
lore rule list: git is not set up for lore's row files in this clone: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/Dropbox/website/.git/config; hook blocks in ~/work/Dropbox/website/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
ERROR: [E_NETWORK_FS] rowfs: the store is inside a "dropbox" folder (cloud sync)
  hint: move the store to a local folder, or set LORE_ALLOW_NETWORK_FS=1 to accept the risk
(exit 23)
$ LORE_ALLOW_NETWORK_FS=1 lore rule list
(no rules)
```
<!-- /example -->

## Sync with git (`.lore/data`)

lore copies your notes into `.lore/data`, the folder git commits, before and after each command. These switches change that.

<!-- ds:block id=sync-env-pe9vvf3f -->
| Variable | What it does |
|---|---|
| `LORE_SYNC` | `0` turns live rows off for this command or shell (like `LORE_LIVE=0`) and stops lore updating the version pin in `.github/workflows/lore-sync-merge.yml`. Rows are still written to `.lore/data`: since 0.1.18 the files are where lore keeps your notes |
| `LORE_SYNC_GIT` | `0` keeps syncing the files but stops lore from running git setup by itself: no hooks, no `.gitattributes`, no git config, no workflow pin update |
| `LORE_SYNC_QUIET` | `1` hides sync's progress lines; errors still print |
| `LORE_SYNC_ALLOW_SECRETS` | removed in 0.1.18 and ignored; `lore doctor` warns while it is still set. A row you confirmed with `--not-a-secret --reason "<why>" --yes` is already in `.lore/data`; lore's commit hook still refuses a file that holds a credential pattern |

## Live rows and the network

A **live row** is a note everyone sees at once, before any commit. lore keeps live rows on a hidden list on the git server that is not a branch (git calls it a *ref*).

<!-- ds:block id=sync-env-pe9vvf3f -->
| Variable | What it does |
|---|---|
| `LORE_LIVE` | `0` turns live rows off in this shell: every row waits for a commit, as before live rows existed |
| `LORE_ALLOW_ROW_DROP` | `1` lets a forced `git push` take lore rows off the remote branch (the pre-push hook refuses it otherwise and lists the rows; see [Rewriting history](../sync/hooks-and-merge-driver.md#rewriting-history-rebase-amend-force-push)) |
| `LORE_LIVE_AUTOPUSH` | `0` means lore never uses the network by itself (same as `live.autopush: false`): live writes and store writes wait on your machine until `lore sync` whatever the store's push mode ([How writes reach the remote](../team/identity-and-stores.md#how-writes-reach-the-remote)), and writes that must reach the server at once, such as claiming a task, fail with `E_OFFLINE` |
| `LORE_PUSH_CLAIMS` | `1` is your standing go-ahead to push claims and holds (`lore task claim`, `lore task next`, `lore hold claim`) to a code repo's remote at once, as `--push` gives it for one command (`live.autopush: true` in `config.yaml` gives it too); without any of them, a claim there is refused before anything is written. Set it in an agent's environment when the agent should claim work by itself |
| `LORE_LIVE_REMOTE` | the git remote that holds the live rows (default `origin`), for forks and tests |
| `LORE_TRACE` | `1` prints every git command lore runs for live rows, with timing, like `--trace` |

`LORE_LIVE=0` keeps a new task out of the live list: it is written to `.lore/data` and waits for a commit, while the next task without the switch goes live as usual:

<!-- example: environment#live-off -->
```console
# alice, in website
$ LORE_LIVE=0 lore task add --title 'Update the privacy page'
lore: wrote ~/work/website/.lore/data/task_lists/tlt_7a67…9504.json
lore: wrote ~/work/website/.lore/data/tasks/tsk_2ad3…f891.json
✓ tsk_2ad3…f891 — Update the privacy page [medium]
  created tasklist tlt_7a67…9504 "default" for tasks added without --tasklist
$ git status --short
 M .lore/data/_meta.json
?? .lore/data/task_lists/
?? .lore/data/tasks/
$ lore task add --title 'Check the contact form on a phone'
lore: wrote tasks/tsk_162d…9666 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_162d…9666.json is unchanged)
✓ tsk_162d…9666 — Check the contact form on a phone [medium]
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
lore live: (live, kept here: `lore sync` or your next `git push` sends live writes; `lore config set --key live.autopush --value true` sends each write at once)
$ lore live show
task_lists tlt_7a67…9504     live      since 2026-11-02 10:00
tasks      tsk_162d…9666     live      since 2026-11-02 10:00
2 live rows in _root (epoch 1)
```
<!-- /example -->

`LORE_LIVE_AUTOPUSH=0` keeps a live write on this machine until `lore sync` pushes it, and refuses a claim, which must reach the server at once:

<!-- example: environment#autopush-off -->
```console
# alice, in website
$ LORE_LIVE_AUTOPUSH=0 lore task add --title 'Renew the SSL certificate'
lore: wrote tasks/tsk_54fe…28f2 on this clone's refs/lore/live (a live row, not pushed yet; ~/work/website/.lore/data/tasks/tsk_54fe…28f2.json is unchanged)
✓ tsk_54fe…28f2 — Renew the SSL certificate [medium]
  on tasklist tlt_7a67…9504 "default" (the default; --tasklist picks another)
$ lore live diff
local     601d2850ffd0
tracking  (none)
seen      (none)
pending   3 write(s) not confirmed by the remote
  _root/task_lists/tlt_7a67…9504.json
  _root/tasks/tsk_162d…9666.json
  _root/tasks/tsk_54fe…28f2.json
$ LORE_LIVE_AUTOPUSH=0 lore task claim --id tsk_54fe…28f2 --push
ERROR: [E_OFFLINE] could not confirm the coordination write (autopush is off (live.autopush / LORE_LIVE_AUTOPUSH=0))
  hint: coordination writes (claims, releases, holds) are never queued; turn live.autopush on (or unset LORE_LIVE_AUTOPUSH=0) and run the command again
(exit 15)
$ lore sync
lore live: pushed waiting live writes commit 601d285 to origin refs/lore/live
· in sync; 2 file(s) in .lore/data wait for your commit (.lore/data/_meta.json, .lore/data/tasks/tsk_2ad3…f891.json): git add .lore/data && git commit
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
lore task list: git is not set up for lore's row files in this clone: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/website-fork/.git/config; hook blocks in ~/work/website-fork/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
(no tasks)
$ LORE_LIVE_REMOTE=upstream lore task list
○ tsk_162d…9666 Check the contact form on a phone [medium]
○ tsk_54fe…28f2 Renew the SSL certificate [medium]
```
<!-- /example -->

`LORE_TRACE=1` prints each git command with how long it took (the times are shown as `<time>` here, because they differ on every run):

<!-- example: environment#trace -->
```console
# alice, in website
$ LORE_TRACE=1 lore task list 2>&1 | grep '^trace:' | head -6
trace: git rev-parse --is-bare-repository --show-object-format (<time>, ok)
trace: git rev-parse --path-format=absolute --git-common-dir (<time>, ok)
trace: git config --get remote.origin.url (<time>, ok)
trace: git config --get-regexp ^user\.(name|email)$ (<time>, ok)
trace: git check-ref-format refs/lore/live (<time>, ok)
trace: git for-each-ref --format=%(objectname) %(refname) -- refs/lore/live (<time>, ok)
```
<!-- /example -->

## Who you are and which org

<!-- ds:block id=team-env-org-z6mz7n3q -->
<!-- ds:block id=team-env-actor-6rnbrdhr -->
| Variable | What it does |
|---|---|
| `LORE_ORG` | use this org entry (a name under `orgs:` in `~/.lore/config.yaml`) for this shell or one command; it is checked first, before every other way lore picks an org |
| `LORE_SETUP` | use this setup (a name under `setups:` in `~/.lore/config.yaml`) for this shell, like `--setup`; it wins over the folder, org and default choices, and an unknown name is refused with `E_SETUP_NOT_FOUND` |
| `LORE_ACTOR` | who is writing, for agents: `agent:<label>`. `lore agent start` prints the line to export. In an org repo the agent must be on the org's people list (the roster). Approving a gate is refused while it is set, because a gate is approved by a person, never by an agent |
| `LORE_SESSION` | the agent's session id, recorded on its claims; `lore agent start` prints it too |
| `LORE_HUMAN` | `1` says a person is typing, even inside a coding agent's session: lore still notices the agent (`lore whoami` shows it), but writes are made as you instead of as `<you> via <tool>`. `LORE_ACTOR` wins over it |

lore looks for a coding agent behind every command (its environment variables, then the programs that started lore). When you open a terminal inside an agent's session, your own commands would be written as that agent. `LORE_HUMAN=1` keeps them yours. Here `CODEX_CI=1` stands in for such a terminal (the example shell normally turns detection off, hence the `unset`):

<!-- example: environment#lore-human -->
```console
# alice, in website
$ unset LORE_NO_DETECT LORE_HUMAN; export CODEX_CI=1; lore whoami | grep -E '^(identity|agent) '
identity   alice via codex <agent:auto/codex/10380a>   act_b69e…5ed7  (from: agent detection)
agent      codex  (from: CODEX_CI)
$ unset LORE_NO_DETECT; export CODEX_CI=1 LORE_HUMAN=1; lore whoami | grep -E '^(identity|agent) '
identity   alice <human:alice@agency.example>   act_b4f4…a5b0  (from: git config user.email)
agent      codex  (from: CODEX_CI)  LORE_HUMAN=1: writes stay yours
```
<!-- /example -->

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
Status: HEALTHY
$ NO_COLOR=1 FORCE_COLOR=1 lore doctor | head -1 | cat -v
Status: HEALTHY
```
<!-- /example -->

<!-- ds:block id=team-ci-markers-ec4tqnjs -->
**CI variables.** When any of `CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `BUILDKITE`, `CIRCLECI`, `JENKINS_URL`, `TF_BUILD` or `BITBUCKET_BUILD_NUMBER` is set, lore treats its output as something other people may read: `lore check leaks --as-client` then hides the details of what it found. Store URLs, org folders and local paths are printed as they are in every mode (a store's address is not a secret); only a credential inside a URL is shown as `<redacted>`. A tool session id is masked when the output does not go to a terminal or a CI variable is set.

## Upgrades

| Variable | What it does |
|---|---|
| `LORE_NO_UPGRADE_NOTICE` | `1` turns the once-a-day release check off in this shell: no "a new lore release is out" line, and no `auto_upgrade` either (the config keys `upgrade_notice` and `auto_upgrade` do the same for good). The line is never printed in CI, into a pipe or with `--json` anyway |
| `LORE_VERSION_PIN` | the lore version this shell must use, for example `0.1.18`. It wins over the team pin `lore_version_pin` in `.lore/data/_meta.json`: `lore upgrade` installs that version, `lore upgrade --version` with another one is refused with `E_UPGRADE_PINNED` (unless `--ignore-pin`), and `lore doctor` notes when the running lore is not the pinned one. In CI, `lore upgrade` needs a pin or `--version` |

## Set by lore itself

These are set by lore, or read only by lore's own tests. Never set them for real use.

| Variable | What it is |
|---|---|
| `LORE_RIDE_ACTIVE` | lore sets it while it makes a ride-along commit (a commit that carries waiting notes to main), so its own git hooks do not run twice. Never set it by hand |
| `LORE_NOW` | a fixed clock for lore's own test builds only (it also dates the commits lore makes in a store); normal lore ignores it |
| `LORE_NO_DETECT` | `1` turns coding-agent detection off, in lore's own test builds only (so example output does not depend on where it runs); normal lore ignores it |
| `LORE_HOOK_TIMEOUT` | a longer deadline (a Go duration, e.g. `10m`) for `lore context --hook` in lore's own test builds only, so a busy test machine cannot turn a hook's answer into the 2 s deadline's empty one; normal lore ignores it and keeps 2 s |
| `LORE_GITHUB_API` | the GitHub API address the guided setup's account checks call instead of `https://api.github.com`. It exists for lore's own tests, which point it at a local fake server; never set it for real use |

## Pitfalls

- `LORE_SYNC=0` also turns live rows off, so `LORE_LIVE=1` does nothing next to it.
- `LORE_LIVE_AUTOPUSH=0` does not queue claims: a claim that cannot reach the server fails with `E_OFFLINE` instead of waiting.
- `LORE_HOME` is easy to misread. Setting it does not move `~/.lore`; use a different `HOME` if you need a separate lore setup.
- `LORE_HUMAN=1` changes who a write is made as, not whether lore sees the agent: `lore whoami` still names the agent, and `LORE_ACTOR` set in the same shell still wins.
- `TRUE`, `yes` or `on` are not `1`: lore ignores them and the switch stays at its default.
- An empty `NO_COLOR=` counts as not set.

## Related

- [Flags](flags.md): `--db`, `--project`, `--repo`, `--read-only`, `--trace` and `--color` do the same as some of these variables, for one command.
- [Config files](config.md): the lasting settings in `~/.lore/config.yaml`.
- [Error codes](errors.md): the codes named above.
- [Team command reference](../team/commands.md#environment-variables).
