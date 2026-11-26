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

To change only the email (an admin added your new address with `lore actor contact add … --identity`), give `--key` alone: the name and handle the entry has are kept, and the store clone's git user stays your name.

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

A row you write into one of that company's stores carries this identity wherever you run lore: from a repo of no org, `lore memory add --to <store>/<folder>` records you (`created_by`, `updated_by`, and your actor row in that store) as your identity in the org, never the fallback `lore whoami` shows there (your git email or `human:<user>@<host>`, the OS user).

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
lore: committed locally to store agency/org; lore sync sends it
lore: committed locally to store agency/org; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/org/projects/prj_7a67…9504.json (store agency/org)
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_2ad3…f891.json (store agency/org)
lore: wrote ~/.lore/stores/agency/notes/org/actor_periods/apd_162d…9666.json (store agency/org)
lore: wrote ~/.lore/stores/agency/notes/org/_meta.json (store agency/org)
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
✓ act_2ad3…f891 Bob Roe (@bob) registered in the roster of agency (agency/org)
✓ you are agency's admin (org role admin): lore member add --email <their-email> --name "Their Name" adds a teammate
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
      store: sto_54fe…28f2
      version: 601d2850ffd0

# bob, in website
$ lore whoami
org        agency  (matched by: repos pattern github.com/client/* of agency)
identity   Bob Roe <human:bob@agency.example>   act_2ad3…f891  @bob  (from: org entry (config.yaml orgs[agency].identity))
repo       client repo  (guards OFF in this clone: no lore commit-msg hook, commits are not checked for internal notes; lore fix here installs it)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_05e9…4603
remotes    github.com/client/website
stores     git@github.com:agency/notes on branch main  (org folder agency/org: roster, teams, company rules)
links      known  0 link(s) for this repo (stores of 1 entry scanned)
personal   not set
$ lore sync
lore: pushed store agency/org: commit 86dbae4 to branch main (3 commits, 3 row files)
store agency: up to date
store agency/org: pushed (sent 3, received 0, merged 0, refused 0)
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
lore: wrote ~/work/website/.lore/data/actors/act_c0f3…b5b8.json
lore: wrote ~/work/website/.lore/data/memories/mem_76bd…3f10.json
✓ mem_76bd…3f10
  scope: [project]
$ lore move --id mem_76bd…3f10 --to agency --yes
lore: committed locally to store agency/client; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/client/memories/mem_76bd…3f10.json (store agency/client)
lore: wrote ~/work/website/.lore/data/memories/mem_76bd…3f10.json
lore: wrote ~/.lore/stores/agency/notes/client/_meta.json (store agency/client)
✓ mem_76bd…3f10 moved to store agency (same id; the old copy is tombstoned)
  warning: projects/prj_05e9…4603 stays: repos/rep_d431…313a project_id references it
$ lore memory list
mem_76bd…3f10 [retrieved] the client replies faster on WhatsApp than email [agency/client]
```
<!-- /example -->

If the original was already pushed or committed, the client may have seen it; see [Cleaning up a leak](use-cases/leak-cleanup.md).

## Step 7 — Look inside a store, or drop your copy: `lore store open` and `forget`

Lore keeps one clone of each store, a plain git clone at `~/.lore/stores/<owner>/<repo>` (lore's own lock and stamps sit in its `.git/lore/`). `lore store open` fetches it and prints where it is, so you can browse the files; another branch of the store gets its own clone, `<repo>@<branch>`. To use a clone you made yourself, `lore store use --path <path>`. `lore store forget` deletes this machine's copy of a store (never a clone of your own). The next command that needs it downloads it again. Both take the store's url, or the name this repo attaches it under (a name from `.lore/stores.yaml`, or `me` for your personal store); a name this repo does not attach is `E_NOT_FOUND`:

<!-- example: people#store-open-and-forget -->
```console
# alice, in ~
$ lore store open --url git@github.com:agency/notes
✓ ~/.lore/stores/agency/notes on main (9b40ce71af28): the store's own clone, a plain git checkout to browse; lore commits and pulls here too
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

Lore keeps a cache file per attached store in `~/.lore/cache`, and one per repo checkout in `~/.lore/cache/repo/` (named after the checkout's folder and path). `lore cache prune` removes the cache files of stores that no repo on this machine attaches any more (a removed checkout, a `lore store remove`) and the cache of a checkout that was moved or deleted (moving a repo folder gives it a new cache; the old one is listed here). A checkout counts as still here while its folder holds `.lore/` or `.git`, so a client repo linked with `lore org link` (no `.lore/` by design) keeps its caches. When the only thing to clean is lore's own record of a gone checkout, it says `removed N cache use record(s)` and nothing else. It lists each file with its size and asks before deleting anything: `--dry-run` only lists, `--yes` deletes without asking, and without a terminal it refuses with `E_NEEDS_CONFIRM` unless you pass `--yes`. It never touches row files or anything outside `~/.lore/cache`; a store you attach again rebuilds its cache on the next command.

<!-- example: people#cache-prune -->
```console
# alice, in ~
$ lore cache prune --dry-run
nothing to prune: every cache file belongs to a repo on this machine or a store one attaches
```
<!-- /example -->

## Who signs lore's commits in a store

Every commit lore makes in a store clone (a note, a merge, a marker, an adopt, an `org link`, a folder or org config change) follows one rule per store, whichever command makes it:

| Store | Signed by |
|---|---|
| an org's store | your identity in that org (`orgs.<org>.identity` in `~/.lore/config.yaml`, what `lore whoami` shows there), as author and committer, whatever the clone's git `user.name` says. A personal git email (`@gmail.com` and so on) never signs an org store's commit. An org entry with no identity signs like any other store (next row), `org link` and org config commits included. |
| any other store (`me`, a `.lore/stores.yaml` store) | your git identity for that clone when git has one (environment, the clone's config, then global and system config); otherwise your lore identity |

lore gives an org store's clone that same identity as its own `user.name` and `user.email` (in the clone's `.git/config`, never your global git config) when it clones the store, finds no `user.email` there or commits org settings there (a store added with `lore org store add` too), announces it in one line, and later changes only the value it wrote itself ([Plain git in the store](../sync/editing-by-hand.md#plain-git-in-the-store)), so a commit you make by hand in the clone carries it too. If you set another `user.name` in the clone (your git handle beside the org email), your hand commits carry that name while lore's own commits still carry your org identity, so `lore doctor` flags it (`store_identity` names both, the clone's and your org identity) and prints the `git -C <clone> config …` line that sets both; `lore doctor --fix` writes them.

When a coding agent writes for you, the commit is still signed as the table above says, never as the agent, and its message does not name the agent either; the agent is credited on the rows it writes.

## Admins-only stores

An org often has notes only its admins may read: servers, domains, registrar and cloud accounts, where a secret lives. Put them in a store of their own and mark it for admins: `lore org store add --org acme --url git@github.com:acme/ops.git --audience admins --push --yes` (or, for a store the org already lists, `lore org store audience --org acme --url git@github.com:acme/ops.git --audience admins --push --yes`). The store's entry in `lore-org.yaml` gets `audience: admins`; `--audience everyone` removes the key again. Give only the admins access to the repo on the git host: the git host is what keeps the notes private, the key is what keeps members' lore from trying. The org's repo patterns change the same way, with no hand edit of `lore-org.yaml`: `lore org config set --org acme --key repos --add 'github.com/acme/client-*' --push --yes` (a client pattern wins over an own one that matches the same repo; the confirmation and the result print the change, one `+ <pattern>` or `- <pattern>` line per entry added or taken out and the list's size before and after, never the whole list); `lore org config get --org acme` shows them, and names each own pattern that overlaps repos entries and that the repos entries win.

What each person's lore does with it:

- **A member** (no open roster stint with org role admin): the org entry lore copies from `lore-org.yaml` into `~/.lore/config.yaml` leaves the store out, so nothing on the machine clones, fetches, syncs, reads, renders or checks it. Setup, `lore sync`, reads and `lore doctor` show no error for it; doctor and `lore setup check` say once, as information, `acme's store ops is for the org's admins (audience: admins in lore-org.yaml); you are not an admin of acme, so lore does not clone or read it`.
- **An admin**: the entry lists it like any other store; `lore store list` shows `admins` in its AUDIENCE column; writes go there with `--to ops/<folder>`, reads with `--store ops` or from the setup's folders.
- **A member made admin** (`lore actor period edit --period @ann --org-role admin`): their next sync (any command that opens the org's store) copies the entry again with the store in it, with no command of their own. An admin whose stint ends loses it the same way; a clone already on disk stays there.

A store of the org (audience everyone) that a member's git login has no access to, or that is gone, never breaks their reads (devco D-726). When its fetch is refused (not found, permission denied, a folder they may not read; no answer at all still counts as offline), lore skips that store for this person: their other stores and their repo's own folder read, list and sync as normal, `lore whoami` still shows the `reads` row, and `lore whoami` and `lore doctor` name the store once, as information: `lore-secret: no access, skipped (…); ask an admin of acme for read access to it, then run lore sync`. The refusal is remembered until the next `lore sync`, which tries the store once more; any fetch that works ends it (a sync, a push, an open of the store, the link reader's own fetch). It only ever skips a store this machine has no copy of: one run whose fetch was refused (an editor's commit hook without the ssh agent, a run under other credentials) never hides a store you have fetched before, which is read from its copy as an offline store is, so `lore show --id <id> --setup <setup>` from the home folder and `lore show --id <id>` in a repo linked to one of its folders keep finding its rows (devco D-741). When a read by id finds no row, its refusal names the folders it looked in and each store it did not look in (`did not look in lore-secret (no access)`). Every command that walks all of the org's stores (folder lists, `lore project list`, a link picking its store, the setup's `lore sync` outside a repo) skips it the same way with one quiet line, and never asks it again in between. A write into that store refuses with the true cause, never "go online": `no access to lore-secret (…): your git login cannot read it, or it is gone`, with the hint to ask an admin of the org for read access (or to put you in the store's audience), then run `lore sync` and write again; a link that names it with `--store` refuses the same way.

Decisions (devco D-442):

- **The filter sits where lore-org.yaml is copied, not in each reader.** Every reader (setup, sync, doctor, reads, render, the link index, the leak and client checks) works from the org entry, so one copy step that leaves the store out covers them all, including readers added later. Who is an admin is read from the roster in the org store's clone at the commit lore-org.yaml was read at, by the same rule that decides who may change lore-org.yaml.
- **Client and leak checks never depend on it.** No repo can be linked to an admins-only store (`lore org link --store <it>` is refused): a repo's notes must be readable by everyone who works in the repo, and the link index skips the store on every machine, so whether a repo is own or client is the same for an admin and a member, and an unreachable admins' store never makes a repo "unknown" (guards on).
- **The org folder's store is never admins-only**: it holds the roster, teams and company rules every member reads; the command refuses it and the reader keeps it for everyone whatever the file says.
- **An `audience:` value lore does not know** is read as admins-only, so a member never tries a store the file does not plainly open to them; `lore config check` refuses it in `config.yaml`.
- **An older lore ignores the key** and treats the store as before (every member tries it): upgrade the team before relying on it.

## Team stores

A store can also be kept to one roster team, for example a client's notes that only the people working for that client may read: `lore org store add --org acme --url git@github.com:acme/lore-secret.git --audience team:@secret --push --yes` (or `lore org store audience --org acme --url git@github.com:acme/lore-secret.git --audience team:@secret --push --yes` for a store the org already lists). The store's entry in `lore-org.yaml` gets `audience: team:@secret`. Only an admin sets it (a member is refused with the same line as for `--audience admins`), and the team must already be on the org's roster (`lore team add --handle @secret --members @lee`, then `lore sync`); a team the roster does not hold is refused, naming the teams it does hold. As for admins-only stores, give only that team access to the repo on the git host.

What each person's lore does with it:

- **A member of @secret, and every admin**: the org entry lists it like any other store; reads, writes (`--to lore-secret/<folder>`), sync and links work as usual, and repos may be linked to its folders. `lore store list` shows `team:@secret` in its AUDIENCE column, `lore whoami` lists it under `stores` with `(audience team:@secret)`, and the `store <org>` line of `lore doctor` names every store of the entry with who uses it (`· stores lore-team (org folder), lore-secret (team:@secret)`).
- **The leak guard**: a client repo linked to a folder of a team-audience store takes that team as the client's team: rows of the org's stores with that same audience pass its leak checks, and rows of every other org store are internal there (docs/team/how-it-works.md, "Which stores are internal in a client repo").
- **A member outside @secret**: the copy of `lore-org.yaml` in their org entry leaves the store out, so their lore never clones, fetches, syncs, searches or renders it, and no command names it (not `lore whoami`, `lore doctor` or sync output): they have no reason to know it exists.
- **Added to the team** (`lore team member add --handle @secret --members @ann`): their next sync after the roster change reaches them copies the entry again with the store in it. **Removed from the team** (or the store's audience changed away from them): their next sync drops it and says so once, `lore-secret: no longer read: audience team:@secret, you are not on it; the clone at <path> is left as it is: lore store forget --url <url> --yes removes it`; no later sync repeats it.

**A lore too old for the audience.** Team audiences need lore 0.1.19 or later. An admin can make an older lore say so instead of skipping the store: `lore org config set --org <org> --key min_lore_version --value 0.1.19 --push --yes` turns lore 0.1.18 read-only on every store of the org, with its upgrade line (reads keep working). `lore org store add` and `lore org store audience` print that command for a team audience while the org's stores need less; they never set it themselves. lore 0.1.18 reads `team:@secret` as admins-only, so even a member of @secret gets no clone, no `reads` line in a repo linked to the store and no word about it: run `lore upgrade`, then `lore sync`. From lore 0.1.20 on, an audience kind lore does not know (one a newer lore adds) is kept as unknown instead of skipped silently: the store stays off a member's machine, `lore whoami` shows an `audience` row and `lore doctor` a warning with the fix `lore upgrade`, both naming the store: `acme's store lore-secret is for <audience> (audience in lore-org.yaml); this lore <version> cannot read that audience, so it does not clone or read the store: upgrade with lore upgrade, then run lore sync` (devco D-734).

Decisions (devco D-727): team membership is read from the roster's `teams` rows in the org store's clone, at the commit `lore-org.yaml` was read at, in the same copy step as the admin check, so every reader follows it; a team that left (inactive) counts for nobody; the org folder's store is never a team store.

## What lore takes from a store, and from whom

A store is a plain git repo, and everyone who can push to it can commit anything there with git alone. So lore checks what it reads instead of trusting the branch as it stands:

- **The org's `lore-org.yaml` is changed by an admin only.** Its store list, `own:` and `repos:` patterns and defaults decide where lore clones from and pushes to. Every lore reads the file's history and takes a change only from a commit whose git author is an admin of the org in the roster at that commit, the same trusted roster every reader replays for roster changes (an open stint with org role admin that an admin gave; a stint a member edited to admin by hand never counts; for an org made before org roles, the person who created the org). A change by anyone else is held back: lore keeps the version it had, never clones, reads or pushes to a store only that change added, and `lore sync` names it: `lore: org acme: commit 3e07be6 by Kai Lane <kai@example.com> ("tidy") changed org/lore-org.yaml: held back, Kai Lane is not an admin of the org at that commit …`. An admin settles it by making the change with lore (`lore org store add`, `lore store set-url`), or by reverting the commit in the store clone (`git revert <sha>`, then `lore sync`). A change that only moves or reformats the file is taken from anyone, so lore's own layout moves never hold it back. A merge commit that leaves a version of the file neither side had (a hand-made merge resolved with an edit) is judged the same way, as its author's change; revert such a merge with `git revert -m 1 <sha>`. The changes come from the roster replay lore already keeps, so no command walks the whole history to check the file. A machine that synced such a change before this check may still hold a clone of the outside repo under `~/.lore/stores`: `lore doctor` names it (`store_trust`: a store clone no org, setup or repo on this machine uses), and `lore fix --yes` removes it when it holds no commit of its own (one that does is named, never removed).
- **Files lore does not own are skipped, never fatal.** A repo with `lore-store.yaml` at its top stays a lore store whatever else is pushed into it: a stray file, a folder lore does not know, a broken row. lore reads the rest as before, and `lore sync` names each file with the commit and author that added it and the step to drop them (`git -C <clone> rm -r -- <file>`, commit, `lore sync`). Only a repo without `lore-store.yaml` that holds other files (a code repo) is refused with `E_NOT_A_STORE`.
- **A store a repo's `.lore/stores.yaml` attaches on a host outside your org waits for your yes.** On a machine with an org, a store that the committed file attaches, which is not a store of your org, not under the same host and owner as one, not matched by its `own:` or `repos:` patterns, and that nobody attached on this machine before, is not cloned and its notes never reach your agent's context. `lore sync` names it with the commit that last changed the file; `lore store trust --name <store>` is your yes, recorded on this machine only (`~/.lore/state/trusted-stores.json`), and the next command clones and reads it.

Git authorship is not proof: anyone who can push can set any name and email on a commit. These checks raise the bar (a change has to claim an admin's identity, and the claim stays in the store's history), but they do not stop someone who forges it. Signed changes are not part of lore yet.

`lore doctor` lists the same three things under the check `store_trust`.

## How writes reach the remote

Your personal store (`me`), your org's stores and the stores a repo attaches in `.lore/stores.yaml` are git repos that hold only lore notes. By default nothing reaches a store's remote until you ask: every note you write to one is committed on your machine, lore says so once per command and store ("committed locally to store <name>"), and `lore sync` (or `lore store push --store <store>`, from any folder, or a `git push` in the store clone; outside a repo `<store>` is an org's name, the name of any store an org lists (`lore-ops`, a second store `lore org store add` added), `<org>/<store>` or a setup's name, as `lore store set-url --name` takes it, and `lore store show --store` and `lore store forget --url` take the same names; a name that is none of these is refused naming your orgs, their stores and your setups; the push says how many commits it sent and how many row files they changed, `sent 1 commit (0 row files)` for an org file such as `lore-org.yaml`, and `nothing to send` only when the remote already has every commit; `--json` gives the same numbers as `sent` and `rows`) sends what waits. This holds for every store write: notes, `--to me`, `lore secrets allow`, roster and team writes (`lore member add`, `lore team add`), links and `lore-org.yaml` (`lore org link`, `lore org create`), and store settings (`lore store set`, `lore store visibility`, `lore store folder add`). A command that only works in a code repo (a task edit there) writes nothing to your personal store. A store can instead push by itself (`batch`, or `now` for every write at once), a choice you make in the setup or with `lore store set`, and its old history can be folded into one commit. None of this applies to the code repo (`repo`): you push that one with your code.

### The three push modes

| Mode | What a write does | When it reaches the remote |
|---|---|---|
| `manual` (the default) | commits on your machine only | only when you run `lore sync` or `lore store push` (or `git push` in the store clone) |
| `batch` | commits on your machine only | when the oldest waiting write is `push_every` (default `5m`) old and you run any lore command that uses the store, on `lore sync`, on commands that need the remote current (`lore sync status`, `lore land`), when your coding tool starts or ends a session, and from the repo's git hooks lore installs (after a commit, before a push) |
| `now` (your opt-in) | commits and pushes | at the end of each command that wrote, one push per store (a command that fails part way pushes what it committed), roster and link writes included |

Each store commit is titled after what you did, in one form for every kind of record: the command and the record's id, as in `lore: memory add mem_01ab…` or `lore: task add tsk_01cd…` (with `(N rows)` when more of its own rows, such as a task list, went into the same commit). A title names only records the commit holds. A record lore writes on the side of your command is named too: your actor row when your first write registers you (or your agent) is listed, as in `lore: memory add mem_01ab… (+ actor act_02ef…)`, or gets its own commit, `lore: actor register act_02ef…`; a folder's project row, made at the folder's first write, is listed after your actor row in that write's commit, as in `lore: memory add mem_01ab… (+ actor act_02ef…, project prj_03aa…)`, and gets its own commit, `lore: project add prj_03aa…`, only when that write names no project. No text of a record goes into a commit title: not a note's body, not a task's or plan's title, not a folder's or person's name, since a title can hold private words as easily as a body and a commit title stays readable in `git log` after a delete or purge; `lore show --id <id>` gives the record. In `batch` and `manual` mode every write is still a commit on your machine, so a crash loses nothing, and your own reads see the note at once. A push (in every mode: `lore sync`, `lore store push`, a `batch` push, the push after a write in mode `now`) sends the waiting commits as **one** commit per author, `lore: batch N rows (memories 2, tasks 1)` with one line per note in its message, in the order they were written; one waiting commit goes out as it is, with its own title. Some commits keep their own commit, title and author, so the org's checks of who may change what judge exactly the same changes: a hand edit, a roster or membership change (`lore member add`, `lore team add`, your actor row on a first write), `lore-org.yaml` and the store's `lore-store.yaml`; a waiting commit a branch's own notes (a branch layer) were started on goes out as it is, with every commit before it, so the branch still lands. `lore batch undo`, `lore meta set --where`, `lore meta import`, `lore task delete --where`, `lore resource delete`, `lore task import` (sub-tasks included), `lore task add --template` and `lore task stale --close` write one commit per store, however many rows they change; one of them refused part way writes nothing. A store whose team wants one commit per write in its git history sets `squash=off` (`lore store set --store <store> --settings squash=off`); then a push sends the waiting commits as they are. Setting `LORE_LIVE_AUTOPUSH=0` still stops the push after a write, in every mode.

Your actor row and your machine profile live in one folder of a store, the folder that holds your row (the org folder, where the roster is), not in every folder you write to. A write in another folder of the same store (`task edit --id … --assign @lee` in `sync/`) commits only that row: one commit, with no `sync/actors/…` or `sync/machines/…` file beside it, since a folder's reads attach the org folder and find you there. A coding agent's first write in a store adds the agent's own row to that same folder, once, as its own commit; later writes carry only their rows. A folder of a store that holds no row of yours yet (a store with no org folder) still gets your actor row with your first write there.

lore has no daemon, timer or background process, and never starts one. A batch goes out only when something runs lore: a command you (or your coding tool) run anyway checks the age of the oldest waiting write and, once it is `push_every` old, pushes the batch inside that command. That push gets a short time limit, so a slow or unreachable remote never holds your command up; what did not go out stays committed for the next command. If nothing runs lore, the batch simply stays on your machine, safe. `lore store show`, `lore sync status` and `lore doctor` show how old the oldest waiting write is, and `lore doctor` warns once it is more than a day old. Run `lore sync` to push it.

Alice batches her personal store. Her two notes wait on her machine, together with the rows lore records about her and her machine on a first write; `lore store show` says how many commits are waiting, and `lore sync` pushes them as two commits: the first write, which also adds her actor row (a roster change keeps its own commit), and one batch of the rest:

<!-- example: store-push#batch -->
```console
# alice, in website
$ lore store set --store me --settings push=batch --settings push_every=10m
me: push batch every 10m, compact off
  set in    ~/.lore/config.yaml (personal:)
  epoch     0
$ lore memory add --body 'I always forget the staging VPN profile' --to me
lore memory add: store clone ~/.lore/stores/alice/lore-me: git user set to Alice <alice@agency.example> (your lore identity, was alice@devbox)
lore: committed locally to store me; lore sync sends it
lore: wrote ~/.lore/stores/alice/lore-me/_store/actors/act_7a67…9504.json (store me)
lore: wrote ~/.lore/stores/alice/lore-me/_store/memories/mem_2ad3…f891.json (store me)
lore: wrote ~/.lore/stores/alice/lore-me/_store/machines/mch_162d…9666.json (store me)
✓ mem_2ad3…f891 [me]
  scope: [project]
$ lore memory add --body 'I prefer short standups' --to me
lore: committed locally to store me; lore sync sends it
lore: wrote ~/.lore/stores/alice/lore-me/_store/memories/mem_54fe…28f2.json (store me)
✓ mem_54fe…28f2 [me]
  scope: [project]
$ lore store show --store me
me: push batch every 10m, compact off
  at        git@github.com:alice/lore-me
  used by   your setup agency (one personal store per person: any other setup of yours shares it)
  set in    ~/.lore/config.yaml (personal:)
  epoch     0
  waiting   3 local commit(s) not pushed yet, the oldest under 1m old (`lore sync` pushes them)
$ lore sync
lore: pushed store me: commit 601d285 to branch main (2 commits, 4 row files)
store me: pushed (sent 2, received 0, merged 0, refused 0); rows: 4 sent
store agency/client: up to date
store agency/org: up to date
$ git fetch -q https://github.com/alice/lore-me main && git log -1 --format=%B FETCH_HEAD
lore: batch 2 rows (machines 1, memories 1)
machines mch_162d…9666
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

- **Your personal store:** in the `personal:` block of `~/.lore/config.yaml` (keys `push`, `push_every`, `squash`, `compact`, `compact_after`). `lore store set --store me --settings …` writes it; `lore config set --key personal.push --value batch` works too, and `lore config check` checks the values.
- **An org's store:** in the store's own `lore-store.yaml`, so every member gets the same setting. Only an admin of the org may change it: the person this machine is in that org (`orgs[<org>].identity` in `~/.lore/config.yaml`) must hold the admin role on the org's roster (`lore member list` shows it), as the org's creator does from `lore org create` on, with or without an admin setup. An admin setup on a machine whose person the roster names a member gives no admin rights; only where this machine cannot read the roster (no clone of the org's store yet, or a store with no `lore-org.yaml`) does the admin setup decide. A member cannot override it on their own machine.
- **A store attached in `.lore/stores.yaml`** (`lore store init` or `lore store add`) that belongs to no org: in the store's own `lore-store.yaml` too, so every clone of it gets the same setting. There is no admin to ask, so anyone who can push to the store may change it with `lore store set --store <store> --settings …`, and compact it. If the store holds an org's `lore-org.yaml` (it is an org's store that this machine attached by its address without `lore org import`), it is that org's store and only the org's admin may change it.

The admin sets it once and every member's next sync picks it up:

<!-- example: store-push#org-settings -->
```console
# alice, in website
$ lore store set --store agency --settings push=batch --settings push_every=15m && lore sync
committed locally to store agency; lore sync sends it
agency: push batch every 15m, compact off
  set in    the store's lore-store.yaml (org agency; set by an org admin, which this machine is)
  epoch     0
  waiting   1 local commit(s) not pushed yet, the oldest under 1m old (`lore sync` pushes them)
  pushed    2026-08-03T09:00:00Z
lore: pushed store agency/client: commit 86dbae4 to branch main (1 commit)
store me: up to date
store agency/client: up to date
store agency/org: up to date

# bob, in website
$ lore sync >/dev/null && lore store show --store agency
agency: push batch every 15m, compact off
  at        git@github.com:agency/notes
  set in    the store's lore-store.yaml (org agency; set by an org admin)
  epoch     0
  folders   client, org
  pushed    2026-11-02T10:00:00Z
  waiting   nothing: every local commit is pushed
  clone     ~/.lore/stores/agency/notes   default branch main   last fetch 2026-11-02T10:01:10Z
```
<!-- /example -->

`lore store show --store <store>` prints a store's settings and, as `lore store show` with no name does for every store, its url (`at`), its folders, its unpushed commits (`waiting   nothing: every local commit is pushed` when none wait) and this machine's clone with its last fetch; `--json` gives the settings under `"push"` (folders in `"push"."folders"`) and the clone under `"stores"`. With no store name, `lore store show` lists the stores of your orgs as before. `lore org list` lists this machine's org entries: each with its stores (the org folder one and an admins-only one marked), the setups that use it with their role, and a count line.

The values `lore store set` takes:

| Key | Values | Default |
|---|---|---|
| `push` | `manual`, `batch`, `now` | `manual` |
| `push_every` | how long a batch may wait, at least `1m` (such as `10m`, `1h`) | `5m` |
| `squash` | `on`: a push folds the waiting commits into one commit per author (a hand edit, a roster change, `lore-org.yaml` and `lore-store.yaml` keep their own); `off`: one remote commit per write | `on` |
| `compact` | `on` or `off`: fold old history by itself (below) | `off` |
| `compact_after` | how old history must be to be folded, at least `1d` (such as `30d`); `compact=on` needs it | none |
| `compact_archive` | `on`: compaction keeps the replaced history in an archive and every copy checks the rewrite (below); while this is new, a compaction needs it on and the run `LORE_COMPACT_V2=1` | `off` |

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
ERROR: [E_REFUSED] store set: store agency belongs to org agency and only an admin of agency may change its shared settings; human:bob@agency.example (this machine's identity in agency) holds no admin role on the roster of agency
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

Compaction is behind a flag while it is new: the store needs `compact_archive=on` (`lore store set --store <store> --settings compact_archive=on`) and the run `LORE_COMPACT_V2=1`; without them `lore store compact` and `compact=on` refuse with `E_REFUSED` and change nothing. On an org's store the org's `min_lore_version` must also be 0.1.57 or later (`lore org config set --org <org> --key min_lore_version --value 0.1.57 --push --yes`): a lore older than that takes a higher epoch without checking it, so it has to be read-only on the org's stores first; until then the compaction refuses with that command. A `--dry-run` never refuses for it: it prints its plan and adds `the real run would be refused now` with that command.

- The cutoff is `--before` (such as `--before 30d`), else the store's `compact_after`, else `30d`. A later merge whose other side started before the cutoff, or an open branch layer based before it, moves the cut back to where they start, so every kept merge keeps both parents and every open layer can still land.
- `--dry-run` says what would change and changes nothing.
- It asks first. Without a terminal it stops with `E_NEEDS_CONFIRM` (exit 26) until you pass `--yes`.
- Before anything is rewritten, the old history is pushed to an **archive**: the ref `refs/lore-archive/epoch-<n>` in the store's own repo (no copy downloads it), or the repository `archive_url:` in `lore-store.yaml` names. If that push is refused, nothing is rewritten.
- The snapshot is the store as lore reads it, not as the files are: a change lore holds back (a member making themselves admin by hand, say) is in the snapshot as its last accepted version, so a compaction never turns a held change into a trusted one. The output counts them (`held change(s) are in the snapshot as their last accepted version`).
- Only an admin of the store's org compacts, checked against the store's own roster; your personal store is yours.
- The rewrite is pushed only if nobody pushed since lore last looked (`E_CONTENDED` otherwise; run it again). Offline it is `E_OFFLINE`. In both cases nothing changes.
- Branch layers, open and in the trash, are moved onto the new history and pushed with it, so they still land and every copy downloads only the new history. A trashed layer older than the cut cannot move and keeps its ref; the output says how many (`layer ref(s) older than the cut still reach the old history`).
- The old history also stays on your machine under `refs/lore/backup/compact-<epoch>-<commit>`.

<!-- example: store-push#compact only=2,3,4,5 -->
```console
# alice, in website
$ lore store set --store agency --settings compact_archive=on
committed locally to store agency; lore sync sends it
agency: push batch every 15m, compact off
  set in    the store's lore-store.yaml (org agency; set by an org admin, which this machine is)
  epoch     0
  waiting   1 local commit(s) not pushed yet, the oldest under 1m old (`lore sync` pushes them)
  pushed    2026-11-02T10:01:05Z
$ LORE_COMPACT_V2=1 lore store compact --store agency --before 30d --dry-run
would squash 5 commit(s) of agency before 2026-10-03 into one snapshot, keep 3 later commit(s) and every one of its 5 row file(s); epoch 0 → 1
$ LORE_COMPACT_V2=1 lore store compact --store agency --before 30d
ERROR: [E_NEEDS_CONFIRM] store compact agency: rewrite the history of store agency: squash 5 commit(s) before 2026-10-03 into one snapshot (every row is kept; every clone re-fetches at epoch 1)?
  hint: ask the person, then rerun with --yes
(exit 26)
$ LORE_COMPACT_V2=1 lore store compact --store agency --before 30d --yes
✓ compacted agency: 5 commit(s) before 2026-10-03 are one snapshot, 3 later commit(s) kept, all 5 row file(s) kept; epoch 0 → 1 (old history archived as refs/lore-archive/epoch-0)
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

Nothing to do. On Bob's next sync lore sees the higher epoch and first checks the rewrite against his own copy: it re-does the compaction from his own history (fetching the archive when his copy lacks the old tip) and takes the new history only when every commit comes out the same and the person who compacted was an admin there. Then it moves his copy onto the new history instead of merging the two, and replays his unpushed notes on top as one commit, `lore: replay local work onto epoch N` (a note both sides changed is merged field by field, as always). His old copy stays under `refs/lore/backup/epoch-<n>-<commit>`. Here Bob had written a note that was still waiting for his next batch when Alice compacted:

<!-- example: store-push#compact only=1,6,7,8 -->
```console
# bob, in website
$ lore memory add --body 'the client wants the invoice on the 1st' --to agency
lore: committed locally to store agency/client; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/client/memories/mem_05e9…4603.json (store agency/client)
✓ mem_05e9…4603 [agency/client]
  scope: [project]
$ lore sync
lore: pushed store agency/client: commit 05513aa to branch main (1 commit, 1 row file)
store agency/client: pushed (sent 1, received 5, merged 0, refused 0); rows: 0 received
store agency/org: up to date
$ lore memory list
mem_c0f3…b5b8 [retrieved] the client signs off designs on Thursdays [agency/client]
mem_76bd…3f10 [retrieved] staging resets every Sunday night [agency/client]
mem_05e9…4603 [retrieved] the client wants the invoice on the 1st [agency/client]
$ git fetch -q https://github.com/agency/notes main && git log --format=%s FETCH_HEAD
lore: replay local work onto epoch 1
lore: compact store history (epoch 1)
lore: store set agency
lore: store set agency
lore: actor register act_d431…313a
lore: snapshot of the store history before 2026-10-03
```
<!-- /example -->

A rewrite lore cannot re-do that way (a higher epoch someone pushed by hand, a compaction by a member, or a snapshot that differs from what the old history allows) is refused on every copy: the sync says `the store's history was rewritten to epoch N by <name> <email> and this clone cannot verify it (<why>); it stays on its own history: check it with lore store compact --store <url> --verify and ask an admin`, pushes nothing and keeps the copy as it was. A new copy made after a compaction starts from the snapshot and trusts it as any copy trusts a store's first commit; `lore store compact --store <store> --verify` fetches the archive, replays its roster and re-does the rewrite, back through every earlier compaction:

<!-- example: store-push#compact only=9 -->
```console
# bob, in website
$ lore store compact --store agency --verify
✓ store agency: epoch 1 verified against its archive refs/lore-archive/epoch-0 (old tip dfd8dc87ab36): the snapshot is the trusted view of the old history and every kept commit is unchanged
```
<!-- /example -->

To put the old history back, an admin runs `lore store compact --store <store> --undo-epoch <n>`, where `<n>` is the epoch the newest compaction replaced (`lore store show` names the current one): the archived history comes back with every write made since on top of it, as the next epoch (epochs only go up, so every copy takes it through the same check), and the compacted history is archived in turn. It asks first, like the compaction, and is behind the same flag.

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
