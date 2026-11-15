# Story BL — notes that follow the code branch (branch layers)

Lore 0.1.19 work (design: branch layers spec). Replayed by `TestStoryBL1SharedLayer` … `TestStoryBL9TwoLandings` in `BL_branch_layers_test.go` and `TestStoryBL10CodeRepoMode` … `TestStoryBL15InstallAction` in `BL_more_test.go` (build tag `stories`); the nine parts run in parallel, about 35 seconds together.

## Who

- An org admin who links a client's code repo to a folder of a client store, and works there on code branches.
- A developer who joins and works on the same branch; a third person who stays on `main`.
- CI: a runner with a fresh checkout of the code repo and no lore config at all.
- Repos, generated: the client's code repo with no lore data, the admin's own repo, a team store and a client store (bare remotes on disk). Every name, email and url is made up.

## Steps

1. **BL-1** The admin switches the client repo to `feat/x` and adds a task and a memory: lore says they went to the `feat/x` notes of the folder, `lore branch mode` says isolated (step 4), and nothing reaches the store remote. `lore sync` pushes the branch's notes as a hidden ref (no new branch on the store). On `main` the admin sees neither row; `lore branch list` names the branch with its counts. The developer joins, checks out `feat/x`, syncs, sees both rows, finishes the task and adds a rule. The third person on `main` sees none of it after a sync, only the branch in `lore branch list`. The admin back on `feat/x` gets the developer's edits after a sync. The client repo stays byte-identical.
2. **BL-2** A branch with notes is merged into `main` with a merge commit. `lore branch list` says merged and names the land command. `lore branch land --branch … --pr 12` makes one commit on the store's `main` (a merge commit whose second parent is the layer tip; one commit on the first-parent line, the branch, repo and PR in its subject), removes the hidden ref from the remote and keeps a trash copy there; landing again says already landed. The third person sees the landed rows after a sync; `lore branch restore` brings the notes back.
3. **BL-3** Two branches are squash-merged and deleted (a squash merge cannot be told from a delete: both read orphaned). The CI runner, with only a checkout and no config, lands one with `--store --folder --repo --branch --pr`; a branch with no notes lands nothing and exits 0. The admin lands the other by hand; a later sync of the admin's clone does not put the CI-landed notes back on the remote.
4. **BL-4** A branch with a task is deleted without merging: orphaned. Reads and a sync two days later keep it; a sync on day 16 moves it to the trash and says how to restore it. Restore, drop (with `--dry-run` first), and purge (refused without `--confirm`) all work; purge leaves no trash ref on the remote.
5. **BL-5** On a branch with notes: commit, `reset --hard`, switching to `main` (the notes are not there), rebasing on `main`, merging `main` in: no note is lost. `git branch -m` gives the new name no notes until `lore branch rename --from --to`, which moves them, on the remote too.
6. **BL-6** A task's title edited on the branch and its status changed on `main` merge field by field when the branch lands. A memory whose text both sides changed stops the landing with `E_LAND_CONFLICT` naming the row; nothing on the remote changes and the branch's notes stay open.
7. **BL-7** Two worktrees (two agents in parallel) keep two separate sets of notes, and `main` sees neither. `lore branch promote --ids` shares one rule at once. On a detached HEAD a write is refused with `E_DETACHED` and the hint `--no-layer`, which works. An open set of notes of an older branch with the same name refuses a write with `E_LAYER_STALE` until it is dropped.
8. **BL-8** Standing rules: a branch write pushes nothing; the client repo stays byte-identical; a fake secret is refused and never printed; named flags only; after the first branch notes the folder's `_meta.json` on the store's `main` declares `branch_layers` and lore 0.1.19, so lore 0.1.18 refuses to write there (`E_OLD_LORE`); `LORE_LAYER=off` writes to the shared notes.
9. **BL-9** Two people land the same branch: one landing commit, the second says already landed and exits 0.

10. **BL-10** Code-repo mode (owner decision: everything isolates, even the task): a person in no org runs `lore init` in their own repo. On `main` a task is a live row as before; on `feat/local` a new task is a file of the branch (not on `refs/lore/live`), `--live` and `--no-layer` still share at once; `main` does not see the branch task until the branch merges, then it does. A task on `dev` (main-like) is live, and so is one on `integration` once the repo's own policy shares it (`lore policy set branches.shared integration`).
11. **BL-11** `--kinds memories` lands the memory and keeps the task open on the branch; `--review` with no terminal is refused (`E_NEEDS_CONFIRM`) and with answers lands the kept row and drops the other; a same-text clash refused with `E_LAND_CONFLICT` lands with `--take branch --ids <id>`, keeping the branch's text.
12. **BL-12** `feat/b` made from `feat/a` reads `feat/a`'s notes under its own (its `show` lists only its own); `feat/a` does not see `feat/b`'s. `land --branch feat/b --into feat/a` moves them into `feat/a`'s notes. When `feat/a` lands, `feat/c` (also stacked) moves onto the shared notes and lands alone.
13. **BL-13** Notes landed too early come back with `restore --revert`, which adds a revert commit on top of the landing (no history rewrite) and pushes it; after a drop, `--revert` says the notes were never landed.
14. **BL-14** A memory written on `feat/old` into the shared notes (before layers) is moved into the branch's notes with `lore branch adopt` (`--dry-run` first); `main` no longer shows it; landing the branch brings it back.
15. **BL-15** In a client repo the CI workflow is never offered and `install-action --yes` is refused (`E_LEAK_BLOCKED`); in an own repo with branch notes `lore branch list` offers it, `--dry-run` prints it, without `--yes` it is refused (`E_NEEDS_CONFIRM`) and nothing is written, with `--yes` it is written and no longer offered.

## What must hold

- Notes written on an isolated branch are never in the store's `main` (or anyone's view on another branch) until a land or a promote.
- Standing rule 1: nothing reaches a remote without `lore sync`, `lore branch land|drop|restore|purge|rename` or the CI step.
- Standing rule 2: a repo with no lore data stays byte-identical.
- Standing rules 4, 5, 6: no secret value printed; named flags only; an old lore is refused, never silently writing.

## Findings this story guards

| Finding | What | Guarded by |
|---|---|---|
| bl:F1 | a teammate's sync pushed back a layer the CI step had just landed | `TestStoryBL3SquashMerge`, `TestLayerPushRetiresTrashedLayer` |
| bl:F2 | a branch whose notes were written before its first commit read as merged | `TestStoryBL3SquashMerge`, `TestStoryBL4Orphaned` |
| bl:F3 | `lore sync` in a linked repo syncs whole store clones, so the layer was never pushed | `TestStoryBL1SharedLayer` |
| bl:F4 | two moves to the trash in one second clashed on the trash ref name | `TestStoryBL4Orphaned`, `TestRefsRoundTrip` |
| bl:F5 | the CI form of land was refused in a checkout with no lore project | `TestStoryBL3SquashMerge` |
| bl:F6 | the layer wrapper hid the store clone's audit journal and purge recording: a purge in a linked folder was refused `E_UNSUPPORTED` | `TestJourneyAgencyFirstWeeks`, `TestJourneyTeamLeadUndo` |
| bl:F7 | `restore --revert` after a drop reverted an older landing instead of saying the restored copy was never landed | `TestStoryBL13RestoreRevert` |
| bl:F8 | code-repo mode did not read the repo's own `branches.*` policy rows, so a branch the repo shares was still isolated | `TestStoryBL10CodeRepoMode` |
| bl:F9 | rows a CI runner off the roster landed were held back by the roster authority replay (a squash hid each row's writer); a landing is now a merge commit with the layer tip as second parent | `TestStoryBL3SquashMerge` |
