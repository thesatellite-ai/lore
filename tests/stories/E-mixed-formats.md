# Story E — mixed formats and a migration in the middle of team work

Dev-company round 2, track E. Replayed by `TestStoryEMixedFormats` in `E_mixed_formats_test.go` (build tag `stories`).

## Who

- A CTO, a lead and two developers on the current lore.
- One teammate still on lore 0.1.17 for the first week (the harness's old-lore binary), who upgrades later.
- Two repos, generated: a Go service in the 0.1.17 format 1 (about 180 rows, with one orphan tag link pointing at a deleted note) and a small Go library a new-lore user adopts in format 2.

## Steps

1. Week 1. The 0.1.17 teammate writes tasks and memories in the format-1 repo; the others on the new lore read and search them. A write by the new lore there is refused with one line naming `lore migrate run` (the format pin). Reads change no tracked file, and the repo stays format 1 (`.lore/data/_meta.json` `_v` is 1).
2. The 0.1.17 teammate clones the format-2 repo. Old lore says in one line that the files are from a newer lore format and to upgrade; its hint never sends the person to `lore init`.
3. The 0.1.17 teammate has an unpushed feature branch where they archived a rule and added a memory (format 1). The CTO runs `lore migrate run --dry-run`, then `lore migrate run` on main, commits and pushes. The run's summary matches the dry run's numbers, the orphan tag link is skipped with one line instead of aborting, nothing is pushed until the CTO pushes, and `lore doctor` has no actor-roster complaint right after (the migrator's actor row exists).
4. The 0.1.17 teammate, still on old lore, commits code in the migrated repo: no 330-line dump, no credential hint. Then they upgrade and rebase the branch over the migration: the archive made before the migration survives (the merge driver keeps no "newer" side over it), every row id present before is present after. Merge and cherry-pick across the migration are replayed by `TestJourneyMigrateKeepsBranchEdits`.
5. A branch from before the migration deletes a row the migration rewrote; merging it stops on a modify/delete conflict, and `lore doctor` sees the conflict (never "open conflicts: 0") with named-flag next steps.
6. The migration's own actor trim is not reported as a hand edit by the first write after the upgrade, and a teammate's `lore sync` on a fresh clone of the migrated repo changes no tracked file.
7. Someone reverts the migration commit: the repo is format 1 again with no format-2 rows left behind, or doctor names the revert and the next step; never a silent HEALTHY over mixed rows.
8. Two branches from before the migration migrate on their own, one with `lore migrate run` and one with `lore fix --migrate` (whose render line tells the truth about what it wrote), and are merged: one migrated repo, no row lost.
9. (Replayed in step 4: the 0.1.17 teammate's code commit in the migrated repo.)
10. Stores: the 0.1.17 teammate makes a personal store and a team store with old lore; the CTO opens them with the new lore. The layout move is committed locally and reaches the team remote only on `lore sync`; the old teammate keeps working against the same stores for a day. Not replayed here: `TestJourneyUpgraderOldLayout` and `TestJourneyUpgraderPersonalNotes` build those stores.
11. `lore fix` converges (a second run fixes no actor roster); `lore fix --all` from the home folder never lists it as a repo. (`lore memory list --store <org>` listing the org store's notes needs an org: guarded by the org-store tests of the people2 fix.)
12. Timings: `lore doctor` on the migrated repo is logged (the 5.5k-row timings are story C's).

## What must hold

- Standing rule 1: no remote moves without a person's `git push`, `lore sync` or `lore store push` (the upgraded teammate's first read pushes nothing to the team store).
- Standing rule 3: each format-1 repo stays format 1 until someone runs `lore migrate run` there.
- Row counts: every row present before the migration is present after it, once.
- Standing rules 4, 5, 6 as in every story; no fake secret value printed.

## Findings this story guards

| Finding | Round-2 id | What | Guarded by |
|---|---|---|---|
| D-50 | r2 E-4 | pre-migration edit dropped across a rebase | `TestStoryEMixedFormats` step 4; `TestJourneyMigrateKeepsBranchEdits` |
| D-51 | r2 E-5 | orphan tag row aborts the migration | `TestStoryEMixedFormats` step 3 |
| D-52 | r2 E-7 | reverted migration leaves v2 rows, doctor HEALTHY | `TestStoryEMixedFormats` step 7 |
| D-53 | r2 E-1 | 0.1.17 cannot read a format-2 repo | `TestStoryEMixedFormats` step 2 |
| D-54 | r2 E-2 | migrate summary contradicts the dry run | `TestStoryEMixedFormats` step 3; `TestJourneyMigrateCountMatchesDryRun` |
| D-55 | r2 E-3 | doctor DEGRADED after migrate | `TestStoryEMixedFormats` step 3; `TestJourneyMigrateCountMatchesDryRun` |
| D-56 | r2 E-6 | `fix --migrate` says render skipped but writes | `TestStoryEMixedFormats` step 8 |
| D-69 | r2 E-8 | conflicts across the migration have no next step | `TestStoryEMixedFormats` step 5 |
| D-70 | r2 E-9 | migration's actor trim seen as a hand edit | `TestStoryEMixedFormats` step 6 |
| D-71 | r2 E-10 | "already in sync" sync writes committed files | `TestStoryEMixedFormats` step 6; `TestJourneyPullNeverBlocked` |
| D-72 | r2 E-11 | 0.1.17 commit in a migrated repo: 330 lines | `TestStoryEMixedFormats` step 4 |
| D-84 | r2 E-13 | upgraded 0.1.17 machine's first read pushes | `TestJourneyUpgraderOldLayout`; `TestStoryEMixedFormats` rule 1 checks |
| D-88 | r2 E-14 | `memory list --store <org>` prints none | not replayed here (needs an org; the people2 fix's tests) |
| D-89 | r2 E-12 | doctor's 0.1.17 `everywhere/` advice wrong | `TestJourneyUpgraderPersonalNotes` (not replayed here) |
| D-90 | r2 E-15 | `lore fix` never converges on actor_roster | `TestStoryEMixedFormats` step 11 |
| D-91 | r2 E-16 | `fix --all` lists the home folder | `TestStoryEMixedFormats` step 11 |
| D-154 | perf3 | pre-commit refuses every migration commit (signpost file) | `TestStoryEMixedFormats` step 3 (the migration commit; the rest of the story skips with "waiting for D-154" if the hook refuses it) |
