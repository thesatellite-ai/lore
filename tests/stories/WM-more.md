# WM — one org's reads and resource checks from the home folder

**Who.** Ada (made-up names) is the admin of the org `acme`, whose store has the folders `org`, `sync` and `tools`. The kind `code_repo` is defined in `acme/org`; `acme/org` holds `sync_go` (project `sync`, uses `shared_ci`), `tool_cli` (project `tools`), `shared_ci` and `ops_dash` (both org-wide; `ops_dash` uses `sync_go`), and `acme/sync` holds `sync_web` (uses `sync_go`); `uses` is a ref field of `code_repo`.

**Why.** After use pass 2's fixes the owner's workers still met two reads that answered from one folder: `trash list --store acme` from the home folder said "the trash is empty" while `acme/sync` held a trashed row (D-431 says `--store <org>` reads every folder), and `resource check --id <name>` from the home folder refused "this command works on the folder it runs in, so --store does not apply" although the resource is in a store; `resource stale`, `report` and `graph` with `--store acme/sync` listed every resource of `acme/org` too. Then (D-618) `graph` and `impact` through `acme/sync` still walked into org-wide resources and `impact --id` took a resource of another folder, and (D-621) graph and impact `--json` nodes did not say which store each came from, while every other routed read's rows did.

**Test.** `TestStoryWMMore` (tests/stories/WM_more_test.go), about 15 s; the graph walk unit is `TestGraphWalkStopsOutsideTheFolder`; the hint guard is `TestHomeHintNeverSaysStoreDoesNotApply`, the store-name rule `TestInNamedStore`.

## Steps

1. Ada starts the team in `~/code/platform` and adds the kind, its fields and the five resources with `--to`.
2. From the home folder: a memory of `acme/sync` added and deleted; `lore trash list --store acme/sync`, then `lore trash list --store acme`.
3. From the home folder: `lore resource stale --store acme/sync` and `--store acme`, `lore resource report --store acme/sync`, `lore resource graph --id sync_go --store acme/sync`, `--id tool_cli` with `acme/sync` and with `acme`.
4. From the home folder: `lore resource graph --id sync_web --store acme/sync` and `--store acme`, `graph --id sync_go --up`, `impact --id sync_go` with `acme/sync` and `acme`, `impact --id tool_cli --store acme/sync`; inside the store clone's `sync` folder `graph --id sync_web`, `impact --id sync_go`, `stale`, `report`.
5. From the home folder with `--json`: `resource graph --id sync_web` and `impact --id sync_go` through `acme/sync`, `memory list`, `memory show`, `search`, `resource list`.
6. From the home folder: `lore resource check --id sync_go --store acme/sync --yes`, `--id shared_ci --store acme`, `--id sync_web --store acme`, `--id tool_cli --store acme/sync`, and `--id sync_go` with no store, then with `--setup`.

## What must hold

- `trash list --store acme` lists the row trashed in `acme/sync`, as `--store acme/sync` does; it never says the trash is empty.
- `resource stale` and `report` with `--store acme/sync` list `sync_go` and `sync_web` only, with a line naming `lore resource stale --store acme` (or `report`); with `--store acme` every resource.
- `resource graph --id tool_cli --store acme/sync` is refused naming `lore resource graph --id tool_cli --store acme`, which works.
- `resource check --id <name> --store acme[/sync]` works from the home folder and records the check on the row where it is stored (a row of `acme/sync` through `--store acme` too); a resource of another folder is refused naming `lore resource check --id tool_cli --store acme`.
- `graph` and `impact` through `acme/sync`, from the home folder and inside the store clone's `sync` folder, walk the folder's resources: `shared_ci` prints as `shared_ci (acme/org, outside this folder)` and is not followed, `ops_dash` is never listed, and the line under names `lore resource graph --id sync_web --store acme` (or `impact`); with `--store acme` both are walked. `impact --id tool_cli --store acme/sync` is refused naming `lore resource impact --id tool_cli --store acme`.
- Graph and impact `--json` nodes carry `store` (`acme/org`, `acme/sync`) and `outside: true` for a left-out resource; memory list/show (`_label`), search (`_source`) and resource list (`store`) through `acme/sync` name each row's place.
- With no store from the home folder the hint names `--store <org>`; no answer says "--store does not apply".

## Findings it guards

- `devco:D-586`, `devco:D-606`, `devco:D-618`, `devco:D-621` (REGRESSIONS.md, owner use pass 2 follow-ups).
