# Plugin allow-list and database settings

**In short:** `lore plugin` keeps a list of plugin files you trust, each named with a fingerprint of its exact contents. `lore config get/set/list` reads and writes your personal settings in `~/.lore/config.yaml`, and `lore policy` sets the rules a whole team follows. All are rarely needed; this page shows what they do today and where the sharp edges are.

## What it is and when you want it

**The plugin allow-list.** A plugin is an extra program that a tool runs on your behalf. Trusting one by name alone is risky: someone could swap the file. So lore's list records the name **and** the file's SHA-256 fingerprint (a 64-character code computed from the file's contents; change one byte and the code changes completely). The list is a per-machine decision: each person decides what runs on their own computer, so it is never shared through git.

Today the list is only kept: nothing in this version of lore runs plugins yet. You can prepare it now; it does nothing else.

**Settings.** `lore config get/set/list` works on your own `~/.lore/config.yaml` (the file that also holds your orgs and stores; `lore config check` validates it). A key is a dotted path such as `default_branch` or `live.autopush`, and only keys lore actually reads are accepted. lore's own bookkeeping inside `.lore/lore.db` (the schema version, the search index fingerprint) is no longer something you read or set by hand. Rules for the whole team, such as how long the trash keeps deleted rows, are set with `lore policy set` instead: they are stored with the repo or the org, so everyone gets them.

You want these when a guide or a support request asks you to look at a setting, or when you prepare the plugin list for a tool that will read it.

## Before you start

- lore is set up in the repo.
- `sha256sum` (Linux) or `shasum -a 256` (macOS) to compute a file's fingerprint.

## Step 1 — Trust a plugin by its fingerprint

Alice (an agency developer) has a small script, `hello.sh`. She lets `sha256sum` compute its fingerprint and passes it straight to `lore plugin trust`. Pass the full 64-character fingerprint (`cut -c1-64` keeps only the code, without the file name); lore prints the first 12 characters back:

<!-- example: ops#plugin -->
```console
# alice, in website
$ lore plugin list
(no trusted plugins)
$ printf '#!/bin/sh\necho hello\n' > hello.sh
$ lore plugin trust --name hello --sha256 $(sha256sum hello.sh | cut -c1-64)
✓ plugin trusted: hello (sha256:bfdeaeb08cff)
lore sync: actors/act_2ad3…f891.json: lore.db change not written (this table's files are owned by the 0.1.18 core; change it with the core's commands, e.g. `lore memory`)
$ lore plugin list
tpg_b4f4…a5b0 hello  sha256:bfdeaeb08cff
```
<!-- /example -->

`lore plugin untrust <id>` removes an entry (the id is the `tpg_…` value in the list).

## Step 2 — Read and write your settings

`lore config set <key> <value>` writes one key of `~/.lore/config.yaml` (keeping its comments and order); `lore config get <key>` prints it, and `lore config list` prints every key you set. A key lore does not read is refused with `E_INVALID_INPUT` and nothing is written; a key that is not set is `E_NOT_FOUND` (exit 1):

<!-- example: ops#db-config -->
```console
# alice, in website
$ lore config set default_branch main
✓ default_branch = main (/tmp/<redacted>/alice/.lore/config.yaml)
$ lore config get default_branch
main
$ lore config list
default_branch=main
$ lore config set team.note 'Ask Bob before changing the build'
ERROR: [E_INVALID_INPUT] config key "team.note" is not a key lore reads (unknown key, ignored: line 2: field team not found in type lorecfg.Config)
  hint: nothing written; team rules are set with `lore policy set`
(exit 20)
$ lore config get color
ERROR: [E_NOT_FOUND] config key "color" not found
  hint: it may have landed on main while this branch is older: `lore sync peek origin/main`
(exit 27)
```
<!-- /example -->

## Step 3 — Set a rule for the whole team

A note such as "ask Bob before changing the build" is a rule: add it with `lore rule add` so every agent reads it. A setting the team must share, such as how long deleted rows stay in the trash, is a policy. `lore policy set` stores it for this repo (or, with `--to`, for a store or an org) and `lore policy show` prints the value that applies and where it comes from:

<!-- example: ops#team-policy -->
```console
# alice, in website
$ lore policy set trash.retention_days 365 --yes
✓ trash.retention_days = 365 (this repo, pol_…c6d7)
$ lore policy show trash
trash.retention_days	365	repo this repo (pol_…c6d7)
```
<!-- /example -->

`lore policy unset <key>` removes it again, so the level above applies.

## Try it

Run `lore config list` to see the keys you set, and `lore policy show` in a repo to see every team rule with its value and where it comes from.

## Pitfalls

- **A short fingerprint breaks `lore plugin`.** `lore plugin trust` does not check that `--sha256` is 64 characters. With a shorter value it crashes after saving the entry, and from then on `lore plugin list` crashes too. Always pass the full fingerprint (Step 1). If it already happened, restore a backup from before (see [Back up, restore and repair](backup-restore-repair.md)).
- **`lore config` is per person, not per repo.** It edits `~/.lore/config.yaml` on your machine only, so a teammate never sees it. Use `lore policy set` for anything the team must share.
- **The plugin list is per machine.** A rebuild of the database from `.lore/data` starts with an empty plugin list.
- **The full list of keys** is in the [config reference](../reference/config.md).

## Related

- [Secrets and personal data](secrets-and-pii.md)
- [Use lore in CI](ci.md)
- [Config file reference](../reference/config.md)
- [Operations index](README.md)
