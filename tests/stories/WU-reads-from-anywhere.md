# WU — reads from anywhere

**Who.** Ada (made-up names) is the admin of the org `acme`, whose own repos match `github.com/acme-co/*`. She works from the home folder (under no setup's folders) and from `~/code/web`, an own repo of the org that is not linked to a store folder and has no `.lore/`.

**Why.** An owner use pass found that reads did not go where writes went: `lore memory edit --id <id>` worked in the unlinked own repo while `lore memory show --id <id>` refused "lore isn't set up", `lore resource list --store acme` refused without `--kind`, a read naming the wrong folder answered with a branch hint that has no meaning outside a repo, `lore store list` in the home folder said count 1 with no word about the setups, and `lore context` took no `--store`.

**Test.** `TestStoryWUReadsFromAnywhere` (tests/stories/WU_upread_test.go), about 25 s.

## Steps

1. Ada starts the team in `~/code/platform` with the setup `acme-admin`, adds the `own:` pattern, clones `web` unlinked and writes a memory to `acme/sync` from the home folder.
2. From the home folder and in `web`: `lore resource list --store acme` with no `--kind`; with `--where` and no `--kind` it is refused naming `--kind` and `lore resource kinds`.
3. In `web`: `lore memory show --id <id>`, `lore memory edit --id <id> …`, then show again; `lore trash list` there.
4. From the home folder: `lore memory show --id <id> --store acme/org` (a folder that does not hold the row), then `lore memory show --id <id>` with no `--store`.
5. From the home folder: `lore store list`, then `lore store list --setup acme-admin`.
6. `lore context --store acme --for-prompt deploy` and `--store acme/sync` from the home folder, and `--store acme` in `web`.
7. (`TestStoryWUFolderScope`) With folders `org`, `sync` and `tools`, a `code_repo` kind in `acme/org` and resources in `acme/org` (`project = sync`, `project = tools`, none) and in `acme/sync`: `lore resource list|kinds|kind show|show --store acme/sync` from the home folder and in `platform`, then `lore resource list` inside the `sync` folder of the store clone.
8. `lore resource list --store acme`, with and without `--kind code_repo` (one resource has a long `purpose`), before and after a `lore resource check`.
9. (D-623) In `web`, with a resource of `acme/sync` naming its remote: `lore trash list --json`, `lore rule list --json`, `lore resource list --json`; then `lore rule list --store acme/sync --json` from the home folder.
11. (`TestStoryWUStoreTaskNextFromHome`, D-666) From the home folder: `lore task next --setup acme-admin`, `--store acme`, `--store acme/web`; `--claim` without and with `--push`; `lore task heartbeat` with no `--id` from home, at the store clone's top and in `web`; `lore task heartbeat --id <id>` and `lore task release --id <id>` from home; `web` shows the claim after each step.
10. `lore org config set --org acme --key own --add 'github.com/acme-co/*'` and two exact `repos` entries under that owner; then `lore org config get --org acme` and `lore store doctor`.

11. (`TestStoryWUBudgetWarn`, D-636, D-637) Ada links `web` to `acme/web` with `--own`, adds a long must rule to `acme/org`, sets `context.budget.internal` to 1000 in `acme/web`; Mo joins as a member. Each `lore sync` WARN line's fixes are run exactly as printed in `web`; Mo then tries to demote, archive and delete the company rule and to set a policy in `acme/org`.
12. (`TestStoryWUClaimPushOnlyWhenAsked`, D-639) A `task release` after the refused claim, and `task release --help`.

## What must hold

- A read by id finds the row in whichever org folder holds it (ids are unique), from the home folder or a repo lore is not set up in, and the text output names that folder after the id (`mem_… [acme/sync]`).
- A read that still needs a project in an own repo of the org names `lore org link --org acme --project <folder> --own` and `--store acme`.
- A read naming a folder that does not hold the row says `it is in acme/sync` and the read that finds it, never "landed on main" or `lore sync peek`.
- `lore store list` outside every setup names `lore store list --setup <setup>` for each setup that brings org stores.
- `lore context --store <org>[/<folder>]` answers from any folder.
- A read a resource routed to `acme/sync` names it in a top-level `"routed": "acme/sync"` field of its `--json`, an empty answer too; a read naming its store carries no such field.
- A folder read (`--store acme/sync`, or inside that folder of the store clone) sees the org folder's kinds and lists the folder's resources (stored there, or in `acme/org` with `project = sync`), with a line saying how many org-wide or other folders' resources it left out and naming `lore resource list --store acme`; the same from every folder.
- `lore resource list` with no `--kind` prints `KIND NAME PROJECT URL`, `CHECKED` only once a row was checked; no cell is wider than 60 characters.
- `lore org config get` and `lore store doctor` name an own pattern that overlaps repos entries, how many, and that the repos entries win (client).
- In a repo linked to a store folder, `lore task claim` without `--push` is refused and the store remote is unchanged; with `--push` the claim reaches the store's signals ref signed by the org identity; `lore task release` takes it off and says it pushed (TestStoryWUClaimPushOnlyWhenAsked).
- From the home folder `lore task next --setup <setup>` (or `--store <org>[/<folder>]`) ranks the org store's tasks, `--claim` is refused without `--push` and writes nothing, with `--push` the claim lands on the store's signals ref under the task's folder and `web` sees it; `lore task heartbeat` with no `--id` names that claim from home, in the store clone and in a code repo; every printed next step (it carries the same `--setup` or `--store`) works where printed.

- The budget warning's raise names `--to acme/web` in the linked repo and works there; a company row's fix is a command for an admin only, and a member is told `ask an admin of acme (<names>) to …`; a member's demotion of a company must rule or policy write to `acme/org` is refused with E_REFUSED and nothing changes; a rule of `acme/web` stays the member's to change.
- `task release` with no pushed claim leaves the store remote unchanged, and its help says it needs no `--push` and why.

## Findings it guards

- `devco:D-550`..`devco:D-555`, `devco:D-583`..`devco:D-585` (REGRESSIONS.md).
- `devco:D-580`..`devco:D-582` (REGRESSIONS.md).
- `devco:D-623` (REGRESSIONS.md).
- `devco:D-636`, `devco:D-637`, `devco:D-639` (REGRESSIONS.md).
- `devco:D-666` (REGRESSIONS.md).
