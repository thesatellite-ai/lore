# Your identity, joining an org, and store housekeeping

**In short:** lore needs to know who you are, so it can write your name on the notes and tasks you add. Your identity is your email address. This page shows how to check and change it, join your company's setup in one command, move a note that went to the wrong place, and tidy the copies lore keeps on your machine.

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
source:      git config
stable:      yes
```
<!-- /example -->

## Step 2 — Set or remove your own identity: `lore identity set` and `unset`

Outside any company, `lore identity set` saves a fallback identity in `~/.lore/identity.yaml`. Use your email: your **identity key** is your email address with `human:` in front, and lore uses it, not your display name, to tell people apart. `lore identity unset` removes the file, and lore goes back to your git settings:

<!-- example: people#identity only=2,3,6,7 -->
```console
# alice, in ~
$ lore identity set alice@example.com
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
source:      git config
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
source:      org entry (config.yaml)
stable:      yes
```
<!-- /example -->

## Step 4 — Join your company's setup in one command: `lore org import`

Writing the org entry by hand is easy to get wrong. Instead, someone at the agency commits a suggested entry, `lore-org.yaml`, into the org folder of the store once. It names the stores and which repos belong to whom, and never anyone's identity:

<!-- example: people#suggest-an-org-entry -->
```console
# alice, in notes-checkout
$ git add org/.lore/lore-org.yaml && git commit -qm 'suggest the agency org entry' && git push -q origin main
```
<!-- /example -->

Then each new person runs one command with their own email, from any folder: no repo is needed. Lore shows the entry, adds it to their config and puts them on the roster (the org's list of people), opening only the org's store. If the roster step fails (the store cannot be reached, say), the entry is still added and the command exits non-zero, telling you to run `lore actor register` again later:

<!-- example: people#org-import -->
```console
# bob, in ~
$ lore org import git@github.com:agency/notes --key human:bob@agency.example --name 'Bob Roe' --handle bob --yes
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

# bob, in website
$ lore whoami
org        agency  (matched by: link in <redacted>, project id + remote)
identity   Bob Roe <human:bob@agency.example>   act_7a67…9504  @bob  (from: org entry (config.yaml))
repo       client repo  (guards on)
machine    mch_fac0df3a  (mch_fac0df3a618f3d99d93273abc59a9ece0b7cedbb24ea7cd8e8af24a257cfe1bc)
agent      none  (no coding agent detected)
project    prj_2ad3…f891
remotes    github.com/client/website
stores     <redacted> on branch main  (org folder <redacted>)
internal   <redacted> <redacted>/
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   local: agency/, everywhere/
```
<!-- /example -->

## Step 5 — Let a repo name its org for everyone: `lore org set`

In the agency's own repos (not a client's), you can commit `.lore/org.yaml`, a one-line file that says which org the repo belongs to. Teammates then get the right org there without any config of their own. Lore refuses it in a client repo, because it would tell the client the agency's org name:

<!-- example: people#org-set -->
```console
# alice, in tools
$ lore org set agency --commit
✓ wrote ~/work/tools/.lore/org.yaml (org agency)
  commit it with git so teammates resolve the same org
$ cat .lore/org.yaml
org: agency

# alice, in website
$ lore org set agency --commit
ERROR: [E_INVALID_INPUT] refusing to write .lore/org.yaml: .lore/org.yaml names agency, but no remote (github.com/client/website) matches its own: patterns; ignored (only an org's own repos may name it)
  hint: only an org's own repos (matching its own: patterns) may name it; use `lore org use` for a machine-only choice
(exit 20)
```
<!-- /example -->

## Step 6 — Move a note to where it belongs: `lore move`

Alice wrote a note into the client's repo by mistake, where the client can read it. `lore move` copies it to the agency's internal project with a new id and deletes the original:

<!-- example: people#move -->
```console
# alice, in website
$ lore memory add --body 'the client replies faster on WhatsApp than email' --to repo
✓ mem_162d…9666
  scope: [master]
$ lore move mem_162d…9666 --to agency --yes
This becomes visible to everyone with access to store agency:
  "the client replies faster on WhatsApp than email"
✓ mem_162d…9666 moved to store agency (same id; the old copy is tombstoned)
$ lore memory list
lore sync: trashed rows 1
lore sync: rows whose file disappeared are kept in `lore trash --local` (undo: `lore trash restore --local <id>`)
mem_162d…9666  the client replies faster on WhatsApp than email  [agency/client]
```
<!-- /example -->

If the original was already pushed or committed, the client may have seen it; see [Cleaning up a leak](use-cases/leak-cleanup.md).

## Step 7 — Look inside a store, or drop your copy: `lore store open` and `forget`

Lore never needs a working folder of a store; it reads and writes the store itself. To browse the files, `lore store open` makes a read-only folder. `lore store forget` deletes this machine's copy of a store. The next command that needs it downloads it again:

<!-- example: people#store-open-and-forget -->
```console
# alice, in ~
$ lore store open git@github.com:agency/notes
✓ ~/.lore/worktrees/github.com/agency/notes/main at main (601d2850ffd0, detached; read-only by convention — lore writes the branch itself)
$ ls ~/.lore/worktrees/github.com/agency/notes/main
client
lore-store.yaml
org

# bob, in ~
$ lore store forget git@github.com:agency/notes
✓ forgot <redacted> (removed ~/.lore/clones-v017/github.com/agency/notes.git; the next command that needs it clones it again)
```
<!-- /example -->

## Step 8 — Clean up old caches: `lore cache prune`

Lore keeps a cache file per attached store in `~/.lore/cache`. `lore cache prune` removes the cache files of stores that no repo on this machine attaches any more (a removed checkout, a `lore store remove`). It lists each file with its size and asks before deleting anything: `--dry-run` only lists, `--yes` deletes without asking, and without a terminal it refuses with `E_NEEDS_CONFIRM` unless you pass `--yes`. It never touches row files or anything outside `~/.lore/cache`; a store you attach again rebuilds its cache on the next command.

<!-- example: people#cache-prune -->
```console
# alice, in ~
$ lore cache prune --dry-run
cache files no repo on this machine attaches:
  ~/.lore/cache/stores/sto_54fe…28f2/main/root.db  2.5 MB
1 file(s), 2.5 MB
--dry-run: nothing removed
```
<!-- /example -->

## Pitfalls

- **The identity key is an email, never a name.** `lore identity set "Alice Doe"` is refused (`E_INVALID_INPUT`), because the same person on another machine with another spelling would become a second person. To keep a display name, pass the email too: `lore identity set "Alice Doe <alice@example.com>"` or `lore identity set "Alice Doe" --key human:alice@example.com`.
- **`lore identity show` does not show anonymous mode.** After `anonymize on` it still prints your normal identity; the `anon:…` name is used only for the writes that follow.
- **`lore org import` asks before risky defaults.** A suggested entry that sends rows into client repos, or a pattern that matches a whole host, needs your yes. `--yes` accepts it without a prompt, so read the printed entry first.
- **`lore org set` is refused in client repos.** Use `lore org use <org>` there: it is a choice for your machine only and is never committed.
- **`lore move` is refused for agents with `--yes`.** Moving a note is a person's decision.
- **`lore store forget` refuses while the copy holds work that is not pushed yet.** Run `lore sync` first.
- **Never edit the folder `lore store open` made.** Lore writes the store branch itself; your edits there are not picked up.

## Related

- [Getting started](getting-started.md): the first config with an org.
- [Two companies on one laptop](use-cases/two-companies.md): one identity per org.
- [A contractor who sees only one client](use-cases/contractor.md): store access per person.
- [Agents](agents.md): agent identities (`lore agent start`).
- [Commands](commands.md): every flag of `identity`, `org import`, `org set`, `move`, `store`, `cache prune`.
