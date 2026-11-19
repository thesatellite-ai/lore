# WUL — the names and ids people read (owner use pass 4)

**Who.** Ada, the admin of the org `acme` (made-up names), on one laptop with one setup `acme-admin` covering `~/code/**`, with two own repos, `web` and `api`, each linked to its own folder of the org store (`acme/web`, `acme/api`).

**Why.** The fourth use pass found: in a linked repo `lore sql … GROUP BY source` named the folder's rows `acme` while the store clone and the home folder said `acme/web` (D-685); store commit subjects mixed the id alone (`memory add mem_…`) with id and title (`task claim tsk_…: Question: …`), and a title can hold private words (D-685); `policy set` of one key in two linked folders gave both rows one `pol_` id and the result line said `(acme, pol_…)` (D-689); `--to me` with no personal store offered `--to repo` first, which a linked own repo refuses (D-690); doctor's Team versions printed the raw `act_` id of the home-folder agent (D-688).

**Test.** `TestStoryWULUp4Labels` (tests/stories/WUL_up4label_test.go).

## Steps

1. Day 1: Ada starts `acme` from `~/code/platform`, marks `github.com/acme-co/*` as the org's own, adds the folders `web` and `api` and links `~/code/web` and `~/code/api` to them with `--own`.
2. D-685: a memory written in `web`; `SELECT DISTINCT source FROM memories_all` gives `acme/web` in the linked repo and from the home folder (`--store acme/web --setup acme-admin`), never the bare `acme`.
3. D-685: a task titled `Question: renewal price for acme-co`, synced; no subject on the store remote carries the title.
4. D-689: `policy set --key trash.retention_days --to acme/web` says `(acme/web, pol_…)`; the same key set in `api` for `acme/api`; from the home folder the two rows have two ids and name both folders.
5. D-690: with `personal_store: none`, `memory add --to me` in `web` is refused offering `--to acme/web` and never `--to repo`; the offered target writes as printed.
6. D-688: `lore doctor` in `web` prints no Team versions line that starts with a bare `act_` id.
