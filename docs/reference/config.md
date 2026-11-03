# Config files

**In short:** lore reads a few small settings files. The main one is `~/.lore/config.yaml` on your own machine: it says who you are and which companies (orgs) you work for. Every key is optional, and an empty or missing file is fine. `lore config check` reads the file and lists every mistake in it.

This page lists every file and every key, with its default and a plain one-line meaning. For step-by-step setup, see the [team guide](../team/README.md).

## The files at a glance

| File | Where it lives | Shared with others? | Written by | What it holds |
|---|---|---|---|---|
| `~/.lore/config.yaml` | your home folder | no, only you can read it (file mode 0600) | you; `lore org use`, `lore org import` and `lore identity set --org` add to it | who you are, your orgs, their stores, timing settings |
| `~/.lore/identity.yaml` | your home folder | no (mode 0600) | `lore identity set` | your fallback name and identity key |
| `.lore/org.yaml` | in a repo the org owns | yes, it is committed | `lore org set` | the org's name, and optionally the project folder |
| `lore-store.yaml` | at the top of a store repo | yes, everyone who can open the store | `lore store init` | the marker that makes a repo a store |
| `<org_folder>/.lore/lore-org.yaml` | inside a store | yes, everyone who can open the store | a team member, by hand | the org entry the team suggests; `lore org import` copies it into your config |
| `.lore/lore.yaml` | in a project folder | it is committed if you commit it | `lore project shared-init` | a pointer to a shared database instead of `.lore/lore.db` |
| database settings | inside `.lore/lore.db` | no | lore itself; `lore config set` | internal values such as the schema version |

A **store** is a separate repo that holds only lore notes, so private notes never sit in a repo the client can read. An **org** is one company you work for, set up as one entry under `orgs:`.

## How the files are read

- **Format.** Every file is YAML. Older lore versions wrote TOML (`config.toml`, `identity.toml`, `lore.toml`). When lore finds only the `.toml` file, it converts it to `.yaml` once, keeps the old file as `<name>.toml.migrated`, and prints one line. When both exist, the `.yaml` file wins and lore warns that the `.toml` file is ignored.
- **Durations** (how long something lasts) are written like `60s`, `20m`, `1h30m` or a whole number of days such as `7d`. A plain number means seconds. A mix of days and another unit, such as `1d12h`, is refused: write `36h` instead. Negative values are refused.
- **Org settings win over top-level settings.** The blocks `live`, `lease`, `agents` and `merge_check` can be set at the top of the file and again inside one `orgs:` entry. The org entry's value wins for repos of that org; anything it does not set comes from the top level, then from the built-in default.
- **A bad file stops writes.** When `~/.lore/config.yaml` has a mistake, commands that read notes still work, but writing a note stops with `E_CONFIG_INVALID` until you fix the file.

## `~/.lore/config.yaml`

This is a complete, valid example. It sets most keys only to show where they go; you need far fewer. Alice (an agency developer) works for the agency and in client repos:

<!-- example: reference#config-check-good -->
```console
# alice, in ~
$ cat .lore/config.yaml
# Everything below is optional; this file sets most keys to show where they go.
identity:
  key: human:alice@agency.example
  name: Alice
personal:
  store: local
orgs:
  - name: agency
    identity:
      key: human:alice@agency.example
      name: Alice
      handle: alice
    stores:
      - url: github.com/agency/notes
        org_folder: "."
    own:
      - github.com/agency/*
    repos:
      - github.com/client/*
      - github.com/client2/**
    defaults:
      client_repos:
        task: agency
        memory: agency/website
    agent_details: internal
    lease:
      ttl: 30m
live:
  max_age: 7d
  fetch_ttl: 60s
  autopush: true
  compact_after: 1000
  remote: origin
  defaults:
    memories: true
lease:
  ttl: 20m
  renew_every: 5m
  grace: 2m
  push_retries: 5
agents:
  max_claims_per_agent: 1
  max_attempts: 3
merge_check:
  adapter: none
link_index:
  max_age: 1h
untrusted_hosts:
  - build-*.client.example

# alice, in website
$ lore config check
✓ ~/.lore/config.yaml is valid: 1 org entry (agency)
```
<!-- /example -->

### Top-level keys

| Key | Default | What it means |
|---|---|---|
| `identity.key` | none | who you are in repos that match no org entry, written `human:<your email>`; your identity key is your email |
| `identity.name` | none | your name in those repos |
| `identity.handle` | none | your short name after `@` (lowercase letters, digits, `_` or `-`) |
| `personal.store` | `local` | where your personal notes live: `local` means `~/.lore/personal`, or give the git URL of your own private store |
| `orgs` | none | one entry per company you work for (see below) |
| `repo_orgs.<project id>` | none | this machine's org choice for one project; `lore org use` writes it, you do not need to |
| `default_branch` | read from `origin/HEAD`, else `main` | the branch lore treats as main |
| `live` | see below | live rows: notes everyone sees at once, before any commit |
| `lease` | see below | how long an agent's claim on a task lasts |
| `agents` | see below | limits for agents |
| `merge_check` | see below | how lore learns that a finished task's pull request was merged |
| `link_index` | see below | how long lore trusts its saved list of linked client repos |
| `untrusted_hosts` | none | names of machines the client owns, such as `build-*.client.example`; `lore doctor` warns when your private stores are set up on one of them |

When two org entries match a repo, `lore org use` records this machine's choice under `repo_orgs`:

<!-- example: reference#org-use -->
```console
# alice, in website
$ lore whoami
org        refused  (the patterns of 2 org entries match this repo (agency, freelance) (candidates: agency, freelance))
identity   (not resolved: the org is refused above)
project    prj_c0f3…b5b8
remotes    github.com/client/website
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   local: everywhere/
ERROR: [E_ORG_AMBIGUOUS] the patterns of 2 org entries match this repo (agency, freelance) (candidates: agency, freelance)
  hint: choose one with `lore org use <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns
(exit 17)
$ lore org use agency
✓ prj_c0f3…b5b8 uses org agency on this machine
  recorded in ~/.lore/config.yaml (machine-only, nothing committed)

# alice, in ~
$ grep -A3 repo_orgs ~/.lore/config.yaml
repo_orgs:
  prj_c0f3…b5b8: {org: agency, remotes: [github.com/client/website]}

# alice, in website
$ lore org link agency --project client --yes
This marks github.com/client/website as a CLIENT repo of agency: internal notes default to github.com/agency/notes client/,
and the leak guards turn on for everyone in agency.
  link: project prj_c0f3…b5b8 at github.com/client/website → github.com/agency/notes@main:client/ (default=false)
✓ github.com/client/website (prj_c0f3…b5b8, github.com/client/website) linked to github.com/agency/notes client/ (created; pushed)
  nothing was written to this repo
```
<!-- /example -->

`default_branch` sends finished live rows to another branch than the one `origin/HEAD` names. Here they land on `develop`, and `main` is left alone (the message still says "main"):

<!-- example: reference#default-branch -->
```console
# alice, in ~
$ cat .lore/config.yaml
default_branch: develop

# alice, in website
$ lore task add 'Fix the footer links'
✓ tsk_05e9…4603 — Fix the footer links [medium]
lore live: (live, pushed)
$ lore task done tsk_05e9…4603
✓ tsk_05e9…4603 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed)
lore live: landed on main (601d285); leaves live once main is fetched
$ git fetch -q origin && git log --oneline origin/develop -2 && git log --oneline origin/main -1
601d285 lore: land 2 rows (_root)
86dbae4 lore: init
86dbae4 lore: init
```
<!-- /example -->

### Keys of one org entry (`orgs:`)

| Key | Default | What it means |
|---|---|---|
| `name` | required | the entry's name; also what you type after `--to`. Lowercase letters, digits, `.` or `-`, at most 64 characters. `repo`, `me` and `everywhere` are taken, and names may not start with `_` |
| `identity.key` | required when the entry is used | who you are in this org: `human:<your email in this org>` |
| `identity.name`, `identity.handle` | none | your name and `@handle` in this org |
| `stores[].url` | required | a store repo of this org, such as `github.com/agency/notes` |
| `stores[].branch` | `main` | the store branch this entry uses; no `/` in the name. A branch other than the store's main branch makes this a staging entry |
| `stores[].org_folder` | none | set on exactly one store: the folder that holds the org's people list (the roster), teams and company rules; `.` means the top of the store |
| `own` | none | patterns for the org's own code repos, such as `github.com/agency/*` |
| `repos` | none | patterns for client repos the org works in, such as `github.com/client/*` |
| `defaults.client_repos.<kind>` | the linked internal project | where a new row of this kind goes in a client repo when you give no `--to`: `repo`, the org's name, or `<org>/<folder>`. Another org's name is refused |
| `agent_details` | `internal` | where agent work notes go in a client repo: `internal` keeps them in the agency's store, `client` writes them to the client repo |
| `live`, `lease`, `agents`, `merge_check` | the top-level values | the same blocks as at the top level, for this org only |

**Repo patterns.** A pattern is a repo address with `*` for exactly one part of the path and `**` for any number of parts. `github.com/client/*` matches `github.com/client/website` but not `github.com/client/web/site`; `github.com/client2/**` matches every repo under `client2`. The host (`github.com`) may not be a wildcard, `**` may appear once, and a part is either plain text or a whole wildcard (`web*` is refused). You can also paste a full remote URL as an exact pattern.

### Timing and limit blocks

<!-- ds:block id=team-config-defaults-vcrgzmhf -->
| Key | Default | What it means |
|---|---|---|
| `live.max_age` | `7d` | an open live row is copied to `main` once it is this old, and again every 7 days while it still differs |
| `live.fetch_ttl` | `60s` | lore fetches once per command, cached 60 s: a command skips the fetch when the last one is younger than this. `lore sync` always fetches |
| `live.autopush` | `true` | push live writes at once; `false` keeps them on your machine until you run `lore sync` |
| `live.compact_after` | `1000` | how many commits the live list may collect before lore squeezes it into one |
| `live.remote` | `origin` | the git remote that holds the live rows |
| `live.defaults.<kind>` | see the next table | whether rows of this kind are live without `--live` |
| `lease.ttl` | `20m` | how long a claim lasts without a renewal (a heartbeat) |
| `lease.renew_every` | `5m` | how often an agent renews its claim; must be shorter than `ttl` |
| `lease.grace` | `2m` | extra time after a claim runs out before others may take the task |
| `lease.push_retries` | `5` | how many times lore retries a push the remote turned down |
| `agents.max_claims_per_agent` | `1` | how many tasks one agent may hold at once |
| `agents.max_attempts` | `3` | how many agents may try a task before it is marked `blocked` |
| `merge_check.adapter` | `none` | `none` learns about merges from commits only; `github` also asks the `gh` tool about merged pull requests |
| `link_index.max_age` | `1h` | how old the saved list of linked client repos may be when the store cannot be reached |

<!-- ds:block id=team-live-kinds-62s7pcxa -->
Kinds that are live by default (the keys are table names, as in `live.defaults.memories: true`):

| Kind | Live by default |
|---|---|
| `tasks`, `missions`, `handoffs`, `comments` | yes |
| `rules`, `decisions`, `memories` | no |

### What `lore config check` refuses

`lore config check` names every problem at once, with where it is:

<!-- example: reference#config-check-bad only=2 -->
```console
# alice, in website
$ lore config check
✗ ~/.lore/config.yaml
  - lease: renew_every: must be shorter than ttl, or every claim expires before it is renewed
  - merge_check: adapter "gitlab": must be "none" or "github"
  - orgs[0] "Agency": name: use lowercase letters, digits, '.' or '-', starting with a letter or digit
  - orgs[0] "Agency": identity.key "alice@agency.example": must be human:<your email in that org> (TEAM-029)
  - orgs[0] "Agency": stores[0].branch "team/staging": a store branch may not contain "/" (refs/lore/live/<branch> must never clash with another branch)
ERROR: [E_CONFIG_INVALID] ~/.lore/config.yaml is not valid
  hint: fix the problems listed above; nothing else in lore reads an invalid config
(exit 1)
```
<!-- /example -->

The rules it checks, in plain words:

| Key | Rule |
|---|---|
| `identity.key`, `orgs[].identity.key` | must be `human:` followed by an email address |
| `identity.handle`, `orgs[].identity.handle` | lowercase letters, digits, `_` or `-`, no `@` |
| `personal.store` | `local` or a git URL |
| `orgs[].name` | see the org entry table; two entries may not share a name |
| `orgs[].stores[]` | a valid URL; two items may not name the same store and branch; at most one has `org_folder` |
| `orgs[].stores[].branch`, `default_branch` | a valid git branch name; a store branch may not contain `/` |
| `orgs[].stores[].org_folder` | `.` or one folder name (letters, digits, `.`, `_`, `-`), not starting with `_` |
| `orgs[].own`, `orgs[].repos` | valid repo patterns |
| `orgs[].defaults.client_repos` | keys are lowercase kind names; values are `repo`, this org's name or `<org>/<folder>` |
| `orgs[].agent_details` | `internal` or `client` |
| `repo_orgs` | each key is a project id and each value names an entry under `orgs:` |
| `live.max_age`, `lease.ttl`, `lease.renew_every`, `link_index.max_age` | greater than zero |
| `lease.renew_every` | shorter than `lease.ttl` (checked when both are set in the same block) |
| `live.compact_after`, `agents.max_claims_per_agent`, `agents.max_attempts` | at least 1 |
| `lease.push_retries` | not negative |
| `live.remote` | a remote name such as `origin` (no spaces, `/` or `:`) |
| `live.defaults` | keys are lowercase table names such as `tasks` |
| `merge_check.adapter` | `none` or `github` |
| `untrusted_hosts` | no empty pattern |

A typing mistake that YAML itself cannot read, such as a bad duration, is reported alone, with its line number. Fix it and run the check again to see the rest:

<!-- example: reference#config-check-typo -->
```console
# alice, in website
$ lore config check
✗ ~/.lore/config.yaml
  - lorecfg: load ~/.lore/config.yaml: line 2: invalid duration "1d12h": time: unknown unit "d" in duration "1d12h"
ERROR: [E_CONFIG_INVALID] ~/.lore/config.yaml is not valid
  hint: fix the problems listed above; nothing else in lore reads an invalid config
(exit 1)
```
<!-- /example -->

## `~/.lore/identity.yaml`

Your fallback identity, used when no org entry applies and no other source names you. `lore identity set` writes it and `lore identity unset` removes it.

| Key | Default | What it means |
|---|---|---|
| `display_name` | none | your name as lore shows it |
| `stable_key` | `human:<display_name>` | your identity key; for a person, `human:<your email>` |

## `.lore/org.yaml`

A committed file in a repo the org owns. It names the org so everyone who clones the repo gets the same org without any setup. It holds names only, never a store address or a person, so it tells a reader nothing about where the org keeps its notes. lore accepts it only in repos that match the org's `own:` patterns and are not linked as a client repo. `lore org set` writes it.

| Key | Default | What it means |
|---|---|---|
| `org` | required | the org's name, as in `orgs[].name` |
| `project` | none | the internal project folder in the org's store (one folder name) |

## `lore-store.yaml`

<!-- ds:block id=team-store-allowed-yhy36m4v -->
The marker at the top of a store repo. Its presence is what makes a repo a store; `lore store init` writes it. Besides this marker, a store may hold only `README*`, `LICENSE*`, `.gitattributes`, `.github/workflows/lore-*.yml` and `.lore/` folders (at the top or one folder down). lore refuses a store that holds anything else, with `E_NOT_A_STORE`.

| Key | Default | What it means |
|---|---|---|
| `lore_store` | required | the marker format version, a whole number of at least 1 (lore writes `1`) |
| `created` | written by `lore store init` | the day the store was made, `YYYY-MM-DD` |
| `org_folder` | none | the folder suggested for the roster and company rules; only a hint, the org entry's `org_folder` decides |

`lore store init` writes the marker as the store's first commit:

<!-- example: reference#store-marker only=2,4 -->
```console
# alice, in ~
$ lore store init git@github.com:agency/notes
✓ wrote lore-store.yaml to github.com/agency/notes (new branch main, first commit) and pushed
$ git clone -q https://github.com/agency/notes notes-checkout && cat notes-checkout/lore-store.yaml
lore_store: 1
created: "2026-11-02"
org_folder: org
```
<!-- /example -->

## `<org_folder>/.lore/lore-org.yaml`

The org entry a team suggests to its members, kept in the store's org folder. It never holds a person's identity. `lore org import` copies it into your `~/.lore/config.yaml` and adds your identity; lore also reads `defaults.client_repos` from it every time, and the team's value wins over a looser one in your own file.

| Key | What it means |
|---|---|
| `name` | the suggested entry name |
| `stores[]` | the org's stores, with `url`, `branch` and `org_folder` as in your config |
| `own`, `repos` | the repo patterns |
| `defaults.client_repos.<kind>` | where rows of each kind go in client repos |

## `.lore/lore.yaml` (shared database)

Normally a project keeps its notes in `.lore/lore.db`. With this pointer file instead, the project uses a database somewhere else, which several projects can share. `lore project shared-init` writes it. A folder may hold either `.lore/lore.db` or this file, never both (`E_AMBIGUOUS_PROJECT`). Older versions called it `.lore/lore.toml`; lore converts that file on first read.

| Key | Default | What it means |
|---|---|---|
| `db_path` | required | where the shared database is. You may write `${HOME}`, `${LORE_HOME}` (the value of `LORE_HOME`, else `~/.lore`) and `${PROJECT_ROOT}` (this project's folder). A `..` part is refused unless `LORE_ALLOW_DB_PATH_OUTSIDE=1` |
| `project_id` | required | which project inside that database this folder is |

## Database settings (`lore config`)

`lore config get`, `set` and `list` read and write small settings stored inside the database, not in a file. lore writes and reads these keys itself; `lore config set` stores any other key you give it, but lore does not act on keys it does not know.

<!-- example: reference#db-config -->
```console
# alice, in website
$ lore config list
db_created_at=2026-11-02T10:00:00.000000001Z
db_uuid=cfg_54fe…28f2
fts_registry_fingerprint=a98a26bd082406f5
last_checkpoint_at=
last_vacuum_at=
schema_version=1
$ lore config get schema_version
1
```
<!-- /example -->

| Key | What it is |
|---|---|
| `schema_version` | the database layout version |
| `db_uuid` | this database's own id |
| `db_created_at` | when `lore init` made the database |
| `fts_registry_fingerprint` | which search index layout was last built; `lore setup` warns when it is out of date |
| `last_checkpoint_at`, `last_vacuum_at` | when lore last tidied the database file |

## Pitfalls

- A mistake in `~/.lore/config.yaml` blocks writing notes, in every repo, until you fix it; reading still works. Run `lore config check` after each edit.
- `lease.renew_every` is checked against `lease.ttl` only when both are set in the same block. If you shorten `ttl` in an org entry, set `renew_every` there too.
- A store branch name may not contain `/`. Use `staging`, not `team/staging`.
- `identity.key` is `human:` plus your email, not your email alone and not your name.
- `1d12h` is refused; write `36h`.
- `LORE_HOME` does not move `~/.lore`: lore always keeps its config, identity and caches under `~/.lore` in your home folder. `LORE_HOME` only fills in `${LORE_HOME}` in `db_path`.

## Related

- [Environment variables](environment.md): switches that change the same behaviour for one shell or one command.
- [Error codes](errors.md): `E_CONFIG_INVALID`, `E_NOT_A_STORE` and the rest.
- [Team guide](../team/README.md) and [team command reference](../team/commands.md#config-keys).
- [Command index](commands.md).
