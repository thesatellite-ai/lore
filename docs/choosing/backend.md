# Which storage backend and cache

**In short:** you never pick a database. Every note is one small JSON file, and lore writes those files to one of two kinds of place: a folder (the repo's own `.lore/data`, or a folder store given as `file:///…`) or a git store (a private notes repo given as `git@…`, `ssh://…` or `https://…`). Next to every place lore keeps a SQLite cache, a fast index it rebuilds from the files whenever it needs to. So the real choice is only this: does a note go in the repo, in a folder on this machine, or in a git repo of its own?

## The options in one table

| Option | How you choose it | What lore does with the files | Who sees the notes | Its cache |
|---|---|---|---|---|
| **The repo** (always there) | `lore init`; every write without `--to` | writes `.lore/data/<table>/<id>.json` in your checkout and never commits: you commit the files with your code | everyone who can read the repo | `~/.lore/cache/repo/<repo folder>-<hash>.db`, in lore's home, never in the repo |
| **Folder store** | `lore store init --name <name> --url "file:///<absolute path>"`, then `--to <name>` | writes the same files under `<folder>/<table>/` (the store's own rows under `_store/<table>/`), with no git and no network | only people on this machine | `~/.lore/cache/stores/<store id>/local/<folder>.db` |
| **Git store** | `lore store init --name <name> --url git@github.com:acme/notes` (or `ssh://`, `https://`, `http://`, or the path of a bare git repository on this machine, `/srv/git/notes.git`, the way `git clone` takes it; `file:///srv/git/notes.git` is read the same), then `--to <name>` or `lore org link` | keeps a plain git clone at `~/.lore/stores/<owner>/<repo>` (`~/.lore/stores/_local/<repo>` for a repository on this machine; or your own clone: `lore store use --path <path>`), makes one commit per write and pushes it at once; fetches again when the last fetch is older than `live.fetch_ttl` (60 seconds) | everyone GitHub lets open that repo | `~/.lore/cache/stores/<store id>/<branch>/<folder>.db` |
| **Personal store `me`** | `personal.store` in `~/.lore/config.yaml`; `lore store init --name me` with no address makes `file://~/.lore/stores/_me` | a folder store or a git store, as its address says; `--to me` writes there | only you | as for its kind |
| **Live list** of the repo | `--live`, and tasks, missions, handoffs and comments by default | rows on the hidden ref `refs/lore/live` of the repo's own remote, pushed and fetched by lore | everyone who can clone the repo | the repo cache |
| **Machine store** (automatic) | nothing: runs, sessions and other rows that only mean something on this machine | `~/.lore/state/machine.db`, never synced | only this machine | `~/.lore/cache/machine-store.db` |

Both store backends lay rows out byte for byte the same way (`<store root>/<folder>/<table>/<id>.json`, the store's own rows in `<store root>/_store/<table>/<id>.json`, a `README.md` and a `lore-store.yaml` marker at the root; see [Stores as plain folders](../sync/store-clones.md)), so a folder store and a git store differ only in how the files travel. The older shared-database setup (v0.1.17 "Mode B": one database file and a `.lore/lore.yaml` pointer in each repo) is gone; `lore migrate run` turns such a repo into one that uses a store (see [Where your notes live](database-mode.md)).

## The cache

The cache is never the truth. Lore keeps every row it read from the files and builds its tables from them, so search, lists, `lore sql` and `lore render` are fast. When a lore upgrade changes a table, or a cache file is damaged, lore drops it and builds it again from the files on the next command, and a damaged cache file is first moved aside beside itself (`<file>.corrupt-<time>`). You never commit it and never back it up.

- Each clone and each git worktree has its own repo cache, `~/.lore/cache/repo/<repo folder>-<hash>.db`, in lore's home and never in the repo (a moved or renamed repo folder gets a fresh one).
- Caches of store folders live under `~/.lore/cache/stores/`, named by the store's permanent id, so every repo on the machine that attaches the same store folder shares one file.
- A repo that attaches more than nine store folders gets one combined cache, `~/.lore/cache/combined/<hash>.db`, instead of one file per folder.
- `lore cache prune` lists the cache files of stores no repo on this machine attaches any more, and removes them after asking.

## If this, then that

- **If the notes belong with the code,** do nothing: the repo is the default, and the notes travel with commits, branches and pull requests.
- **If a note must never be committed to this repo** (your rates, notes about a client), write it `--to` a store. Use a folder store when only you need it on this machine, a git store when a teammate or a second machine needs it.
- **If you want your personal notes on two machines,** make `personal.store` a private git repo of your own (`lore store init --name me --url git@github.com:octo-personal/lore-me`), not a folder.
- **If your company shares notes across repos and people,** use a git store and link the repos to it with `lore org link` (see [How to lay out your stores](store-layout.md)).
- **If you started with a folder store and now need to share it,** push the folder to a private git repo (the files are already in the git layout) and point the store at it with `lore store set-url --name <name> --url <url>`.
- **If a repo should only read a store,** attach it with `lore store add --name <name> --url <url> --read-only`.
- **If the store's notes may appear in committed files** (the rendered `.lore/LORE.md`, a client repo), mark it `lore store visibility --name <name> --visibility shareable`; the default `internal` keeps them out.
- **If lore feels slow or a cache looks wrong,** you never need to delete anything by hand: `lore doctor` checks it, and `lore search rebuild` rebuilds the search index.

## Pitfalls

- **A folder store is not backed up.** It lives only on this machine; back it up yourself or use a git store.
- **No store inside a cloud-sync folder or on a network drive.** Sync clients upload half-written files and ignore lore's lock, so lore refuses such a place with `E_NETWORK_FS`; `LORE_ALLOW_NETWORK_FS=1` overrides it at your own risk.
- **`file://~/…` is not expanded.** Write `file://$HOME/…` so your shell fills in the path.
- **A store must carry its marker.** An address without `lore-store.yaml` is refused with `E_NOT_A_STORE`; make it with `lore store init`. A repo that holds code is never a store.
- **Live rows only ride the repo's own remote.** A note written `--to` a store is never live; it travels with the store instead.
- **A git store pushes at once, so it needs the network.** With `LORE_LIVE_AUTOPUSH=0` writes wait in the local clone until `lore sync`.

## Try it

A folder store and the repo side by side: where the note files go, that the repo cache is ignored by git, and where the store's cache lives. Then a store inside a cloud-sync folder is refused.

<!-- example: choosing#where-the-files-are -->
```console
# alice, in website
$ lore store init --name notes --url file://$HOME/stores/notes
✓ created store notes (sto_7a67…9504) at file://~/stores/notes; recorded in .lore/stores.yaml; commit it to share
$ lore memory add --to notes --body 'The client pays invoices on the 15th.'
lore: wrote ~/stores/notes/_store/actors/act_2ad3…f891.json (store notes)
lore: wrote ~/stores/notes/_store/memories/mem_162d…9666.json (store notes)
lore: wrote ~/stores/notes/_store/machines/mch_54fe…28f2.json (store notes)
✓ mem_162d…9666 [notes]
  scope: [master]
$ lore rule add --body 'Run the tests before you push.'
lore: wrote ~/work/website/.lore/data/rules/rul_05e9…4603.json
✓ rul_05e9…4603
  scope: [master] severity: must
$ find ~/stores/notes -type f | sort
~/stores/notes/.lore-state/lock
~/stores/notes/README.md
~/stores/notes/_store/actors/act_2ad3…f891.json
~/stores/notes/_store/machines/mch_54fe…28f2.json
~/stores/notes/_store/memories/mem_162d…9666.json
~/stores/notes/lore-store.yaml
$ ls .lore/data/rules
rul_05e9…4603.json
$ ls -a .lore
.
..
data
lock
state
stores.yaml
$ find ~/.lore/cache -name '*.db' | sed -e 's#/sto_[0-9a-z]*/#/<store id>/#' -e 's/-[0-9a-f]\{12\}\.db/-<hash>.db/' | sort
~/.lore/cache/machine-store.db
~/.lore/cache/repo/website-<hash>.db
~/.lore/cache/stores/<store id>/local/root.db
```
<!-- /example -->

<!-- example: choosing#cloud-sync-folder -->
```console
# alice, in website
$ lore store init --name synced --url file://$HOME/Dropbox/notes
ERROR: [E_NETWORK_FS] rowfs: the store is inside a "dropbox" folder (cloud sync)
  hint: move the store to a local folder, or set LORE_ALLOW_NETWORK_FS=1 to accept the risk
(exit 23)
```
<!-- /example -->

## Guides and examples for each option

| Option | Guide | Example |
|---|---|---|
| The repo | [How notes travel with git](../sync/how-notes-travel-with-git.md) | [`sync`](../../tests/docs/sections/sync.sh) ([output](../../tests/docs/transcripts/sync.txt)) |
| Folder store | [Your stores: list, move, retire](../sync/stores.md), [One store for several repos](../sync/shared-database-mode-b.md) | [`stores`](../../tests/docs/sections/stores.sh) ([output](../../tests/docs/transcripts/stores.txt)) |
| Git store | [Agency with a client repo](../team/use-cases/agency-client-repo.md), [Identity and stores](../team/identity-and-stores.md) | [`agency`](../../tests/team/docs/transcripts/agency.txt), [`people`](../../tests/team/docs/transcripts/people.txt) |
| Personal store | [Personal notes](../team/use-cases/personal-notes.md) | [`personal`](../../tests/team/docs/transcripts/personal.txt) |
| Live list | [Keep a note with the code, or share it at once](with-code-or-live.md) | [`small-team`](../../tests/team/docs/transcripts/small-team.txt) |
| Machine store, runs | [Run log](../tasks/run-log.md) | [`runs`](../../tests/docs/sections/runs.sh) ([output](../../tests/docs/transcripts/runs.txt)) |
| The cache, damaged and rebuilt | [Doctor](../operations/doctor.md), [Backup, restore and repair](../operations/backup-restore-repair.md) | [`ops`](../../tests/docs/sections/ops.sh) ([output](../../tests/docs/transcripts/ops.txt)) |
| Where the files land | this page | [`choosing`](../../tests/docs/sections/choosing.sh) ([output](../../tests/docs/transcripts/choosing.txt)) |

## Related

- [Where your notes live](database-mode.md)
- [How to lay out your stores](store-layout.md)
- [All decision guides](README.md)
