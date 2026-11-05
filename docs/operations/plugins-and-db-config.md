# Your settings and team rules

**In short:** `lore config get/set/list` reads and writes your personal settings in `~/.lore/config.yaml`, and `lore policy` sets the rules a whole team follows. Both are rarely needed; this page shows what they do and where the sharp edges are.

## What it is and when you want it

**Settings.** `lore config get/set/list` works on your own `~/.lore/config.yaml` (the file that also holds your orgs and stores; `lore config check` validates it). A key is a dotted path such as `default_branch` or `live.autopush`, and only keys lore actually reads are accepted. lore's own bookkeeping inside `.lore/lore.db` (the schema version, the search index fingerprint) is no longer something you read or set by hand. Rules for the whole team, such as how long the trash keeps deleted rows, are set with `lore policy set` instead: they are stored with the repo or the org, so everyone gets them.

You want these when a guide or a support request asks you to look at a setting, or when the team needs one rule for everyone.

## Before you start

- lore is set up in the repo.

## Step 1 — Read and write your settings

`lore config set <key> <value>` writes one key of `~/.lore/config.yaml` (keeping its comments and order); `lore config get <key>` prints it, and `lore config list` prints every key you set. A key lore does not read is refused with `E_INVALID_INPUT` and nothing is written; a key that is not set is `E_NOT_FOUND` (exit 1):

<!-- example: ops#db-config -->
```console
# alice, in website
$ lore config set default_branch main
✓ default_branch = main (~/.lore/config.yaml)
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
  hint: `lore config list` shows the keys set in ~/.lore/config.yaml
(exit 27)
```
<!-- /example -->

## Step 2 — Set a rule for the whole team

A note such as "ask Bob before changing the build" is a rule: add it with `lore rule add` so every agent reads it. A setting the team must share, such as how long deleted rows stay in the trash, is a policy. `lore policy set` stores it for this repo (or, with `--to`, for a store or an org) and `lore policy show` prints the value that applies and where it comes from:

<!-- example: ops#team-policy -->
```console
# alice, in website
$ lore policy set trash.retention_days 365 --yes
✓ trash.retention_days = 365 (this repo, pol_…c6d7)
$ lore policy show trash
trash.purge_on_delete	false	default
trash.retention_days	365	repo this repo (pol_…c6d7)
```
<!-- /example -->

`lore policy unset <key>` removes it again, so the level above applies.

## Try it

Run `lore config list` to see the keys you set, and `lore policy show` in a repo to see every team rule with its value and where it comes from.

## Pitfalls

- **`lore config` is per person, not per repo.** It edits `~/.lore/config.yaml` on your machine only, so a teammate never sees it. Use `lore policy set` for anything the team must share.
- **The full list of keys** is in the [config reference](../reference/config.md).

## Related

- [Secrets and personal data](secrets-and-pii.md)
- [Use lore in CI](ci.md)
- [Config file reference](../reference/config.md)
- [Operations index](README.md)
