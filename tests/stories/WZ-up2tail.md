# WZ — claim hints, signals labels, purge, client reads and names (owner use pass 2 follow-ups)

**Who.** Ada, the admin of the org `acme` (made-up names), on one laptop with a personal store: the home folder, the org's own repo `~/code/platform`, an own repo `~/code/web` linked to the folder `acme/sync`, and a client repo `~/code/shop` (the org's `repos:` pattern) no folder is linked to. `LORE_PUSH_CLAIMS=0`: only `--push` asks for a push.

**Why.** After the second use pass, workers found: claim hints without `--push` that lead straight to the claim refusal in a linked repo; a revoke there that said "pushed refs/lore/signals/main to origin" where a claim names "of store acme/sync"; `trash purge --confirm` in a linked repo or the home folder that wrote its tombstone and then failed `E_INTERNAL`; a search in an unlinked client repo that answered "(no matches)" from the personal store alone; `E_IN_TRASH` in the home folder that printed "deleted by act_…".

**Test.** `TestStoryWZUp2Tail` (tests/stories/WZ_up2tail_test.go).

## Steps

1. Day 1: Ada starts `acme` from `~/code/platform`, declares `github.com/acme-co/*` own and `github.com/client-co/*` client, links `web` to `acme/sync`, clones `shop` unlinked and writes a memory to `acme/sync` from the home folder.
2. D-610: `lore search --query deploy` in `shop` answers from the personal store and says first `read your personal store only, not acme's folders`, naming `lore org link --org acme --project <folder>` and `--store acme/<folder>`; `lore search --store acme/sync` there finds the memory.
3. D-607: in `web`, `lore task next` prints `take it: lore task next --claim --push (or lore task claim --id <id> --push)` and each printed claim runs there; after a revoke the holder's `lore task heartbeat` is `E_LOST_CLAIM` with `lore task next --claim --push` in its hint.
4. D-608: claim, release, revoke and a reassign that drops a claim in `web` all name `the signals ref refs/lore/signals/main of store acme/sync`, never `pushed refs/lore/signals… to origin`; `lore hold claim` and `lore hold release` name where the hold went.
5. D-611: a rule deleted in `acme/sync` answers `is in the trash (deleted by ada …)` from the home folder, `web` and `platform`; no answer prints a raw `act_` id.
6. D-609: `lore trash purge --id <id> --confirm` in `web` and in the home folder finishes with `purged 1 row` (no `E_INTERNAL`), and the purged row then reads `<id> was purged from acme/sync on <date>`.
