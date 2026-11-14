# J3 — a new user from zero

**Who.** Nia, alone, with no team and no org: one code repo on GitHub (`github.com/nia-dev/app`) and a personal store in a bare git repository on her own disk (`~/disk/nia-notes.git`, the way a NAS or USB drive is used). Every name is made up.

**Test.** `TestStoryJ3NewUser` (`j3_newuser_test.go`).

## Steps

1. Day 1: she clones the app, runs `lore init`, and commits what it wrote with plain `git commit`: lore's hooks pass the first commit in a fresh repo and leave nothing behind.
2. She makes her personal store with `lore store init me <path of the bare repo> --yes`; the clone lives at `~/.lore/stores/_local/nia-notes`.
3. She writes her first note with `lore memory add --body … --to me`: it is committed in the store clone and not pushed; `lore sync` sends it. No store commit subject carries the note's text.
4. Day 2: she deletes the note, finds it in `lore trash list`, restores it with `lore trash restore --target <id>`, and reads it back. She adds two rules and archives one: `lore rule list` shows the other, `lore rule list --archived` the archived one.
5. Day 3: she reads the help of the commands she uses (`store init`, `move`, `trash restore`, `memory add`, `task add`, `tui`, `init`): no internal spec or task ids, only named flags in examples, and `--to` names `me`.
6. Day 4: the disk with the store is locked (`chmod 000`). She keeps writing; `lore sync` keeps her notes locally, and `lore store doctor` says the folder cannot be read (permission denied) and that the address is right, in whole sentences. The disk comes back; `lore sync` sends the waiting note.
7. Day 5: she makes a scratch store and drops it with `lore store forget --url scratch --yes`; lore names it by the path she typed.

## What must hold

- Nothing pushes without push or sync: `lore init`, the first commit and every note leave the app's remote untouched; a note reaches the store only on `lore sync`.
- Named flags in every hint and help example lore printed; no spec ids in help.
- A note's text never appears in a commit subject (privacy: a purged note's words must not stay in `git log --oneline`).
- The shared journey invariants after every step (readable output, doctor, config, file modes).

## Already walked by the journeys (linked, not repeated)

- Local-path personal store, its clone folder and a second machine attaching it: `TestJourneySoloLocalPathStore` (tests/journeys/localstore_test.go).
- Lists a newcomer reads (tasklists, trash, rule scopes, help ids): `TestJourneyNewcomerLists` (tests/journeys/newcomer_lists_test.go).
- New laptop, render and doctor on a fresh clone: `TestJourneySoloNewLaptopRender` (tests/journeys/solo_newlaptop_render_test.go).
- Offline and a missing remote, calm messages: `TestJourneyOfflineCalm` (tests/journeys/offline_test.go).
- `git pull --rebase` with incoming notes: `TestJourneyPullRebase` (tests/journeys/pullrebase_test.go).
- A moved folder and repo, dir-scoped rules: `TestJourneyMovesFolderAndRepo` (tests/journeys/moves_test.go).
- A store renamed, remade empty, a new laptop's first address: tests/journeys/solo_storeurl_test.go.
- The whole first months of a solo user: tests/journeys/solo_test.go.

## Findings guarded

| Finding | What it was | Test |
|---|---|---|
| B3-3, B3-6, B3-24, B3-26 | spec ids in help text | `TestStoryJ3NewUser` |
| B3-12 | store doctor printed a fragment of git's stderr | `TestStoryJ3NewUser` |
| B3-17 | `--to` help omitted `me` | `TestStoryJ3NewUser` |
| B3-21 | local store cloned under its whole absolute path | `TestStoryJ3NewUser`, `TestJourneySoloLocalPathStore` |
| B3-22 | an unreadable store called a wrong address | `TestStoryJ3NewUser` |
| B3-23 | store commit subjects carried the note's text | `TestStoryJ3NewUser` |
| B3-30 | store forget named an internal key | `TestStoryJ3NewUser` |
| B3-31 | rule list --archived | `TestStoryJ3NewUser` |
| B3-5, B3-9 | moved folder orphans dir rules, cache prune | `TestJourneyMovesFolderAndRepo` |
| B3-7, B3-8, B3-10, B3-11, B3-18 | newcomer lists | `TestJourneyNewcomerLists` |
| B3-13, B3-14 | offline repeats, missing origin called offline | `TestJourneyOfflineCalm` |
| B3-15, B3-16, B3-29 | store set-url, new laptop address, remade store | tests/journeys/solo_storeurl_test.go |
| B3-20 | pull --rebase stopped by a re-render | `TestJourneyPullRebase` |
| B3-25 | fresh clone DEGRADED until render | `TestJourneySoloNewLaptopRender` |
