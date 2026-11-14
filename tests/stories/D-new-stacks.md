# Story D — new stacks, monorepos, worktrees, shallow and sparse checkouts

Dev-company round 2, track D. Replayed by `TestStoryDNewStacks` in `D_new_stacks_test.go` (build tag `stories`).

## Who

- A CTO (org admin), a lead and three developers, each on their own laptop (own HOME, made-up git identity).
- Three coding-agent sessions working beside the developers in the same clone (`LORE_ACTOR=agent:<label>`).
- Three code repos, generated, never copied: a TypeScript pnpm-style monorepo (`apps/web`, `packages/ui`, `packages/core`, a root `AGENTS.md` and `.cursor/rules/`, about 300 commits of history, no lore data), a PHP app (`composer.json`, a `CLAUDE.md` marked GENERATED do-not-edit, no lore data) and a small Go tool (no lore data, nobody ever runs `lore init` in it).

## Steps

1. The CTO runs `lore init` at the monorepo root. lore makes one project, writes `.lore/` at the root and a pointer next to the existing `AGENTS.md` and `.cursor/` files without clobbering or duplicating them.
2. A developer runs `lore init` in `packages/core`. lore makes a sub-project named `<repo>-core`, never a second project with the root's name, and says the repo root already holds a project.
3. The package folder's `AGENTS.md` pointer names the package's own rendered file, not the root's `.lore/LORE.md`.
4. In the PHP app the CTO runs `lore init`. The GENERATED `CLAUDE.md` is left alone with a note saying where the pointer went. `lore init --target cursor` on the now initialized repo names `lore render --add-target cursor`.
5. When `CLAUDE.md` is a symlink to `AGENTS.md`, `lore render` writes the file once and keeps the `@AGENTS.md` import.
6. A developer makes a `git worktree add` of main and a detached worktree at a commit from before lore was adopted. In the worktree lore reads and writes; a commit there names the real config file (not `<worktree>/.git/config`). At the pre-lore commit lore names the branch that carries the project instead of hinting `lore init`.
7. CI makes `git clone --depth 1` of the monorepo: reads work, `lore render --check` passes without this machine's private renders, nothing is written into the tree.
8. A sparse cone checkout that leaves `.lore` out: lore says the project is outside the sparse cone, and a package folder in the cone does not list the root's internal tasks as its own.
9. Two weeks of daily work: three developers and three agent sessions add tasks in different packages, plans and decisions; search finds a word across packages; agent-added (proposed) tasks are hidden from `lore task list` with an `N hidden` line naming the flag that shows them.
10. Commits use conventional-commit messages (`feat(core): …`); lore's hooks add well under a second to a code-only commit. A squash merge of a branch that claimed a task keeps the `Lore-Task:` trailer.
11. Two developers add rules on different branches and pull: `.lore/LORE.full.md` merges with lore's merge rule, no git conflict, doctor reports zero open conflicts.
12. A developer hand-edits a row file with an unknown field. The commit hook refuses it and `lore show` never prints the refused value.
13. A developer runs `lore setup --agent-hooks --dry-run` in the Go tool nobody initialised, and the person's global `core.hooksPath` holds a hook manager's hooks. Every command anyone runs inside the Go tool (reads, doctor, `render --check`, a plain `git commit`) leaves it byte-identical.

## What must hold

- Standing rule 1: no remote moves unless a person ran `git push`, `lore sync`, `lore store push` or a claim with `--push`. Checked by comparing every bare remote's refs before and after each step.
- Standing rule 2: the Go tool nobody ran `lore init` in stays byte-identical (tree hash, `git status --porcelain --ignored`, `.git/config`, `.git/hooks`).
- Standing rule 4: every hint lore prints names only flags the command takes.
- Standing rule 5: no markdown file lore writes is hard-wrapped.
- Standing rule 6: every refusal names the cause and the next step.
- Standing rule 7: `.lore/` lands at the repo root (or the package folder for a sub-project), nowhere else.
- No fake secret value is ever printed.

## Findings this story guards

| Finding | Round-2 id | What | Guarded by |
|---|---|---|---|
| D-57 | r2 D-1 | package init makes a second same-named project | `TestStoryDNewStacks` step 2; `TestJourneyCheckoutsOfALoreRepo` |
| D-58 | r2 D-2 | pointer written into a GENERATED `CLAUDE.md` | `TestStoryDNewStacks` step 4 |
| D-59 | r2 D-3 | `init --target` never names `render --add-target` | `TestStoryDNewStacks` step 4 |
| D-60 | r2 D-4 | whoami reads the agent version from `package.json` | `TestStoryDNewStacks` step 9 |
| D-61 | r2 D-5 | worktree hook names `<worktree>/.git/config` | `TestStoryDNewStacks` step 6 |
| D-62 | r2 D-8 | detached HEAD before lore hints `lore init` | `TestStoryDNewStacks` step 6; `TestJourneyCheckoutsOfALoreRepo` |
| D-73 | r2 D-9 | `render --check` in a fresh clone needs private renders | `TestStoryDNewStacks` step 7; `TestJourneyCIShallowReadOnlyHome` |
| D-74 | r2 D-10 | sparse checkout hides `.lore`, package lists root tasks | `TestStoryDNewStacks` step 8 |
| D-75 | r2 D-11 | proposed tasks hidden with no `N hidden` line | `TestStoryDNewStacks` step 9 |
| D-82 | r2 D-14 | claim adds no `Lore-Task:` trailer | `TestStoryDNewStacks` step 10 |
| D-83 | r2 D-15 | claim pushes signals without asking | `TestStoryDNewStacks` rule 1 check; `TestJourneyNoPushWithoutAsking` |
| D-94 | r2 D-16 | hooks cost ~1.7 s per code-only commit | `TestStoryDNewStacks` step 10 |
| D-97 | r2 D-18 | refused hand edit committed anyway | `TestStoryDNewStacks` step 12; `TestJourneyCheckoutsOfALoreRepo` |
| D-100 | r2 D-19 | `LORE.full.md` has no merge rule | `TestStoryDNewStacks` step 11; `TestJourneyRenderedFiles` |
| D-101 | r2 D-20 | `setup --agent-hooks --dry-run` creates `.lore/` | `TestStoryDNewStacks` step 13 |
| D-102 | r2 D-21 | global `core.hooksPath` gets lore's hooks | `TestStoryDNewStacks` step 13; `TestJourneyNoLoreRepoGlobalHooksPath` |
| D-103 | r2 D-22 | package pointer names the root's `LORE.md` | `TestStoryDNewStacks` step 3; `TestJourneyRenderedFiles` |
| D-104 | r2 D-23 | `CLAUDE.md` symlink rewritten twice, import lost | `TestStoryDNewStacks` step 5; `TestJourneyRenderedFiles` |

The client-repo findings of this track (r2 D-6, D-7, D-12, D-13, D-17: D-63, D-68, D-77, D-78, D-96) are replayed by story F, which owns the client repo.
