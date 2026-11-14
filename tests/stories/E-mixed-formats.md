# Story E — mixed formats and a migration in the middle of team work

Dev-company round 2, track E. Replayed by `TestStoryEMixedFormats` in `E_mixed_formats_test.go` (build tag `stories`).

## Who

- A CTO, a lead and two developers on the current lore.
- One teammate still on lore 0.1.17 for the first week (the harness's old-lore binary), who upgrades later.
- Three repos, generated: two in the 0.1.17 format 1 (a Go repo of about 300 rows with tags, links and one orphan tag row pointing at a deleted note; a TypeScript monorepo of about 200 rows) and one already in format 2 (about 60 rows).

## Steps

1. Week 1. The 0.1.17 teammate and the others work side by side in the two format-1 repos: tasks, memories, rules. Nothing anyone writes breaks the others' reads; both repos stay format 1 (`.lore/data/_meta.json` `_v` is 1).
2. The 0.1.17 teammate clones the format-2 repo. Old lore reads it (as the docs promise) or says in one clear line that a newer lore is needed; its hints never loop between `lore init` and "already initialized".
3. A developer has an unpushed feature branch with new and edited notes (format 1). The CTO runs `lore migrate run --dry-run`, then `lore migrate run` on main, commits and pushes. The run's summary matches the dry run's numbers, the orphan tag row is skipped with a note instead of aborting, and `lore doctor` is HEALTHY right after (the migrator's actor row exists).
4. The developer rebases the branch over the migration, a second developer merges it, a third cherry-picks one note commit across it. Every pre-migration edit survives (none is dropped because the migration touched `updated_at`), row counts before and after match, nothing is duplicated.
5. Where the merge conflicts, `lore doctor` names the conflicted files and the lore command that resolves them.
6. The migration's own actor trim is not reported as a hand edit by the next write, and `lore sync` that says "already in sync" changes no committed file.
7. Someone reverts the migration commit: the repo is format 1 again with no format-2 rows left behind, or doctor names the revert and the next step; never a silent HEALTHY over mixed rows.
8. Two people run `lore migrate run` on two branches and merge both: one migrated repo, the row count unchanged.
9. The 0.1.17 teammate, still on old lore, commits code in the migrated repo: one clear line says the repo needs the new lore; no 330-line dump, no credential hint.
10. Stores: the 0.1.17 teammate makes a personal store and a team store with old lore; the CTO opens them with the new lore. The layout move is committed locally and reaches the team remote only on `lore sync`; the old teammate keeps working against the same stores for a day.
11. `lore memory list --store <org>` lists the org store's notes; `lore fix` converges (a second run fixes nothing); `lore fix --all` never lists the home folder as a repo.
12. Timings on the bigger repo: `lore doctor` and `lore fix --all` stay inside the gate's budget.

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
| D-56 | r2 E-6 | `fix --migrate` says render skipped but writes | `TestStoryEMixedFormats` step 3 |
| D-69 | r2 E-8 | conflicts across the migration have no next step | `TestStoryEMixedFormats` step 5 |
| D-70 | r2 E-9 | migration's actor trim seen as a hand edit | `TestStoryEMixedFormats` step 6 |
| D-71 | r2 E-10 | "already in sync" sync writes committed files | `TestStoryEMixedFormats` step 6; `TestJourneyPullNeverBlocked` |
| D-72 | r2 E-11 | 0.1.17 commit in a migrated repo: 330 lines | `TestStoryEMixedFormats` step 9 |
| D-84 | r2 E-13 | upgraded 0.1.17 machine's first read pushes | `TestStoryEMixedFormats` step 10; `TestJourneyNoPushWithoutAsking` |
| D-88 | r2 E-14 | `memory list --store <org>` prints none | `TestStoryEMixedFormats` step 11 |
| D-89 | r2 E-12 | doctor's 0.1.17 `everywhere/` advice wrong | `TestStoryEMixedFormats` step 10 |
| D-90 | r2 E-15 | `lore fix` never converges on actor_roster | `TestStoryEMixedFormats` step 11 |
| D-91 | r2 E-16 | `fix --all` lists the home folder | `TestStoryEMixedFormats` step 11 |
| D-154 | perf3 | pre-commit refuses every migration commit (signpost file) | `TestStoryEMixedFormats` step 3 (skipped until D-154 lands) |
