# Story H — things going wrong: corruption, history rewrites, lost remotes, recovery

Dev-company round 3, track H. Replayed by `TestStoryHCorruption`, `TestStoryHRewrites` and `TestStoryHLostRemotes` in `H_recovery_test.go` (build tag `stories`).

## Who

- A lead and a developer on two laptops, with a notes store the repo attaches (`lore store init --name notes`) and the developer's personal store.
- Repos, generated: a library on the lore 0.1.17 format 1 (`SmallShape`, about 180 rows), a tool and an API the people adopt lore in (format 2), and a repo with no lore data that must stay byte-identical.

## Steps

1. Corruption, one damage at a time, each put right before the next: chmod a row file 000, leave merge-conflict markers in one, cut one short, copy one row over another (a duplicate id) in the format-2 tool; delete the whole `.lore` folder, delete `.lore/data/_meta.json` and save a row with a UTF-8 BOM in the format-1 library (empty files, wrong ids and the rest of the damages are walked by `TestJourneyDamagedLoreFiles`). Every command names the damaged file, the cause in plain words (conflict markers are called a conflict, an unreadable file is called unreadable) and the next step; `lore doctor` is never HEALTHY over the damage; `lore doctor --fix` restores `_meta.json` and strips the BOM; the commit hook refuses a commit that carries the damage; no other row is lost.
2. History rewrites by someone else: a force-push on the code remote that drops a commit carrying notes; a rebased main pushed with force; the store remote rewound by a force-push; the store emptied (deleted and made again). lore names what was dropped and how to get it back; commit links follow rebased shas; the next member `lore sync` does not silently re-push the commits the rewind dropped; an emptied store is refused clearly and `lore store push --restore` brings it back.
3. Lost remotes: a store remote is deleted; a personal store remote disappears while it has two unsynced notes and is made again empty. Sync and doctor name `lore store set-url` or `--restore` (not "offline, check the network"); `lore sync status` works outside a repo; `store set-url` is refused for a plain member with a correct reason; `store forget` refuses while unpushed work exists; nothing is lost.
4. Recovery: a deleted note restored from trash after a sync, in the repo and in the org store (also from a format-1 repo); a row removed with `git rm` and committed is found by `lore trash --from-history`, which prints the `git checkout <commit>^ -- <file>` that brings it back; `git commit -a` with a new row commits it once.
5. A new laptop after the old one died with an unsynced org note, an unpushed code commit with a memory and a note in a local-only personal store: on the new laptop lore says what could not be found; the join line says when a store is machine-only.
6. Hooks: lore's pre-commit replaced by hand with the person's own script, or taken over by husky (`core.hooksPath`). `lore doctor` says the leak guard is off; `lore fix` re-adds lore's blocks without clobbering the other hooks; a fake secret commit is refused after the fix; with husky, lore never adds files to the tracked `.husky/` folder without asking.

## What must hold

- Nothing silently lost: every row present before a damage or rewrite is present after the documented recovery, or lore named it as lost.
- Standing rule 1: every remote change is traced to a person's `git push`, `lore sync` or `lore store push --restore`.
- Standing rule 2: the untouched generated repos stay byte-identical.
- Standing rule 3: format-1 repos stay format 1 in every clone until `lore migrate run`.
- Standing rule 4: every printed hint uses named flags (`lore store init --name <name> --url <url>`).
- Standing rules 5, 6, 7; no fake secret value printed.

## Findings this story guards

| Finding | Round-3 id | What | Guarded by |
|---|---|---|---|
| D-111 | r3 H-1 | deleted `.lore` blamed on sparse checkout | `TestStoryHCorruption`; `TestJourneyDamagedLoreFiles` |
| D-112 | r3 H-2 | chmod 000 row: E_INTERNAL with no cause | `TestStoryHCorruption` |
| D-113 | r3 H-3 | deleted `_meta.json`: doctor HEALTHY | `TestStoryHCorruption`; `TestJourneyDamagedLoreFiles` |
| D-114 | r3 H-4 | conflict markers reported as invalid JSON | `TestStoryHCorruption` |
| D-115 | r3 H-5 | broken row: show names no file | `TestStoryHCorruption` |
| D-116 | r3 H-6 | pre-commit lets wrong/duplicate ids through | `TestStoryHCorruption`; `TestJourneyDamagedLoreFiles` |
| D-149 | r3 I-15 | UTF-8 BOM row refused everywhere | `TestStoryHCorruption`; `TestJourneyDamagedLoreFiles` |
| D-124 | r3 H-7 | force-push dropping notes: no recovery route | `TestStoryHRewrites` |
| D-125 | r3 H-8 | rebase + force-push leaves links on old shas | `TestStoryHRewrites` |
| D-126 | r3 H-9 | rewound store: sync re-pushes dropped commits | `TestStoryHRewrites` |
| D-127 | r3 H-13 | trash restore of an org row refused E_FORMAT_OLD | `TestRestoreOfStoreRowInOldFormatRepo` |
| D-128 | r3 H-14 | git rm'd row: 'purged by ?', no git restore line | `TestStoryHRewrites` |
| D-140 | r3 H-15 | `git commit -a` with a new row: 'nothing added' | `TestStoryHRewrites` |
| D-129 | r3 H-10 | `sync status` outside a repo fails | `TestStoryHLostRemotes` |
| D-130 | r3 H-11 | deleted/moved store reported as offline | `TestStoryHLostRemotes` |
| D-131 | r3 H-12 | `store set-url` wrong refusal, member rewrites url | `TestStoryHLostRemotes` |
| D-143 | r3 H-16 | store doctor: unsent commits as an error ×3 | `TestStoryHLostRemotes` |
| D-144 | r3 H-17 | new laptop: local personal store lost silently | `TestLocalPersonalStoreNote`, `TestJoinLinesSayLocalIsMachineOnly` (story H has no setup with a local personal store) |
| D-141 | r3 H-18 | hooks taken over: doctor silent about the leak guard | `TestStoryHLostRemotes` |
| D-142 | r3 H-19 | husky: lore adds hook files to tracked `.husky` | `TestStoryHLostRemotes` (new files; a changed tracked hook skipped until stories:H-1) |
