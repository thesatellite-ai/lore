# Your stores: list, move, retire

**In short:** a store is a place for lore notes outside the client's repo. This page covers the everyday commands for one person's stores: see which stores a repo uses (`lore store list`), read the marker that makes a folder a store (`lore-store.yaml`), decide whether its notes may reach client repos (`lore store visibility`), follow a store that moved (`lore store set-url`), split it into folders (`lore store folder add`), move a note into it (`lore row move`) and detach it (`lore store remove`).

## What it is and when you want it

Every repo with lore keeps its own notes in `.lore/data`, committed with the code. A **store** is a second place: a folder on your machine (`file://…`) or a private git repo that holds lore notes and nothing else. A repo attaches a store in `.lore/stores.yaml`, and from then on `--to <store>` writes a note there and every list shows it with the store's name in brackets.

You want these commands when:

- you keep notes that must never be committed into the client's repo (your rates, your own habits, notes about other clients), so they live in a store next to it;
- a note was written into the repo by mistake and should move to the store, keeping its id;
- the store folder or store repo moved or was renamed, and every repo that attaches it must follow;
- a store has grown and you want one folder per topic;
- you no longer want a repo to see a store.

Making a store and sharing one store across several repos is covered in [One store for several repos](shared-database-mode-b.md); git stores shared by a team are covered in [Lore for teams](../team/README.md).

## Before you start

- lore installed and the repo set up with `lore init` (here Alice's checkout of `github.com/client/website`).
- A place for the store. These examples use a folder store under `~/stores`. Write the address with a full path, `file://$HOME/stores/notes`, so your shell fills in `$HOME`.

## Step 1 — Make a store and look at it

`lore store init --name <name> --url <url>` turns the folder into a store and attaches it to this repo. The folder gets one file, `lore-store.yaml`: the marker that says "this is a lore store", with the store's permanent id (`sto_…`) and its visibility. The repo gets one line in `.lore/stores.yaml`. `lore store list` shows every store the repo sees, in the order lore looks in them, and whether each one can be reached right now (`REACH`):

<!-- example: stores#store-init -->
```console
# alice, in website
$ lore store init --name notes --url file://$HOME/stores/notes
✓ created store notes (sto_7a67…9504) at file://~/stores/notes; recorded in .lore/stores.yaml; commit it to share
$ cat .lore/stores.yaml
stores:
    - name: notes
      url: file://~/stores/notes
$ ls ~/stores/notes; cat ~/stores/notes/lore-store.yaml
README.md
lore-store.yaml
lore_store: 4
id: sto_7a67…9504
visibility: internal
created: "2026-11-02"
$ lore store list
stores:
  NAME   URL                                                                     FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE       VISIBILITY  REACH  ADDRESS
  notes  file://~/stores/notes          false              false     stores.yaml  internal    ok     
count: 1
```
<!-- /example -->

The id in the marker never changes, even when the store moves; that is how lore knows a moved store is still the same store (Step 3). Commit `.lore/stores.yaml` if teammates should attach the same store; a `file://` store only exists on your machine, so for a personal folder store you may leave the file uncommitted.

## Step 2 — Decide whether its notes may reach client repos

A new store is `internal`: its notes show in your lists but never render into committed files such as `CLAUDE.md` or `AGENTS.md`, and never reach a client repo. `lore store visibility --name <name> --visibility shareable` lifts that. It asks first, because the next render may write the store's notes into files the client sees; without a terminal, answer with `--yes`. The setting is written into the store's own `lore-store.yaml`, so every repo that attaches the store follows it. Turning it back to `internal` names the repos to render again, since their committed files may still hold the store's notes:

<!-- example: stores#visibility -->
```console
# alice, in website
$ lore store visibility --name notes --visibility shareable
ERROR: [E_NEEDS_CONFIRM] store visibility notes: rows of this store will render into committed files and show in client repos; make it shareable?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore store visibility --name notes --visibility shareable --yes
✓ notes is shareable (was internal; lore-store.yaml written)
$ grep visibility ~/stores/notes/lore-store.yaml
visibility: shareable
$ lore store visibility --name notes --visibility internal --yes
✓ notes is internal (was shareable; lore-store.yaml written)
  re-render these repos (`lore render`): their committed files may hold its rows
    ~/work/website
```
<!-- /example -->

## Step 3 — Follow a store that moved

If the store folder moves (or the store's git repo is renamed), `lore store list` shows the old address as `missing`. `lore store set-url --name <name> --url <new-url>` records the new address in `.lore/stores.yaml`. It opens the new address first and refuses one that holds no store marker (`E_NOT_A_STORE`), and when the old address still opens it refuses a new address with a different `sto_` id, so a typo cannot point the name at another store. It also re-points the store's folder entries (`notes/golang`) and lists them (`folder entries re-pointed: notes/golang`); running it again with the current address repairs entries an older lore left on the old address. The store's id and notes stay as they were:

<!-- example: stores#store-moved -->
```console
# alice, in website
$ mv ~/stores/notes ~/stores/agency-notes
$ lore store list
stores:
  NAME   URL                                                                     FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE       VISIBILITY  REACH    ADDRESS
  notes  file://~/stores/notes          false              false     stores.yaml              missing  
count: 1
$ lore store set-url --name notes --url file://$HOME/stores/agency-notes
✓ store notes (sto_7a67…9504) is now at file://~/stores/agency-notes (was file://~/stores/notes); its rows stay
  now naming it: .lore/stores.yaml notes
  note: the old address could not be read (E_NOT_A_STORE), so its store id was not compared
$ lore store list
stores:
  NAME   URL                                                                            FOLDER  READ_ONLY  BRANCH  PERSONAL  SOURCE       VISIBILITY  REACH  ADDRESS
  notes  file://~/stores/agency-notes          false              false     stores.yaml  internal    ok     
count: 1
```
<!-- /example -->

A git store's clone folder follows its address, so a renamed store repo would get a new clone. The same goes the other way: renaming a clone folder under `~/.lore/stores` by hand makes the next command clone the store again at its path, and lore then names the renamed folder as another clone of the same store that it no longer reads or writes, so you can copy what you need from it and delete it. Notes written while the old address was gone are commits in the old clone only, so `lore store set-url` carries that clone to the new address instead (it prints `the clone ~/.lore/stores/lore-me (1 unpushed commit(s)) now fetches from and pushes to …`) and the next `lore sync` pushes them. When the new address already has a clone with work of its own, set-url refuses (`E_REFUSED`) naming both clones and changes nothing. `lore doctor` warns (`store_clone`) about a clone an older lore left at an old address with unpushed commits; its fix, `lore store set-url --name <sto_id> --url <url>`, carries it the same way.

A store repo that was deleted and made again at the same address (empty, or with another store's history) is not filled with the old store on its own. The write that finds it, and every `lore sync`, says the store was reset and keeps the commits on this machine (`E_REFUSED`), and `lore store init` at that address refuses to call it "already a lore store". There are two ways out: `lore store push --store <store> --restore --yes` puts this machine's copy back into the remote; `lore store forget --url <url> --yes` drops the old copy (it asks first, since that copy holds commits the remote lacks) and `lore store init --name <name> --url <url>` then starts a fresh store there. A store whose branch someone rewound (a force-push of an older commit, for example to drop a wrong row or a leaked secret) is refused the same way on every machine whose clone still holds the dropped commits: the sync says `the store at … was rewritten by someone: its branch main moved back, and 1 commit(s) it had (0c7c0fd) are no longer on it` and pushes nothing. `lore store push --store <store> --restore --yes` puts the commits back; to take the rewrite, `lore store forget --url <url> --yes`, then `lore sync` clones the store as it is now (this machine's commits that are not on it are dropped). A store compacted by lore (a higher epoch) is not a rewind: every clone adopts it as before. This holds for every kind of store (an org store too) and for every push lore makes (a sync, a write, an org link), and `lore store doctor` and `lore doctor` name the reset and the same two ways out. `lore store push --restore` checks the remote itself, so it works as the first command after a wipe. Once someone has put the store back, every other machine's next sync sees the remote shares its history again, clears the reset by itself and says `the store at … is back`. When someone started the store fresh instead (the remote now holds a store with another id), every other machine's sync and `--restore` say so and offer the one way that works there: `lore store forget --url <url> --yes`, then `lore sync` clones the new store. Starting fresh on an org's store (`lore store forget --url <url> --yes`, then `lore store init --name <name> --url <url>`) keeps it the org's store: nothing is written to the repo's `.lore/stores.yaml`, and init names the setup team step that writes the org's file and roster again, then `lore member add` for each teammate.

The `note:` line says the old address could not be read (the folder is gone), so the two ids were not compared. Do this once in every repo that attaches the store, then commit `.lore/stores.yaml`.

An org's store, `me` or a store named by its `sto_` id lives in `~/.lore/config.yaml`, not in a repo, so `lore store set-url --name <org> --url <new-url>` works from any folder, a repo or not. Run by an admin of the org (a person with an admin setup of it), it also records the new address in the org's `lore-org.yaml`, which the next `lore sync` pushes. Run by a member, it moves only that member's machine and says `lore-org.yaml of <org> left as it is: only an admin of <org> changes the store's address for the whole org`; the member asks an admin to run it. Until an admin does, `lore org refresh` on the member's machine keeps the address set-url recorded there (`kept <old-url> → <new-url>`) instead of copying the old one back from the file. Teammates whose lore still names the old address cannot read the org's file there, so tell them the new address yourself; each runs the same `lore store set-url` once. When the clone at the old address holds nothing the store lacks, set-url and `lore store doctor` name it as no longer used, with the `lore store forget --url <old-url> --yes` that removes it.

## Step 4 — Split a store into folders

`lore store folder add --store <store> --folder <folder>` creates a folder inside the store, one per topic or per client. A folder name is snake_case and cannot be `org`. Write to it with `--to <store>/<folder>`; the first write attaches the folder in `.lore/stores.yaml` as its own entry, so a repo only shows the folders it uses. `lore store folder list [--store store]` lists a store's folders, who made each and whether this repo attaches it (`team`, `me` and an org's store work outside any repo too: [Client folders from the parent folder](../team/client-folders.md)):

<!-- example: stores#store-folders -->
```console
# alice, in website
$ lore store folder add --store notes --folder golang
✓ created folder golang in notes (prj_2ad3…f891; shown only in repos that attach it)
$ lore memory add --to notes/golang --body 'Close every channel from the sender side.'
lore: wrote ~/stores/agency-notes/golang/actors/act_162d…9666.json (store notes/golang)
lore: wrote ~/stores/agency-notes/golang/memories/mem_54fe…28f2.json (store notes/golang)
lore: wrote ~/stores/agency-notes/golang/projects/prj_2ad3…f891.json (store notes/golang)
✓ mem_54fe…28f2 [notes/golang]
  scope: [project]
$ cat .lore/stores.yaml
stores:
    - name: notes
      url: file://~/stores/agency-notes
    - name: notes/golang
      url: file://~/stores/agency-notes
      folder: golang
$ lore memory list
mem_54fe…28f2 [retrieved] Close every channel from the sender side. [notes/golang]
$ lore store folder list --store notes
store notes folders:
  FOLDER  ID                                    CREATED BY  CREATED  ATTACHED HERE  ROWS
  golang  prj_2ad3…f891  -           -        yes            3
```
<!-- /example -->

## Step 5 — Move a note out of the repo

Alice wrote a rule into the repo that belongs in her store. `lore row move --id <id> --to <place>` moves any row and keeps its id; `<place>` is `repo`, `me` (your personal store), `<store>` or `<store>/<folder>`. It asks first, because the row becomes visible to everyone who can open the target. The old copy in `.lore/data` is not deleted: it becomes a *tombstone*, a small file with `"_deleted": true`, so a teammate who pulls also drops the row instead of bringing it back. Commit the tombstone with your next change:

<!-- example: stores#move-a-row -->
```console
# alice, in website
$ lore rule add --body 'Keep the agency hourly rate out of the client repo.'
lore: wrote ~/work/website/.lore/data/rules/rul_05e9…4603.json
✓ rul_05e9…4603
  scope: [project] severity: must
$ lore row move --id rul_05e9…4603 --to notes
ERROR: [E_NEEDS_CONFIRM] This becomes visible to everyone with access to notes (internal):
  "Keep the agency hourly rate out of the c…" (rul_05e9…4603, id kept), now in repo
Move it?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore row move --id rul_05e9…4603 --to notes --yes
lore: wrote 3 row file(s) in ~/stores/agency-notes/_store (store notes)
lore: wrote 1 row file(s) in ~/work/website/.lore/data
✓ rul_05e9…4603 moved to store notes (same id; the old copy is tombstoned)
$ lore rule list
rul_05e9…4603 [must/always] Keep the agency hourly rate out of the client repo. [notes]
$ cat .lore/data/rules/rul_05e9…4603.json
{
  "_deleted": true,
  "_hlc": "2026-11-02T10:01:40.000Z/0002/act_162d…9666",
  "_table": "rules",
  "_v": 2,
  "id": "rul_05e9…4603",
  "updated_at": "2026-11-02T10:00:00.000000001Z",
  "updated_by": "act_162d…9666"
}
```
<!-- /example -->

For a task, `--with-children` moves its whole subtree; moving into a client repo refuses rows that carry internal metadata unless you pass `--drop-internal`.

## Step 6 — Detach a store

`lore store remove --name <name>` takes the store out of this repo's `.lore/stores.yaml`. It asks first. `lore store remove --name <store>/<folder>` detaches one folder entry (the name `--to` and `lore store list` use); removing the store detaches its folder entries too. The store and its notes are untouched; the repo simply stops seeing them. To delete this machine's copy of a git store as well, use `lore store forget`:

<!-- example: stores#detach-a-store -->
```console
# alice, in website
$ lore store remove --name notes/golang --yes
✓ detached folder entry notes/golang from .lore/stores.yaml; its rows stay in the store (`lore store forget` drops this machine's copy); commit the file to share
$ lore store remove --name notes
ERROR: [E_NEEDS_CONFIRM] store remove notes: detach store notes (file://~/stores/agency-notes) from this repo? its rows stay in the store
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore store remove --name notes --yes
✓ detached store notes from .lore/stores.yaml; its rows stay in the store (`lore store forget` drops this machine's copy); commit the file to share
$ lore store list
count: 0
$ lore rule list
(no rules)
$ ls ~/stores/agency-notes
README.md
_store
golang
lore-store.yaml
```
<!-- /example -->

After the detach, `lore rule list` no longer shows the store's rule, while the store folder still holds it (and the `golang` folder).

## Try it — try to move the note back

Run this right after Step 5, while the store is still attached. A moved row cannot go back to a place it left: the tombstone it left there stays the last word, so lore refuses `--to repo` for the rule from Step 5 (`E_UNSUPPORTED`, exit 23) and the rule stays in the store. If the note really belongs in the repo again, add it as a new row there:

<!-- example: stores#no-way-back -->
```console
# alice, in website
$ lore row move --id rul_05e9…4603 --to repo --yes
ERROR: [E_UNSUPPORTED] rul_05e9…4603 was in store repo before and left it; a row never returns to a place it left
  hint: keep it in notes
(exit 23)
$ lore rule list
rul_05e9…4603 [must/always] Keep the agency hourly rate out of the client repo. [notes]
```
<!-- /example -->

## Sync every store from anywhere

`lore sync` in a code repo syncs that repo's stores, then pushes the waiting commits of any org store your `~/.lore/config.yaml` lists that the repo does not attach (a note written there with `--to <store>/<folder>`), one line each. Run it where there is no code repo (your home folder, the plain parent folder of your client repos) and it syncs every store the setup of that folder sees, the same set `lore store list` shows there: one line per store (`up to date`, `pulled`, `pushed`, `pulled and pushed`, with the counts), and the push lines of each store on stderr. `lore sync --json` lists them under `data.stores` (each with `store`, `url`, `status`, and for a store that failed `error` and `next`). Inside a store checkout (`cd ~/.lore/stores/<owner>/<repo>/<folder>`) it syncs that store only. `lore sync status` there shows the same stores: each one's commits ahead and behind of its remote as of the last fetch, its last push and push mode, and any refused push (no network is used). A store whose address now answers that there is no repository (it was moved, renamed or deleted, or your access was removed) is not called offline, in a code repo or outside one: its line says so with git's reason, the command exits non-zero (`E_STORE_UNREACHABLE`), and it names the way out for each case: `lore store set-url --name <store> --url <new-url>` for a move, an empty repository at the old address and then `lore store push --store <store> --restore --yes` from the clone with the most work for a deletion, and an admin of the store for lost access. `lore doctor`, `lore doctor --all` and `lore org refresh` name the same steps, never an ssh alias, `lore store init` or "retry".

A store that cannot be reached or fails does not stop the others: its line says why, and when any store failed `lore sync` ends with an error naming each failed store and its next step (exit non-zero, `E_STORE_UNREACHABLE` for a store it could not reach).

```
$ cd ~/code && lore sync
store agency: up to date
store notes-two: pulled (sent 0, received 1, merged 0)
store me: up to date
```

## Pitfalls

- **Every confirm needs `--yes` without a terminal.** `store visibility`, `store set-url` onto a store whose old address still opens, `store remove`, `row move`, and `store init`, `add` or `set-url` when the url would land in `.lore/stores.yaml` of a client repo or is one of your org's stores (committed, so visible to everyone who can read the repo) stop with `E_NEEDS_CONFIRM` (exit 26) in a script or a coding agent's shell. Ask the person, then rerun with `--yes`.
- **`file://~/…` is not expanded.** Write `file://$HOME/…`.
- **A move is one way.** A row never returns to a place it left (`E_UNSUPPORTED`); pick the target with care, and write a new row if you change your mind.
- **A moved row leaves a tombstone in the repo.** Commit it with your next change, so teammates who pull drop the row from the repo too.
- **Two entries on one place.** Two `.lore/stores.yaml` entries can name the same place, for example `stag` as `<url>?branch=staging` and `stag2` as `<url> --branch staging`. Lists and search show its rows once under the first entry's name (`[stag]`), even a row written `--to stag2`; `--store stag2` still answers under that name. `lore sql` keeps both names as sources, so a query over both sees each row twice, once with `source` `stag` and once with `stag2`; filter on one of them (`WHERE source = 'stag'`) to count each row once.
- **An ssh alias of yours is not a store url for the team.** `.lore/stores.yaml` is committed, so it holds the canonical url (`git@github.com:agency/notes.git`). Type your alias url (`git@work-gh:agency/notes.git`, where `~/.ssh/config` says `HostName github.com`) and `lore store init` or `lore store add` stores the canonical url, records your spelling under `access:` in `~/.lore/config.yaml`, scoped to the owner (`git@github.com:agency/` → `git@work-gh:agency/`, so your stores under another account are not sent through the alias), and says so ([Reaching stores through your own ssh alias](../reference/config.md#reaching-stores-through-your-own-ssh-alias-access)).
- **`shareable` is about committed files.** It does not copy the store anywhere; it only allows its notes into renders and client repos. Check with `lore render --dry-run` before you commit the render.

## Related

- [One store for several repos](shared-database-mode-b.md): `lore store init` and `lore store add` across several repos, and converting an old shared database.
- [How notes travel with git](how-notes-travel-with-git.md): the repo's own notes in `.lore/data`.
- [Trash, restore from git and purge](trash-and-purge.md): what happens to deleted rows.
- [Configuration › lore-store.yaml](../reference/config.md#lore-storeyaml): every key of the marker.
- [How writes reach the remote](../team/identity-and-stores.md#how-writes-reach-the-remote): pushing a store in batches or only on `lore sync`, and folding its old history with `lore store compact`.
- [Lore for teams](../team/README.md): git stores shared by an agency and its clients.
- [Feature index](../reference/features.md)
