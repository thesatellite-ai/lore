# Your identity, joining an org, and store housekeeping

**In short:** lore needs to know who you are, so it can write your name on the notes and tasks you add. Your identity is your email address. This page shows how to check and change it, join your company's setup in one command, move a note that went to the wrong place, tidy the copies lore keeps on your machine, and choose how your writes to a store reach its remote.

## Before you start

- lore installed ([getting started](getting-started.md)).
- For the org steps: a store, the separate private repo where the agency keeps its notes ([Agency with a client repo](use-cases/agency-client-repo.md#the-config) shows how one is made with `lore store init`).

The people here are Alice and Bob (agency developers). The repo is the client's website, `github.com/client/website`.

## Step 1 — See who lore thinks you are: `lore identity show`

With no setup at all, lore uses the name and email from your git settings. `source:` says where the answer came from:

<!-- example: people#identity only=1 -->
```console
# alice, in website
$ lore identity show
resolved:    human:alice@agency.example
display:     alice
kind:        human
source:      git config user.email
stable:      yes
```
<!-- /example -->

## Step 2 — Set or remove your own identity: `lore identity set` and `unset`

Outside any company, `lore identity set` saves a fallback identity in `identity:` of `~/.lore/config.yaml`. Use your email: your **identity key** is your email address with `human:` in front, and lore uses it, not your display name, to tell people apart. `lore identity unset` removes the file, and lore goes back to your git settings:

<!-- example: people#identity only=2,3,4,5 -->
```console
# alice, in ~
$ lore identity set --identity alice@example.com
✓ identity persisted: alice@example.com
  file: ~/.lore/config.yaml (identity:)

# alice, in website
$ lore identity show
resolved:    human:alice@example.com
display:     alice@example.com
kind:        human
source:      top-level identity (config.yaml)
stable:      yes

# alice, in ~
$ lore identity unset
✓ identity: unset (next session will fall through chain)

# alice, in website
$ lore identity show
resolved:    human:alice@agency.example
display:     alice
kind:        human
source:      git config user.email
stable:      yes
```
<!-- /example -->

## Step 3 — Use a different identity inside a company: `lore identity set --org`

An **org** is one company's setup in your config: its stores, its people and the repos it owns. At work you usually use a different email than at home. `--org` stores that identity in the company's entry of `~/.lore/config.yaml`, and lore uses it in every repo of that company:

<!-- example: people#identity-per-org only=1,2 -->
```console
# alice, in ~
$ lore identity set --org agency --key human:alice@agency.example --name 'Alice Doe' --handle alice
✓ identity in agency: Alice Doe <human:alice@agency.example>
  file: ~/.lore/config.yaml

# alice, in website
$ lore identity show
org:         agency
resolved:    human:alice@agency.example
display:     Alice Doe
kind:        human
source:      org entry (config.yaml orgs[agency].identity)
stable:      yes
```
<!-- /example -->

## Step 4 — Join your company's setup in one command: `lore org import`

Writing the org entry by hand is easy to get wrong. Instead, someone at the agency commits a suggested entry, `lore-org.yaml`, into the org folder of the store once. It names the stores and which repos belong to whom, and never anyone's identity:

<!-- example: people#suggest-an-org-entry -->
```console
# alice, in notes-checkout
$ git add org/lore-org.yaml && git commit -qm 'suggest the agency org entry' && git push -q origin main
```
<!-- /example -->

Then each new person runs one command with their own email, from any folder: no repo is needed. Lore shows the entry, adds it to their config and puts them on the roster (the org's list of people), opening only the org's store. If the roster step fails (the store cannot be reached, say), the entry is still added and the command exits non-zero, telling you to run `lore actor register` again later:

<!-- example: people#org-import -->
```console
# bob, in ~
$ lore org import --url git@github.com:agency/notes --key human:bob@agency.example --name 'Bob Roe' --handle bob --yes
lore: pushed store agency/org: commit 601d285 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_7a67…9504.json (store agency/org)
This adds to ~/.lore/config.yaml:
    name: agency
    identity:
        key: human:bob@agency.example
        name: Bob Roe
        handle: bob
    stores:
        - url: git@github.com:agency/notes
          org_folder: org
    own:
        - github.com/agency/*
    repos:
        - github.com/client/*
✓ added org entry agency
✓ act_7a67…9504 Bob Roe (@bob) registered in agency org/
$ cat ~/.lore/config.yaml
orgs:
  - name: agency
    identity:
      key: human:bob@agency.example
      name: Bob Roe
      handle: bob
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own:
      - github.com/agency/*
    repos:
      - github.com/client/*
    source:
      store: sto_2ad3…f891
      version: 86dbae476bd6

# bob, in website
$ lore whoami
org        agency  (matched by: link in github.com/agency/notes client/, project id + remote)
identity   Bob Roe <human:bob@agency.example>   act_7a67…9504  @bob  (from: org entry (config.yaml orgs[agency].identity))
repo       client repo  (guards on)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_162d…9666
remotes    github.com/client/website
stores     git@github.com:agency/notes on branch main  (org folder org)
internal   github.com/agency/notes client/
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   not set
```
<!-- /example -->

## Step 5 — Let a repo name its org for everyone: `lore org set`

In the agency's own repos (not a client's), you can commit `.lore/org.yaml`, a one-line file that says which org the repo belongs to. Teammates then get the right org there without any config of their own. Lore refuses it in a client repo, because it would tell the client the agency's org name:

<!-- example: people#org-set -->
```console
# alice, in tools
$ lore org set --org agency --commit
✓ wrote ~/work/tools/.lore/org.yaml (org agency)
  commit it with git so teammates resolve the same org
$ cat .lore/org.yaml
org: agency

# alice, in website
$ lore org set --org agency --commit
ERROR: [E_INVALID_INPUT] refusing to write .lore/org.yaml: .lore/org.yaml names agency, but no remote (github.com/client/website) matches its own: patterns; ignored (only an org's own repos may name it)
  hint: only an org's own repos (matching its own: patterns) may name it; use `lore org use` for a machine-only choice
(exit 20)
```
<!-- /example -->

## Step 6 — Move a note to where it belongs: `lore move`

Alice wrote a note into the client's repo by mistake, where the client can read it. `lore move` moves it to the agency's internal project, keeping its id; the client repo keeps only a deletion marker. It shows what would move and asks first (see [Sharing an internal note](use-cases/sharing-a-note.md) for what the question lists):

<!-- example: people#move -->
```console
# alice, in website
$ lore memory add --body 'the client replies faster on WhatsApp than email' --to repo
lore: wrote 2 row file(s) in ~/.lore/personal/agency (store me)
lore: wrote 2 row file(s) in ~/work/website/.lore/data
✓ mem_54fe…28f2
  scope: [master]
$ lore move --id mem_54fe…28f2 --to agency --yes
lore: pushed store agency/client (here: agency): commit 9b40ce7 to branch main
lore: pushed store agency/client (here: agency): commit 05513aa to branch main
lore: wrote ~/.lore/stores/agency/notes/client/actors/act_05e9…4603.json (store agency)
lore: wrote ~/.lore/stores/agency/notes/client/memories/mem_54fe…28f2.json (store agency)
lore: wrote ~/work/website/.lore/data/memories/mem_54fe…28f2.json
✓ mem_54fe…28f2 moved to store agency (same id; the old copy is tombstoned)
$ lore memory list
mem_54fe…28f2 [retrieved] the client replies faster on WhatsApp than email [agency/client]
```
<!-- /example -->

If the original was already pushed or committed, the client may have seen it; see [Cleaning up a leak](use-cases/leak-cleanup.md).

## Step 7 — Look inside a store, or drop your copy: `lore store open` and `forget`

Lore keeps one clone of each store, a plain git clone at `~/.lore/stores/<owner>/<repo>` (lore's own lock and stamps sit in its `.git/lore/`). `lore store open` fetches it and prints where it is, so you can browse the files; another branch of the store gets its own clone, `<repo>@<branch>`. To use a clone you made yourself, `lore store use --path <path>`. `lore store forget` deletes this machine's copy of a store (never a clone of your own). The next command that needs it downloads it again. Both take the store's url, or the name this repo attaches it under (a name from `.lore/stores.yaml`, or `me` for your personal store); a name this repo does not attach is `E_NOT_FOUND`:

<!-- example: people#store-open-and-forget -->
```console
# alice, in ~
$ lore store open --url git@github.com:agency/notes
✓ ~/.lore/stores/agency/notes on main (05513aa7ab36): the store's own clone, a plain git checkout to browse; lore commits and pulls here too
$ ls ~/.lore/stores/agency/notes
README.md
client
lore-store.yaml
org

# bob, in ~
$ lore store forget --url git@github.com:agency/notes
✓ forgot git@github.com:agency/notes (removed ~/.lore/stores/agency/notes; the next command that needs it clones it again)
```
<!-- /example -->

## Step 8 — Clean up old caches: `lore cache prune`

Lore keeps a cache file per attached store in `~/.lore/cache`, and one per repo checkout in `~/.lore/cache/repo/` (named after the checkout's folder and path). `lore cache prune` removes the cache files of stores that no repo on this machine attaches any more (a removed checkout, a `lore store remove`) and the cache of a checkout that was moved or deleted (moving a repo folder gives it a new cache; the old one is listed here). When the only thing to clean is lore's own record of a gone checkout, it says `removed N cache use record(s)` and nothing else. It lists each file with its size and asks before deleting anything: `--dry-run` only lists, `--yes` deletes without asking, and without a terminal it refuses with `E_NEEDS_CONFIRM` unless you pass `--yes`. It never touches row files or anything outside `~/.lore/cache`; a store you attach again rebuilds its cache on the next command.

<!-- example: people#cache-prune -->
```console
# alice, in ~
$ lore cache prune --dry-run
nothing to prune: every cache file belongs to a repo on this machine or a store one attaches
```
<!-- /example -->

## Who signs lore's commits in a store

A commit lore makes in a store clone (a note, a merge, a marker) is signed by the same identity a commit you make by hand in that clone would carry, with one exception for org stores:

| Store | Signed by |
|---|---|
| an org's store | your git identity for that clone when its email is your identity in the org (`lore whoami`); otherwise the org identity. A personal git email (`@gmail.com` and so on) never signs an org store's commit. |
| any other store (`me`, a `.lore/stores.yaml` store) | your git identity for that clone when git has one (environment, the clone's config, then global and system config); otherwise your lore identity |

So when lore's commits show your org name ("Mira Dev") and your own commits in the clone show your git name ("mira-agency") with the same email, give the clone the git identity you want: git's `includeIf "gitdir:~/.lore/stores/"` in your global config, or `git config user.name` in the clone. lore writes `user.name` and `user.email` into a store clone's own `.git/config` only when it clones the store or finds no `user.email` there, announces it in one line, and later changes only the value it wrote itself ([Plain git in the store](../sync/editing-by-hand.md#plain-git-in-the-store)); it never touches your global git config.

When a coding agent writes for you, the commit is still signed as the table above says, never as the agent: the agent is named in a `Lore-Agent:` trailer of the commit message.

## How writes reach the remote

Your personal store (`me`), your org's stores and the stores a repo attaches in `.lore/stores.yaml` are git repos that hold only lore notes. By default every note you write to one is pushed at once, as its own commit. A busy store collects thousands of small commits that way. A store can instead collect your writes and push them together, and its old history can be folded into one commit. None of this applies to the code repo (`repo`): you push that one with your code.

### The three push modes

| Mode | What a write does | When it reaches the remote |
|---|---|---|
| `now` (the default) | commits and pushes | at once, one commit per write |
| `batch` | commits on your machine only | when the oldest waiting write is `push_every` (default `5m`) old and you run any lore command that uses the store, on `lore sync`, on commands that need the remote current (`lore sync status`, `lore land`), when your coding tool starts or ends a session, and from the repo's git hooks lore installs (after a commit, before a push) |
| `manual` | commits on your machine only | only on `lore sync` (and on the syncs lore runs itself for team coordination, such as the roster) |

Each store commit is titled after what you did: the command and, for a kind with a short title (a task, a plan), the title, as in `lore: task add: Ship the beta`; other rows are named by id, as in `lore: memory add mem_01ab…` (with `(N rows)` when the person's actor row or a task list went into the same commit). A note's text never goes into a commit title, so a deleted or purged note is not left readable in `git log`; delete, trash and purge commits name only the command and the id, and a title the secret check flags is replaced by the id. In `batch` and `manual` mode every write is still a commit on your machine, so a crash loses nothing, and your own reads see the note at once. When the waiting writes go out, they go out as **one** commit, `lore: batch N rows (memories 2, tasks 1)`, with one line per note in its message. Setting `LORE_LIVE_AUTOPUSH=0` still stops the push after a write, in every mode.

lore has no daemon, timer or background process, and never starts one. A batch goes out only when something runs lore: a command you (or your coding tool) run anyway checks the age of the oldest waiting write and, once it is `push_every` old, pushes the batch inside that command. That push gets a short time limit, so a slow or unreachable remote never holds your command up; what did not go out stays committed for the next command. If nothing runs lore, the batch simply stays on your machine, safe. `lore store show`, `lore sync status` and `lore doctor` show how old the oldest waiting write is, and `lore doctor` warns once it is more than a day old. Run `lore sync` to push it.

Alice batches her personal store. Her two notes wait on her machine, together with the rows lore records about her and her machine on a first write; `lore store show` says how many commits are waiting, and `lore sync` pushes them as one commit:

<!-- example: store-push#batch -->
```console
# alice, in website
$ lore store set --store me --settings push=batch --settings push_every=10m
me: push batch every 10m, compact off
  set in    ~/.lore/config.yaml (personal:)
  epoch     0
$ lore memory add --body 'I always forget the staging VPN profile' --to me
lore memory add: store clone ~/.lore/stores/alice/lore-me: git user set to Alice <alice@agency.example> (your lore identity, was alice@devbox)
lore: wrote ~/.lore/stores/alice/lore-me/_store/actors/act_7a67…9504.json (store me)
lore: wrote ~/.lore/stores/alice/lore-me/_store/memories/mem_2ad3…f891.json (store me)
lore: wrote ~/.lore/stores/alice/lore-me/_store/machines/mch_162d…9666.json (store me)
✓ mem_2ad3…f891 [me]
  scope: [master]
$ lore memory add --body 'I prefer short standups' --to me
lore: wrote ~/.lore/stores/alice/lore-me/_store/memories/mem_54fe…28f2.json (store me)
✓ mem_54fe…28f2 [me]
  scope: [master]
$ lore store show --store me
me: push batch every 10m, compact off
  at        git@github.com:alice/lore-me
  used by   your setup agency (one personal store per person: any other setup of yours shares it)
  set in    ~/.lore/config.yaml (personal:)
  epoch     0
  waiting   3 local commit(s) not pushed yet, the oldest under 1m old (`lore sync` pushes them)
$ lore sync
lore: pushed store me: commit 601d285 to branch main
store me: sent 4, received 0, merged 0, refused 0
store agency/client (here: agency): sent 0, received 0, merged 0, refused 0
store agency/org: sent 0, received 0, merged 0, refused 0
$ git fetch -q https://github.com/alice/lore-me main && git log -1 --format=%B FETCH_HEAD
lore: batch 4 rows (actors 1, machines 1, memories 2)
actors act_7a67…9504
machines mch_162d…9666
memories mem_2ad3…f891
memories mem_54fe…28f2
Lore-Version: dev
```
<!-- /example -->

With `push=manual` nothing leaves the machine until `lore sync`. `lore doctor` and `lore sync status` print each store's mode and how many commits are waiting:

<!-- example: store-push#manual -->
```console
# alice, in website
$ lore store set --store me --settings push=manual
me: push manual, compact off
  set in    ~/.lore/config.yaml (personal:)
  epoch     0
  pushed    2026-11-02T10:00:30Z
$ lore doctor | grep 'store me:'
  - store me: push manual, compact off
```
<!-- /example -->

### Where the setting lives, and who may change it

- **Your personal store:** in the `personal:` block of `~/.lore/config.yaml` (keys `push`, `push_every`, `compact`, `compact_after`). `lore store set --store me --settings …` writes it; `lore config set --key personal.push --value batch` works too, and `lore config check` checks the values.
- **An org's store:** in the store's own `lore-store.yaml`, so every member gets the same setting. Only an admin of the org may change it: a machine whose `~/.lore/config.yaml` holds an admin setup of that org (`setups:` with `kind: admin` and `org: <org name>`, see [Guided setup](guided-setup.md)). A member cannot override it on their own machine.
- **A store attached in `.lore/stores.yaml`** (`lore store init` or `lore store add`) that belongs to no org: in the store's own `lore-store.yaml` too, so every clone of it gets the same setting. There is no admin to ask, so anyone who can push to the store may change it with `lore store set --store <store> --settings …`, and compact it. If the store holds an org's `lore-org.yaml` (it is an org's store that this machine attached by its address without `lore org import`), it is that org's store and only the org's admin may change it.

The admin sets it once and every member's next sync picks it up:

<!-- example: store-push#org-settings -->
```console
# alice, in website
$ lore store set --store agency --settings push=batch --settings push_every=15m
agency: push batch every 15m, compact off
  set in    the store's lore-store.yaml (org agency; set by an org admin, which this machine is)
  epoch     0
  pushed    2026-11-02T10:01:05Z

# bob, in website
$ lore sync >/dev/null && lore store show --store agency
agency: push batch every 15m, compact off
  set in    the store's lore-store.yaml (org agency; set by an org admin)
  epoch     0
  pushed    2026-11-02T10:00:00Z
```
<!-- /example -->

`lore store show --store <store>` prints a store's settings; `--json` gives them under `"push"`. With no store name, `lore store show` lists the stores of your orgs as before.

The values `lore store set` takes:

| Key | Values | Default |
|---|---|---|
| `push` | `now`, `batch`, `manual` | `now` |
| `push_every` | how long a batch may wait, at least `1m` (such as `10m`, `1h`) | `5m` |
| `compact` | `on` or `off`: fold old history by itself (below) | `off` |
| `compact_after` | how old history must be to be folded, at least `1d` (such as `30d`); `compact=on` needs it | none |

### What is refused, and why

Every refusal below is `E_REFUSED` (exit 23) and nothing is written. `lore store set` and `lore store compact` refuse:

- **the code repo (`repo`):** lore never pushes or rewrites a code repo's history; `lore live compact` compacts its live rows.
- **a member's machine on an org store:** only the org's admin decides; ask them.
- **a folder store (`file://`):** it has no remote to push to and no history to fold. `lore store set-url` moves it to a git url.
- **a compaction while another copy of the store on this machine holds work not pushed yet:** run `lore sync` there first.

A value out of range is `E_INVALID_INPUT` (exit 20):

<!-- example: store-push#refused -->
```console
# alice, in website
$ lore store set --store repo --settings push=batch
ERROR: [E_REFUSED] store set: `repo` is the code repository's own .lore/data; lore never pushes or rewrites a code repo's history, so push and compact settings do not apply there
  hint: you push the code repo with your code; `lore live compact` compacts its live rows
(exit 23)

# bob, in website
$ lore store set --store agency --settings push=batch
ERROR: [E_REFUSED] store set: store agency belongs to org agency and only an admin of agency may change its shared settings; this machine has no admin setup of agency
  hint: ask the org admin to run `lore store set --store agency --settings …`; a member cannot override the org's setting locally
(exit 23)

# alice, in website
$ lore store set --store me --settings push_every=30s
ERROR: [E_INVALID_INPUT] store set: push_every 30s: must be at least 1m
  hint: e.g. compact=on compact_after=30d; push_every at least 1m, compact_after at least 1d
(exit 20)
```
<!-- /example -->

### Folding old history: `lore store compact`

`lore store compact --store <store>` turns every commit older than the cutoff into **one** snapshot commit, `lore: snapshot of the store history before <date>`, keeps the later commits as they were, and adds a commit `lore: compact store history (epoch N)`. Every note is kept; only the list of old commits is gone. The **epoch** is a counter in `lore-store.yaml` that goes up by one with each compaction, so every copy of the store can tell that the history was rewritten.

- The cutoff is `--before` (such as `--before 30d`), else the store's `compact_after`, else `30d`.
- `--dry-run` says what would change and changes nothing.
- It asks first. Without a terminal it stops with `E_NEEDS_CONFIRM` (exit 26) until you pass `--yes`.
- The rewrite is pushed only if nobody pushed since lore last looked (`E_CONTENDED` otherwise; run it again). Offline it is `E_OFFLINE`. In both cases nothing changes.
- The old history stays on your machine under `refs/lore/backup/compact-<epoch>-<commit>`.
- On an org store only the admin may compact; your personal store is yours.

<!-- example: store-push#compact only=2,3,4 -->
```console
# alice, in website
$ lore store compact --store agency --before 30d --dry-run
would squash 7 commit(s) of agency before 2026-10-03 into one snapshot, keep 2 later commit(s) and every one of its 7 row file(s); epoch 0 → 1
$ lore store compact --store agency --before 30d
ERROR: [E_NEEDS_CONFIRM] store compact agency: rewrite the history of store agency: squash 7 commit(s) before 2026-10-03 into one snapshot (every row is kept; every clone re-fetches at epoch 1)?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore store compact --store agency --before 30d --yes
✓ compacted agency: 7 commit(s) before 2026-10-03 are one snapshot, 2 later commit(s) kept, all 7 row file(s) kept; epoch 0 → 1 (old history kept locally in refs/lore/backup/compact-0-86dbae476bd6)
  pushed store agency: commit 9b40ce7 to branch main
```
<!-- /example -->

With `compact=on`, lore compacts by itself, at most once a day per store, inside a normal command right after one of its pushes (never on a timer; if nothing runs lore, nothing is compacted). It does that for your personal store, for a `.lore/stores.yaml` store of no org on any machine, and for an org store only on the admin's machine:

<!-- example: store-push#auto-compact -->
```console
# alice, in website
$ lore store set --store me --settings compact=on --settings compact_after=90d
me: push manual, compact on after 90d
  set in    ~/.lore/config.yaml (personal:)
  epoch     0
  pushed    2026-11-02T10:00:30Z
```
<!-- /example -->

### What a member sees after a compaction

Nothing to do. On Bob's next sync lore sees the higher epoch, moves his copy onto the new history instead of merging the two, and replays his unpushed notes on top as one commit, `lore: replay local work onto epoch N` (a note both sides changed is merged field by field, as always). His old copy stays under `refs/lore/backup/epoch-<n>-<commit>`. Here Bob had written a note that was still waiting for his next batch when Alice compacted:

<!-- example: store-push#compact only=1,5,6,7 -->
```console
# bob, in website
$ lore memory add --body 'the client wants the invoice on the 1st' --to agency
lore: wrote ~/.lore/stores/agency/notes/client/actors/act_05e9…4603.json (store agency)
lore: wrote ~/.lore/stores/agency/notes/client/memories/mem_c0f3…b5b8.json (store agency)
✓ mem_c0f3…b5b8 [agency/client]
  scope: [master]
$ lore sync
lore: pushed store agency/client (here: agency): commit 05513aa to branch main
store agency/client (here: agency): sent 2, received 0, merged 0, refused 0
store agency/org: sent 0, received 0, merged 0, refused 0
$ lore memory list
mem_76bd…3f10 [retrieved] the client signs off designs on Thursdays [agency/client]
mem_d431…313a [retrieved] staging resets every Sunday night [agency/client]
mem_c0f3…b5b8 [retrieved] the client wants the invoice on the 1st [agency/client]
$ git fetch -q https://github.com/agency/notes main && git log --format=%s FETCH_HEAD
lore: replay local work onto epoch 1
lore: compact store history (epoch 1)
lore: store set agency
lore: actor register act_05e9…4603
lore: snapshot of the store history before 2026-10-03
```
<!-- /example -->

## Try it

In a repo of your org, give yourself the org identity and check that lore uses it there (the `identity show` line says where it comes from):

<!-- example: people#identity-per-org -->
```console
# alice, in ~
$ lore identity set --org agency --key human:alice@agency.example --name 'Alice Doe' --handle alice
✓ identity in agency: Alice Doe <human:alice@agency.example>
  file: ~/.lore/config.yaml

# alice, in website
$ lore identity show
org:         agency
resolved:    human:alice@agency.example
display:     Alice Doe
kind:        human
source:      org entry (config.yaml orgs[agency].identity)
stable:      yes
```
<!-- /example -->

Run `lore identity show` in a repo of no org afterwards: it still shows your top-level identity, because `--org` changes only that org's entry.

## Pitfalls

- **The identity key is an email, never a name.** `lore identity set --identity "Alice Doe"` is refused (`E_INVALID_INPUT`), because the same person on another machine with another spelling would become a second person. To keep a display name, pass the email too: `lore identity set --identity "Alice Doe <alice@example.com>"` or `lore identity set --identity "Alice Doe" --key human:alice@example.com`.
- **`lore org import` asks before risky defaults.** A suggested entry that sends rows into client repos, or a pattern that matches a whole host, needs your yes. `--yes` accepts it without a prompt, so read the printed entry first.
- **`lore org set` is refused in client repos.** Use `lore org use --entry <org>` there: it is a choice for your machine only and is never committed.
- **`lore move` is refused for agents with `--yes`.** Moving a note is a person's decision.
- **`lore store forget` refuses while the copy holds work that is not pushed yet.** Run `lore sync` first. The one exception is a store whose repo was deleted and made again: there everything is unpushed, so forget asks instead, and `--yes` drops the old copy (`lore store push --store <store> --restore` puts it back instead). Like the other store commands, forget accepts `--yes`.
- **A batched note is not on the remote yet.** In `batch` or `manual` mode your teammates and your other machines see a note only after the batch is pushed. Run `lore sync` before you switch machines.
- **Never edit the folder `lore store open` made.** Lore writes the store branch itself; your edits there are not picked up.
- **`lore store list` shows where each store comes from.** The SOURCE column says `stores.yaml` (attached by this repo), `org` (brought by the repo's org) or `personal`. A store url from the org is printed as `<redacted>` where output must name nothing (no terminal, CI); its visibility is still worked out from the real url. A name the org already uses cannot be taken again by `lore store init` or `lore store add` (`E_INVALID_INPUT`).
- **Writing an org store's url into `.lore/stores.yaml` asks first.** The org link keeps the org's store urls out of the repo. `lore store add --name <name> --url <url> --folder <folder>` (also `store init` and `store set-url`) with a url that is one of your org's stores (any org in `~/.lore/config.yaml`, with or without an identity, and any spelling of its url: ssh or https, with or without `.git` or a trailing slash) would commit that url, visible to everyone who can read the repo, so lore asks at a terminal and, without one, stops with `E_NEEDS_CONFIRM` (exit 26) before writing anything; rerun with `--yes` to write it. In a client repo the same commands ask for any store url, because the client reads the file. A write to an org store folder this repo does not attach names this command with `--yes` on stderr.
- **A store url never keeps a login in `.lore/stores.yaml`.** `lore store init`, `add` and `set-url` drop the user part of an `https://user@host/…` url before writing it, because git hosts treat that user as a token, and refuse a url with a password or token in it (`E_URL_CREDENTIALS`). An ssh login such as `git@github.com:…` is kept.
- **0.1.18 stores are git repos or folders.** A url with another scheme (`s3://`, `postgres://`, `lore://`) is refused with `E_STORE_SCHEME` before anything is written: use a git url, the path of a bare git repository on this machine (`/srv/git/notes.git`, or on Windows `C:\git\notes.git` / `C:/git/notes.git`) or `file:///<absolute path>` for a folder. A path that does not exist is `E_BAD_PATH`, naming both spellings; a `file://` url (or path) of a bare git repository is that repository as a git store, as `git clone` reads it.
- **`lore store init --name me` works anywhere.** It makes your personal store and records it in `~/.lore/config.yaml`, so it needs no repo; every other `lore store` command runs inside a repo.

## Related

- [Getting started](getting-started.md): the first config with an org.
- [Two companies on one laptop](use-cases/two-companies.md): one identity per org.
- [A contractor who sees only one client](use-cases/contractor.md): store access per person.
- [Agents](agents.md): agent identities (`lore agent start`).
- [Commands](commands.md): every flag of `identity`, `org import`, `org set`, `move`, `store`, `cache prune`.
