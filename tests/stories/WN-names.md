# WN — writes that name their row by name take the store from anywhere

**Who.** Ada (made-up names) is the admin of the org `acme`, whose store has the folders `org`, `sync`, `tools` and `web`. The kind `code_repo` with its fields is defined in `acme/org`, the meta key `owner_team` and a memory are in `acme/sync`, and her repo `~/code/web` is linked to `acme/web` (it attaches `acme/web` and `acme/org`).

**Why.** The owner's rule from use pass 1 (W4): every write takes `--to <org>/<folder>` from the home folder exactly as reads take `--store`. After up2more gave the resource writes `--store`, the other writes that name a row by name still took none (`resource kind rename`, `resource field rename/remove/retype`, `meta set/rename`, `project archive`, `repo archive` and their siblings), and inside a repo a write's `--store` was ignored: the row was found in the stores the repo attaches, whatever `--store` named.

**Test.** `TestStoryWNNames` (tests/stories/WN_names_test.go), about 15 s; the registry rule is `TestStoryWWByNameWritesTakeTheStore` (also run by the WW matrix through `wwOps`).

## Steps

1. Ada starts the team in `~/code/platform`, adds the kind and its fields with `--to acme/org`, a resource, a memory in `acme/tools`, the meta key and a memory in `acme/sync`, and links `~/code/web` to `acme/web`.
2. From the home folder: `lore resource kind rename --id code_repo --new-name code_repos --store acme/org --yes`, `kind edit … --to acme/org`, `field rename`, `field retype`, `field remove --store acme`, `meta rename`, `meta edit`, `meta set --id <memory> --pair owning_team=core --store acme/sync`, `project archive` and `unarchive --id sync --store acme/sync`; then `kind rename` and `meta rename` with no store.
3. In `~/code/web`: `lore resource kind edit --id code_repos --description 'changed in web' --store acme/tools --yes`, `meta rename … --store nosuch`, then `kind edit … --store acme`, and `project archive --id web --store acme/web`.
4. Still in `~/code/web` (devco D-617): `lore resource kind edit --id code_repos --description 'web only' --store acme/web --yes` (the kind lives in `acme/org`), then the same with `--store acme/org`; a memory added with `--to acme/org`, read with `memory show --id <it> --store acme/web` and written with `meta set --id <it> --pair owning_team=web --store acme/web`, then read with `--store acme/org` and `--store acme`.

## What must hold

- Every write in step 2 with a store flag works from the home folder and the rows change where they are stored; with no store flag the hint names `--store <org>` and never says the command takes no store flag.
- In the repo, a `--store` the repo does not attach is refused with `E_NOT_FOUND`: "--store acme/tools is not attached in this repo (it attaches acme/org, acme/web, me) … nothing was written", hint `cd ~ && lore resource kind edit --id code_repos --description 'changed in web' --store acme/tools`; the kind is unchanged.
- A store the repo attaches (`acme`, or its own folder `acme/web`) runs as before.
- A `--store` naming one attached store keeps the command to it: the kind edit and the by-id read and write with `--store acme/web` are refused with `E_NOT_FOUND` "… is not in acme/web; it is in acme/org (use --store acme/org)" and change nothing; `--store acme/org` and `--store acme` work.

## Findings it guards

- `devco:D-615`, `devco:D-616`, `devco:D-617` (REGRESSIONS.md, owner use pass 2 follow-ups).
