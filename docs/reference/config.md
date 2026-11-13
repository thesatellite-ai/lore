# Config files

**In short:** lore reads a few small settings files. The main one is `~/.lore/config.yaml` on your own machine: it says who you are and which companies (orgs) you work for. Every key is optional, and an empty or missing file is fine. `lore config check` reads the file and lists every mistake in it.

This page lists every file and every key, with its default and a plain one-line meaning. For step-by-step setup, see the [team guide](../team/README.md).

## The files at a glance

| File | Where it lives | Shared with others? | Written by | What it holds |
|---|---|---|---|---|
| `~/.lore/config.yaml` | your home folder | no, only you can read it (file mode 0600) | you; `lore config set` / `lore config unset`, `lore identity set`, `lore org use` and `lore org import` add to it | who you are, your orgs, their stores, your personal store, timing settings, stricter local limits, your own tool profiles |
| `~/.lore/identity.yaml` | your home folder (v0.1.17 only) | no | v0.1.17 `lore identity set` | removed in 0.1.18: `lore migrate run` moves it into `identity:` of `config.yaml` |
| `.lore/org.yaml` | in a repo the org owns | yes, it is committed | `lore org set` | the org's name, and optionally the project folder |
| `lore-store.yaml` | at the top of a store repo | yes, everyone who can open the store | `lore store init` | the marker that makes a repo a store |
| `<org_folder>/lore-org.yaml` | inside a store | yes, everyone who can open the store | a team member, by hand | the org entry the team suggests; `lore org import` copies it into your config |
| `.lore/lore.yaml` | in a project folder | it is committed if you commit it | v0.1.17 `lore project shared-init` (retired) | an old pointer to a shared database; `lore migrate run` converts it |
| database settings | inside the cache files under `~/.lore/cache/` | no | lore itself | internal values such as the schema version; no command reads or sets them |

A **store** is a separate repo that holds only lore notes, so private notes never sit in a repo the client can read. An **org** is one company you work for, set up as one entry under `orgs:`.

## How the files are read

- **Format.** Every file is YAML. lore 0.1.18 reads no TOML file: a pre-0.1.17 `~/.lore/identity.toml` is ignored (`lore identity show` warns, `lore identity unset` removes it), and an old `.lore/lore.toml` shared-database pointer is read only by `lore migrate run`. A v0.1.17 `~/.lore/identity.yaml` is read until `lore migrate run` moves it into `identity:` of `config.yaml` (the old file is kept as `identity.yaml.migrated`).
- **Durations** (how long something lasts) are written like `60s`, `20m`, `1h30m` or a whole number of days such as `7d`. A plain number means seconds. A mix of days and another unit, such as `1d12h`, is refused: write `36h` instead. Negative values are refused.
- **Org settings win over top-level settings.** The blocks `live`, `lease`, `agents` and `merge_check` can be set at the top of the file and again inside one `orgs:` entry. The org entry's value wins for repos of that org; anything it does not set comes from the top level, then from the built-in default.
- **A bad file stops reads and writes.** When `~/.lore/config.yaml` has a mistake, lore cannot tell which stores a note may come from, so it fails closed: reading and writing notes both stop with `E_CONFIG_INVALID` until you fix the file.

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
  store: git@github.com:alice/lore-me
orgs:
  - name: agency
    identity:
      key: human:alice@agency.example
      name: Alice
      handle: alice
    stores:
      - url: git@github.com:agency/notes
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
upgrade_notice: true
auto_upgrade: false
context:
  profiles:
    - name: mytool
      pointer_file: MYTOOL.md
      import: at
      budget: 20000

# alice, in website
$ lore config check
✓ ~/.lore/config.yaml is valid: 1 org entry (agency)
```
<!-- /example -->

### Top-level keys

| Key | Default | What it means |
|---|---|---|
| `identity.key` | none | who you are in repos that match no org entry, written `human:<your email>`; your identity key is your email. `lore identity set <email>` writes it; this is the only place your identity lives |
| `identity.name` | none | your name in those repos |
| `identity.handle` | none | your short name after `@` (lowercase letters, digits, `_` or `-`) |
| `personal.store` | none (no personal store) | your personal store, the target of `--to me`, shown in all your repos: a git URL of your own private store, the path of a bare git repository on this machine (`/srv/git/lore-me.git`, a git store), or a local folder `file:///<absolute path>`. `lore store init me` with no URL makes `file://~/.lore/stores/_me` and records it here; with another URL it also moves every setup whose own `personal_store` is that local folder to follow this key (`inherit`): a person has one personal store `me`. The v0.1.17 value `local` (`~/.lore/personal`) is still accepted and attached as `me`, in a folder named after your org when one resolves |
| `personal.push` | `now` | how your writes to the personal store reach its remote: `now` pushes each write at once; `batch` keeps them on this machine and pushes them as one commit once `personal.push_every` has passed, on `lore sync` and when a coding tool starts a session; `manual` pushes only on `lore sync`. `lore store set me push=…` writes it; see [How writes reach the remote](../team/identity-and-stores.md#how-writes-reach-the-remote) |
| `personal.push_every` | `5m` | how long a batch may wait before it is pushed, at least `1m` |
| `personal.compact` | `off` | `on`: fold the personal store's history older than `personal.compact_after` into one commit by itself, at most once a day (`lore store compact me` does it by hand) |
| `personal.compact_after` | none | how old history must be before it is folded, at least `1d` (such as `30d`); `personal.compact: on` needs it |
| `orgs` | none | one entry per company you work for (see below) |
| `repo_orgs.<project id>` | none | this machine's org choice for one project; `lore org use` writes it, you do not need to |
| `setups` | none | your setups on this machine: name, kind (`solo`, `member`, `admin`), org, default, folders, identity (solo), github, personal_store, tools, secrets, live; see [Guided setup](../team/guided-setup.md) |
| `repo_setups.<repo id>` | none | this machine's setup pin for one repo; `lore setup use <name> --here` writes it |
| `setup.idle_minutes` | 30 | the setup page stops after this many idle minutes (1 to 1440) |
| `setup.open_browser` | true | `false`: `lore setup` prints the page address only |
| `doctor.scan_depth` | 3 | how deep `lore doctor --all` scans setup folders for repos while no repo is recorded yet (at most 10) |
| `doctor.probe_timeout_s` | 20 | time limit for one ssh or git check of `lore doctor --all`, in seconds (at most 300) |
| `default_branch` | read from `origin/HEAD`, else `main` | the branch lore treats as main |
| `live` | see below | live rows: notes everyone sees at once, before any commit |
| `lease` | see below | how long an agent's claim on a task lasts |
| `agents` | see below | limits for agents |
| `merge_check` | see below | how lore learns that a finished task's pull request was merged |
| `link_index` | see below | how long lore trusts its saved list of linked client repos |
| `untrusted_hosts` | none | names of machines the client owns, such as `build-*.client.example`; `lore doctor` warns when your private stores are set up on one of them |
| `upgrade_notice` | `true` | `false` turns off the once-a-day "a new lore release is out" line |
| `auto_upgrade` | `false` | `true` upgrades lore instead of printing the notice, when lore was installed with Homebrew or the install script |
| `policies.<key>` | none | a stricter limit for this machine only, for any key `lore policy show` lists (see below) |
| `context.profiles` | none | your own tool profiles for `lore render`, added after the built-in ones (see below) |
| `access` | none | how this machine reaches store urls that shared config names in their canonical form: a list of `url` (the prefix shared config uses, scoped to an owner, such as `git@github.com:agency/`) and `via` (the prefix this machine uses instead, such as `git@work-gh:agency/`); see [Reaching stores through your own ssh alias](#reaching-stores-through-your-own-ssh-alias-access) |

When two org entries match a repo, `lore org use` records this machine's choice under `repo_orgs`:

<!-- example: reference#org-use -->
```console
# alice, in website
$ lore whoami
org        refused  (the patterns of 2 org entries match this repo (agency, freelance) (candidates: agency, freelance))
identity   (not resolved: the org is refused above)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_d431…313a
remotes    github.com/client/website
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   not set
ERROR: [E_ORG_AMBIGUOUS] the patterns of 2 org entries match this repo (agency, freelance) (candidates: agency, freelance)
  hint: choose one with `lore org use <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns
(exit 17)
$ lore org use agency
✓ prj_d431…313a uses org agency on this machine
  recorded in ~/.lore/config.yaml (machine-only, nothing committed)

# alice, in ~
$ grep -A4 repo_orgs ~/.lore/config.yaml
repo_orgs:
  prj_d431…313a:
    org: agency
    remotes:
      - github.com/client/website

# alice, in website
$ lore org link agency --project client --yes
lore: pushed store github.com/agency/notes: commit df3a55c to branch main
This marks github.com/client/website as a CLIENT repo of agency: internal notes default to github.com/agency/notes client/,
and the leak guards turn on for everyone in agency.
  link: project prj_d431…313a at github.com/client/website → github.com/agency/notes@main:client/ (default=false)
✓ github.com/client/website (prj_d431…313a) linked to github.com/agency/notes client/ (created; pushed)
  nothing was written to this repo
```
<!-- /example -->

`default_branch` sends finished live rows to another branch than the one `origin/HEAD` names. Here they land on `develop`, the message names `develop`, and `main` is left alone:

<!-- example: reference#default-branch -->
```console
# alice, in ~
$ cat .lore/config.yaml
default_branch: develop

# alice, in website
$ lore task add 'Fix the footer links'
lore: wrote ~/work/website/.lore/data/tasks/tsk_05e9…4603.json
✓ tsk_05e9…4603 — Fix the footer links [medium]
lore live: (live, pushed) commit 9b40ce7 to origin refs/lore/live
$ lore task done tsk_05e9…4603
lore: wrote ~/work/website/.lore/data/tasks/tsk_05e9…4603.json
✓ tsk_05e9…4603 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
lore live: (live, pushed) commit 05513aa to origin refs/lore/live
lore live: landed on develop (dfd8dc8); leaves live once develop is fetched
lore live: landed on develop; this checkout is on main, so those rows show here after you merge develop
$ git fetch -q origin && git log --oneline origin/develop -2 && git log --oneline origin/main -1
dfd8dc8 lore: land 2 rows (_root)
cf563f3 lore: init
cf563f3 lore: init
```
<!-- /example -->

### Setups and their page (`setups:`, `setup:`, `doctor:`)

A **setup** is one way you work on this machine: which folders it covers, who you are there, which GitHub account and key, which coding tools. `lore setup` (the guided setup page) writes these entries, and the [guided setup guide](../team/guided-setup.md) walks through them; you can also write them by hand. `setup:` tunes the page itself and `doctor:` tunes `lore doctor --all`, which checks every setup. One file with a setup, both blocks and a machine policy, checked:

<!-- example: reference#config-check-setups -->
```console
# alice, in ~
$ cat .lore/config.yaml
identity:
  key: human:alice@agency.example
  name: Alice
setups:
  - name: work
    kind: solo
    default: true
    folders:
      - ~/work
    identity:
      key: human:alice@agency.example
      name: Alice
    tools: [claude_code, codex]
setup:
  idle_minutes: 60
  open_browser: false
doctor:
  scan_depth: 4
  probe_timeout_s: 30
policies:
  meta.max_bytes: 4096
  meta.max_bytez: 100

# alice, in website
$ lore config check
✓ ~/.lore/config.yaml is valid: 0 org entries
  warning: ~/.lore/config.yaml: policies.meta.max_bytez is not a policy key lore knows; ignored
$ lore policy show 2>/dev/null | grep meta.max_bytes
meta.max_bytes	4096	machine (~/.lore/config.yaml policies)
```
<!-- /example -->

The keys of one `setups:` entry:

| Key | Default | What it means |
|---|---|---|
| `name` | required | the setup's name, as `--setup` and `LORE_SETUP` take it: lowercase letters, digits, `_` or `-`, starting with a letter, at most 64 characters |
| `kind` | required | `solo` (just you), `member` (you joined an org) or `admin` (you run the org) |
| `org` | none | the org entry under `orgs:` this setup works for |
| `default` | `false` | the setup used where no folder, repo pin or org picks one; at most one setup may set it |
| `folders` | none | the folders this setup covers, absolute (`/…` or `~/…`) |
| `identity` | none | for a `solo` setup: `key`, `name`, `handle` and other `emails`, as in `identity:`; `aliases` lists your earlier keys (lore adds the old one when you change the email, so you stay one actor and a lookup by the old email still finds you) |
| `github` | none | `user`, `host_alias` (an ssh alias of github.com), `ssh_key`, `key_fingerprint`, `route` (`ssh` or `https`; `https` needs `user`) and `gh_account` |
| `personal_store` | `inherit` | the personal store of this setup: `inherit` (the top-level `personal.store`), `none`, or a store URL |
| `tools` | none | the coding tools set up here, by profile name (`claude_code`, `codex`, `cursor`, `gemini_cli`, `copilot`, `generic`, or one of yours under `context.profiles`) |
| `secrets.backend` | none | the password manager this setup uses |
| `live.autopush` | the top-level value | `true` or `false`, for repos of this setup |
| `note` | none | a short note shown in `lore setup list` |
| `disabled` | `false` | `lore setup disable` sets it: the setup stays in the file but matches no folder |

### Keys of one org entry (`orgs:`)

| Key | Default | What it means |
|---|---|---|
| `name` | required | the entry's name; also what you type after `--to`. Lowercase letters, digits, `.` or `-`, at most 64 characters. `repo`, `me` and `everywhere` are taken, and names may not start with `_` |
| `identity.key` | required when the entry is used | who you are in this org: `human:<your email in this org>` |
| `identity.name`, `identity.handle` | none | your name and `@handle` in this org |
| `stores[].url` | required | a store repo of this org, as an address git can fetch: `git@github.com:agency/notes` or `https://github.com/agency/notes` (or `ssh://…`). The bare form `github.com/agency/notes` is refused when the config is read |
| `stores[].name` | the repo name of `url` without `.git`, in lower case (`git@github.com:agency/lore-x.git` is `lore-x`) | the store's short name: `lore store list` shows it in the NAME column, output names the store by it, and store commands and `<store>/<folder>` targets take it (the url still works). Lowercase letters, digits, `.`, `_` or `-`; `team`, `me` and `repo` are taken. Two stores of one entry may not end up with the same short name: set `name` on one of them |
| `stores[].branch` | `main` | the store branch this entry uses; no `/` in the name. A branch other than the store's main branch makes this a staging entry |
| `stores[].org_folder` | none | set on exactly one store: the folder that holds the org's people list (the roster), teams and company rules; `.` means the top of the store |
| `own` | none | patterns for the org's own code repos, such as `github.com/agency/*` |
| `repos` | none | patterns for client repos the org works in, such as `github.com/client/*` |
| `defaults.client_repos.<kind>` | the linked internal project | where a new row of this kind goes in a client repo when you give no `--to`: `repo`, the org's name, or `<org>/<folder>`. Another org's name is refused |
| `agent_details` | `internal` | where agent work notes go in a client repo: `internal` keeps them in the agency's store, `client` writes them to the client repo |
| `live`, `lease`, `agents`, `merge_check` | the top-level values | the same blocks as at the top level, for this org only |

**Repo patterns.** A pattern is a repo address with `*` for exactly one part of the path and `**` for any number of parts. `github.com/client/*` matches `github.com/client/website` but not `github.com/client/web/site`; `github.com/client2/**` matches every repo under `client2`. The host (`github.com`) may not be a wildcard, `**` may appear once, and a part is either plain text or a whole wildcard (`web*` is refused). You can also paste a full remote URL as an exact pattern.

### Reaching stores through your own ssh alias (`access:`)

Shared files (a store's lore-org.yaml, a repo's `.lore/stores.yaml`) name each store by its canonical url, the one every teammate can reach: `git@github.com:agency/notes.git`. When your machine reaches that host through an ssh alias of its own (a `Host work-gh` block with `HostName github.com` in `~/.ssh/config`, for a second GitHub account), say so once in your own config:

```yaml
access:
  - url: "git@github.com:agency/"
    via: "git@work-gh:agency/"
```

Scope each rule to the owner or org the alias is for (`git@github.com:agency/`, or `https://github.com/agency/` for an https url), never to the whole host. A host-wide rule (`url: "git@github.com:"`) sends every github.com repo through the alias's account, so a store of another account, such as your personal store, is refused or pushed as the wrong account. Add one rule per owner the alias is for.

Every git call lore makes to a store whose url starts with `url` then runs with `git -c url.git@work-gh:agency/.insteadOf=git@github.com:agency/ …`, for that one call. lore never writes this into your git config; `git config --global url."git@work-gh:agency/".insteadOf "git@github.com:agency/"` does the same for every program on the machine, if you prefer that.

You rarely write the block by hand. When you type an ssh-alias url to a command that writes shared config (`lore org create`, `lore org import`, `lore store init`, `lore store add`), lore reads `~/.ssh/config`, stores the canonical url in the shared file, adds the `access:` item for your spelling scoped to the url's owner (`git@work-gh:agency/notes.git` records `url: "git@github.com:agency/"`, `via: "git@work-gh:agency/"`), and says so in one line. `lore doctor` (check `store_access`) warns about a shared url that is spelled with your alias, and about a store this machine cannot reach, with the exact owner-scoped `access:` lines and the git command for it; it never rewrites shared config itself.

A host-wide rule already in your config still works, and `lore doctor` warns about it: it names the stores under another owner that go through the alias too (your personal store, say) and prints the owner-scoped lines that replace it. `lore doctor --fix` replaces the rule with one rule per owner of your org stores; doctor alone never rewrites the file. When a git call that went through an access rule fails (clone, fetch, push), the error names the rule (`url → via`, from `~/.lore/config.yaml`) and the next step.

### Stricter limits for this machine (`policies:`)

Team rules (size limits, secret handling and the rest) are set with `lore policy set` and live in the stores. `policies:` adds a machine level below them: each key is a policy key and each value a plain value. It can only tighten: a looser value than the team's, or than the built-in default, is ignored. A key lore does not know is skipped with a notice; a value a known key cannot take stops lore with `E_CONFIG_INVALID`. `lore policy show` names this level `machine`. Edit it by hand: `lore config set` refuses a policy key.

```yaml
policies:
  meta.max_bytes: 4096
```

In the [setups example](#setups-and-their-page-setups-setup-doctor) above, `meta.max_bytes: 4096` shows up in `lore policy show` with the source `machine`, and the misspelled `meta.max_bytez` is only a warning. A value of the wrong type is an error, as in the [refused setups below](#what-lore-config-check-refuses).

### Your own tool profiles (`context.profiles`)

`lore render` writes `.lore/LORE.md` and points each coding tool's own file at it through a *tool profile*. lore ships profiles for the common tools; an entry here adds one for a tool it does not ship. Which profiles a repo uses is read from the pointer files committed in the repo.

| Key | Required | What it means |
|---|---|---|
| `name` | yes | the profile's name, as `lore render --target <name>` takes it; it may not reuse a built-in name |
| `pointer_file` | yes | the file the tool reads, relative to the repo, such as `MYTOOL.md`; no two profiles may share one |
| `import` | yes | how that file pulls in `.lore/LORE.md`: `at` (an `@.lore/LORE.md` line), `read` (an instruction to read it), `mdc` (a Cursor-style rule file) or `none` |
| `budget` | no | how many bytes of notes the tool should get |
| `limit` | no | the tool's own size limit for one file, for the budget warning |
| `private_file` | no | the tool's uncommitted local file that imports your private renders |
| `path_rules` | no | `style` (`paths` or `mdc_globs`) and `dir` (a folder inside the repo, outside `.lore/`) for a tool that loads rule files per path |

A bad entry is `E_CONFIG_INVALID` naming `context.profiles`.

### Timing and limit blocks

<!-- ds:block id=team-config-defaults-amp24fsn -->
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
| `link_index.max_age` | `1h` | how old the saved list of linked client repos may be when the store cannot be reached; past it lore still attaches the folders the saved list names, so reads show the rows fetched last, but a write that needs to know whether this is a client repo is refused until a sync |

<!-- ds:block id=team-live-kinds-88j26q7s -->
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
ERROR: [E_CONFIG_INVALID] ~/.lore/config.yaml is not valid: lease: renew_every: must be shorter than ttl, or every claim expires before it is renewed; merge_check: adapter "gitlab": must be "none" or "github"; orgs[0] "Agency": name: use lowercase letters, digits, '.' or '-', starting with a letter or digit; orgs[0] "Agency": identity.key "alice@agency.example": must be human:<your email in that org> (TEAM-029); orgs[0] "Agency": stores[0].branch "team/staging": a store branch may not contain "/" (refs/lore/live/<branch> must never clash with another branch)
  hint: fix the problems listed; nothing else in lore reads an invalid config
(exit 20)
```
<!-- /example -->

The rules it checks, in plain words:

| Key | Rule |
|---|---|
| `identity.key`, `orgs[].identity.key` | must be `human:` followed by an email address |
| `identity.handle`, `orgs[].identity.handle` | lowercase letters, digits, `_` or `-`, no `@` |
| `personal.store` | a git URL or a `file://` path (or the v0.1.17 value `local`) |
| `personal.push` | `now`, `batch` or `manual` |
| `personal.push_every` | at least `1m` |
| `personal.compact` | `on` or `off`; `on` needs `personal.compact_after` |
| `personal.compact_after` | at least `1d` |
| `orgs[].name` | see the org entry table; two entries may not share a name |
| `orgs[].stores[]` | a valid URL; two items may not name the same store and branch; at most one has `org_folder` |
| `orgs[].stores[].branch`, `default_branch` | a valid git branch name; a store branch may not contain `/` |
| `orgs[].stores[].org_folder` | `.` or one folder name (letters, digits, `.`, `_`, `-`), not starting with `_` |
| `orgs[].stores[].name` | one lowercase word (letters, digits, `.`, `_`, `-`), not `team`, `me` or `repo`; two stores of one entry may not share a short name (the default short name included), and the refusal names both urls and says to set `stores[].name` in lore-org.yaml |
| `access` | each item has both `url` and `via`, they differ, and each `url` appears once |
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
| `setups[].name`, `setups[].kind`, `setups[].folders` | see the setups table: a lowercase name, `solo`, `member` or `admin`, absolute folders |
| `setups[].default` | at most one setup |
| `repo_setups` | each value names a setup under `setups:` |
| `setup.idle_minutes` | 1 to 1440 |
| `doctor.scan_depth` | 1 to 10 |
| `doctor.probe_timeout_s` | 1 to 300 |
| `policies.<key>` | a value the policy key can take; an unknown key is only a warning |

The setup, `setup:`, `doctor:` and `policies:` rules, broken all at once:

<!-- example: reference#config-check-setups-bad -->
```console
# alice, in ~
$ cat .lore/config.yaml
setups:
  - name: Work
    kind: boss
    folders: [work]
  - name: home
    kind: solo
    default: true
  - name: laptop
    kind: solo
    default: true
setup:
  idle_minutes: 0
doctor:
  scan_depth: 20
  probe_timeout_s: 600
policies:
  meta.max_bytes: lots

# alice, in website
$ lore config check
ERROR: [E_CONFIG_INVALID] ~/.lore/config.yaml is not valid: setups[Work]: name "Work" must be snake_case or kebab-case (a-z, 0-9, _ or -, starting with a letter), at most 64 characters; setups[Work]: kind "boss" must be solo, member or admin; setups[Work]: folder "work" must be absolute (start with / or ~/); setups: default: true is set on home and laptop; at most one setup may be the default; setup.idle_minutes 0 must be between 1 and 1440; doctor.scan_depth 20 must be between 1 and 10; doctor.probe_timeout_s 600 must be between 1 and 300; ~/.lore/config.yaml: policies.meta.max_bytes: value must be a whole number from 0
  hint: fix the problems listed; nothing else in lore reads an invalid config
(exit 20)
```
<!-- /example -->

A typing mistake that YAML itself cannot read, such as a bad duration, is reported alone, with its line number. Fix it and run the check again to see the rest:

<!-- example: reference#config-check-typo -->
```console
# alice, in website
$ lore config check
ERROR: [E_CONFIG_INVALID] ~/.lore/config.yaml is not valid: orgconf: config.yaml: line 2: invalid duration "1d12h": time: unknown unit "d" in duration "1d12h"
  hint: fix the problems listed; nothing else in lore reads an invalid config
(exit 20)
```
<!-- /example -->

## `~/.lore/identity.yaml` (v0.1.17)

v0.1.17 kept your fallback identity here (`display_name`, `stable_key`). 0.1.18 keeps it in `identity:` of `~/.lore/config.yaml`: `lore identity set` and `lore identity unset` edit that block. An old `identity.yaml` is still read while that block is empty (`lore identity show` warns), and `lore migrate run` moves it into the block and keeps the old file as `identity.yaml.migrated`. If `identity:` is already set, that value is kept and the migration report names the dropped one.

## `.lore/org.yaml`

A committed file in a repo the org owns. It names the org so everyone who clones the repo gets the same org without any setup. It holds names only, never a store address or a person, so it tells a reader nothing about where the org keeps its notes. lore accepts it only in repos that match the org's `own:` patterns and are not linked as a client repo. `lore org set` writes it.

| Key | Default | What it means |
|---|---|---|
| `org` | required | the org's name, as in `orgs[].name` |
| `project` | none | the internal project folder in the org's store (one folder name) |

## `lore-store.yaml`

<!-- ds:block id=team-store-allowed-a7qb5uuj lines=1-30 -->
The marker at the top of a store repo. Its presence is what makes a repo a store; `lore store init` writes it. Besides this marker, a store may hold only the signpost `.0-needs-lore-0.1.18-or-newer` (one line telling a lore 0.1.17 user to upgrade; 0.1.17 names it first when it refuses the store), `README*`, `LICENSE*`, `.gitattributes`, `.github/workflows/lore-*.yml`, row files (`<folder>/<table>/<id>.json`, the store's own in `_store/<table>/<id>.json`), each folder's `_meta.json` and `_purged.json`, `lore-org.yaml` (at the top or in a folder), and the `.lore/` folders of the layout before 0.1.18's plain folders (at the top or in any folder). lore refuses a store that holds anything else, with `E_NOT_A_STORE`; a file you add by hand to your clone and do not commit is only reported by `lore doctor` (`store_files`).

| Key | Default | What it means |
|---|---|---|
| `lore_store` | required | the layout version, a whole number of at least 1 (lore writes `4`: rows at `<folder>/<table>/`, the store's own rows in `_store/`); an older lore refuses a newer layout with `E_OLD_LORE` |
| `created` | written by `lore store init` | the day the store was made, `YYYY-MM-DD` |
| `org_folder` | none | the folder suggested for the roster and company rules; only a hint, the org entry's `org_folder` decides |
| `push` | `now` | how every member's writes to this store reach the remote: `now`, `batch` or `manual`, as `personal.push` above. Only an org admin sets it, with `lore store set <store> push=…`; a member cannot override it |
| `push_every` | `5m` | how long a member's batch may wait before it is pushed, at least `1m` |
| `compact` | `off` | `on`: the admin's machine folds history older than `compact_after` into one commit by itself, at most once a day |
| `compact_after` | none | how old history must be before it is folded, at least `1d`; `compact: on` needs it |
| `epoch` | `0` | how many times the history was folded; `lore store compact` raises it, and a copy that sees a higher epoch moves onto the new history and replays its unpushed work. Never edit it by hand |

`lore store init <name> <url>`, run inside a repo, writes the marker as the store's first commit and attaches the store to the repo under that name (in `.lore/stores.yaml`):

<!-- example: reference#store-marker only=2,4 -->
```console
# alice, in website
$ LORE_ORG=agency lore store init agency git@github.com:agency/notes --yes
lore store init: store clone ~/.lore/stores/agency/notes: git user set to Alice <alice@agency.example> (your identity in org agency)
lore store init: store clone ~/.lore/stores/agency/notes: set up for plain git (merge driver, pre-commit hook; `lore where` lists them)
✓ created store agency (sto_c0f3…b5b8) at git@github.com:agency/notes; recorded in .lore/stores.yaml; commit it to share
$ cat .lore/stores.yaml
stores:
    - name: agency
      url: git@github.com:agency/notes
    - name: freelance
      url: git@github.com:other/notes
```
<!-- /example -->

## `<org_folder>/lore-org.yaml`

The org entry a team suggests to its members, kept in the store's org folder. It never holds a person's identity. `lore org import` copies it into your `~/.lore/config.yaml` and adds your identity; `lore org refresh <org>` copies `stores`, `own`, `repos` and `defaults` again when the team changed the file (a key the file no longer has is removed from your entry). lore also reads `defaults.client_repos` from it every time, and the team's value wins over a looser one in your own file.

| Key | What it means |
|---|---|
| `name` | the suggested entry name |
| `stores[]` | the org's stores, with `url`, `name`, `branch` and `org_folder` as in your config. Write each `url` in its canonical form, the way every teammate reaches it (`git@github.com:agency/notes.git` or `https://…`), never with an ssh alias of your own; each machine maps it in its own `access:` |
| `own`, `repos` | the repo patterns |
| `defaults.client_repos.<kind>` | where rows of each kind go in client repos |

## `.lore/lore.yaml` (shared database)

v0.1.17 could point a project at a database somewhere else, shared by several projects, with this pointer file (older versions called it `.lore/lore.toml`). 0.1.18 retired that mode: every command in such a folder stops and names `lore migrate run`, which copies the project's notes into a folder store beside the shared database, attaches it, and keeps the pointer as `<file>.migrated` ([One store for several repos](../sync/shared-database-mode-b.md#step-4--convert-a-repo-that-used-a-shared-database)). `lore migrate run` reads these keys:

| Key | Default | What it means |
|---|---|---|
| `db_path` | required | where the shared database is. You may write `${HOME}`, `${LORE_HOME}` (the value of `LORE_HOME`, else `~/.lore`) and `${PROJECT_ROOT}` (this project's folder). A `..` part is refused unless `LORE_ALLOW_DB_PATH_OUTSIDE=1` |
| `project_id` | required | which project inside that database this folder is |

## Your settings (`lore config`)

`lore config get`, `set` and `list` read and write the keys of this file, `~/.lore/config.yaml`, as dotted paths (`default_branch`, `live.autopush`). A key lore does not read is refused and nothing is written. lore's own bookkeeping inside its cache is no longer read or set with `lore config`; team rules are set with `lore policy set`.

<!-- example: reference#db-config -->
```console
# alice, in website
$ lore config list
(no config keys)
$ lore config get default_branch || echo 'not set'
ERROR: [E_NOT_FOUND] config key "default_branch" not found
  hint: `lore config list` shows the keys set in ~/.lore/config.yaml
not set
```
<!-- /example -->


## Pitfalls

- A mistake in `~/.lore/config.yaml` blocks reading and writing notes, in every repo, until you fix it. Run `lore config check` after each edit.
- A setup folder must be absolute: `~/work`, not `work`.
- `policies:` can only tighten a team rule. A looser value is ignored without an error, so check the source column of `lore policy show`.
- `lease.renew_every` is checked against `lease.ttl` only when both are set in the same block. If you shorten `ttl` in an org entry, set `renew_every` there too.
- A store branch name may not contain `/`. Use `staging`, not `team/staging`.
- `identity.key` is `human:` plus your email, not your email alone and not your name.
- `1d12h` is refused; write `36h`.
- `personal.push`, `push_every`, `compact` and `compact_after` apply to your personal store only. An org store keeps its own in its `lore-store.yaml`, set by the org admin with `lore store set`.
- An ssh alias of yours in lore-org.yaml or `.lore/stores.yaml` works on your machine only: teammates cannot reach the store. Put the canonical url there and the alias in your own `access:`; `lore doctor` names every such url.
- `LORE_HOME` does not move `~/.lore`: lore always keeps its config (your identity included) and caches under `~/.lore` in your home folder. `LORE_HOME` only fills in `${LORE_HOME}` in `db_path`.

## Related

- [Environment variables](environment.md): switches that change the same behaviour for one shell or one command.
- [Error codes](errors.md): `E_CONFIG_INVALID`, `E_NOT_A_STORE` and the rest.
- [Team guide](../team/README.md) and [team command reference](../team/commands.md#config-keys).
- [Command index](commands.md).
