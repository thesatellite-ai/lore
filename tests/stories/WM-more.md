# WM — one org's reads and resource checks from the home folder

**Who.** Ada (made-up names) is the admin of the org `acme`, whose store has the folders `org`, `sync` and `tools`. The kind `code_repo` is defined in `acme/org`; `acme/org` holds `acme_app` (project `sync`), `tool_cli` (project `tools`) and `shared_ci` (org-wide), and `acme/sync` holds `sync_web`.

**Why.** After use pass 2's fixes the owner's workers still met two reads that answered from one folder: `trash list --store acme` from the home folder said "the trash is empty" while `acme/sync` held a trashed row (D-431 says `--store <org>` reads every folder), and `resource check --id <name>` from the home folder refused "this command works on the folder it runs in, so --store does not apply" although the resource is in a store; `resource stale`, `report` and `graph` with `--store acme/sync` listed every resource of `acme/org` too.

**Test.** `TestStoryWMMore` (tests/stories/WM_more_test.go), about 8 s; the hint guard is `TestHomeHintNeverSaysStoreDoesNotApply`, the store-name rule `TestInNamedStore`.

## Steps

1. Ada starts the team in `~/code/platform` and adds the kind, its fields and the four resources with `--to`.
2. From the home folder: a memory of `acme/sync` added and deleted; `lore trash list --store acme/sync`, then `lore trash list --store acme`.
3. From the home folder: `lore resource stale --store acme/sync` and `--store acme`, `lore resource report --store acme/sync`, `lore resource graph --id acme_app --store acme/sync`, `--id tool_cli` with `acme/sync` and with `acme`.
4. From the home folder: `lore resource check --id acme_app --store acme/sync --yes`, `--id shared_ci --store acme`, `--id sync_web --store acme`, `--id tool_cli --store acme/sync`, and `--id acme_app` with no store, then with `--setup`.

## What must hold

- `trash list --store acme` lists the row trashed in `acme/sync`, as `--store acme/sync` does; it never says the trash is empty.
- `resource stale` and `report` with `--store acme/sync` list `acme_app` and `sync_web` only, with a line naming `lore resource stale --store acme` (or `report`); with `--store acme` every resource.
- `resource graph --id tool_cli --store acme/sync` is refused naming `lore resource graph --id tool_cli --store acme`, which works.
- `resource check --id <name> --store acme[/sync]` works from the home folder and records the check on the row where it is stored (a row of `acme/sync` through `--store acme` too); a resource of another folder is refused naming `lore resource check --id tool_cli --store acme`.
- With no store from the home folder the hint names `--store <org>`; no answer says "--store does not apply".

## Findings it guards

- `devco:D-586`, `devco:D-606` (REGRESSIONS.md, owner use pass 2 follow-ups).
