# Your settings and team rules

**In short:** `lore config get/set/list` reads and writes your personal settings in `~/.lore/config.yaml`, and `lore policy` sets the rules a whole team follows. Both are rarely needed; this page shows what they do and where the sharp edges are.

## What it is and when you want it

**Settings.** `lore config get/set/list` works on your own `~/.lore/config.yaml` (the file that also holds your orgs and stores; `lore config check` validates it). A key is a dotted path such as `default_branch` or `live.autopush`, and only keys lore actually reads are accepted. lore's own bookkeeping inside its cache (the schema version, the search index fingerprint) is no longer something you read or set by hand. Rules for the whole team, such as how long the trash keeps deleted rows, are set with `lore policy set` instead: they are stored with the repo or the org, so everyone gets them.

You want these when a guide or a support request asks you to look at a setting, or when the team needs one rule for everyone.

## Before you start

- lore is set up in the repo (`lore init`), as in [your first project](../knowledge/first-project.md). `lore config` works anywhere, even outside a repo; `lore policy` needs a repo, because a team rule is stored with the repo or one of its stores.
- Know which of the two you need. Ask "should my teammates get this too?". If no (your default branch, whether your live writes push at once), it is a setting: `lore config`. If yes (how long the trash keeps rows, how secrets are handled), it is a policy: `lore policy`. If it is advice for people and agents, it is neither: add a rule with `lore rule add`.

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
ERROR: [E_INVALID_INPUT] config key "team.note" is not a key lore reads (unknown key, ignored: line 2: team)
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
lore: wrote ~/work/website/.lore/data/policies/pol_28f8…03df.json
✓ trash.retention_days = 365 (this repo, pol_…03df)
$ lore policy show trash
trash.purge_on_delete	false	default
trash.retention_days	365	repo this repo (pol_…03df)
```
<!-- /example -->

A policy can be set at several levels: the repo (the default), a folder of a linked store, the store's `org/` folder (`--to <store>/org`, one rule for every repo of the org) or the store root. When two levels set the same key, the stricter value wins, so a repo can tighten what its org set but never loosen it. A `policies:` block in your own `~/.lore/config.yaml` adds one more level, for this machine only, that can also only tighten. `lore policy set` always asks before it writes; in a script or an agent session, pass `--yes`.

## Step 3 — Let lore refuse a bad setting, then check the whole file

`lore config unset <key>` removes one key the same way (comments and order of the rest kept; an emptied parent such as `personal:` goes too). `--dry-run` shows what would go and writes nothing; a key that is not set is `E_NOT_FOUND`, and a removal that would turn a valid file invalid is refused with `E_CONFIG_INVALID`. Use it for a value no setup needs any more, such as `lore config unset personal.store` after a setup was deleted permanently; the setup page lists those under "Unused settings in config.yaml" on Home, and `lore setup leftovers clear identity|personal_store` clears them from the command line.

`lore config fmt` rewrites `~/.lore/config.yaml` in block style (one key or list item per line) when a list or mapping sits on one line, such as `setups: [{name: personal, kind: solo, …}]` saved by an older lore; comments and key order are kept, a flat list such as `tools: [claude, codex]` stays as written, and `--dry-run` names the keys and writes nothing. No read rewrites the file: `lore doctor` warns (`config_style`) and names the command, and `lore doctor --fix` runs it.

`lore config set` checks the file it is about to write, not only the one key: a value of the wrong type is refused with `E_INVALID_INPUT` and the hint `nothing written`. Keys under `policies:` (the machine level of the team rules from Step 2) are refused by name, because a policy key holds dots and a dotted path cannot name it; edit that block by hand. `lore config check` then reads the whole file and names every problem in it at once, with where it is, or says it is valid:

<!-- example: ops#config-refusals -->
```console
# alice, in website
$ lore config set live.autopush maybe
ERROR: [E_INVALID_INPUT] config key "live.autopush": bad value "maybe" (line 3: live.autopush: expected true or false, got text)
  hint: nothing written; docs/reference/config.md lists every key with its type (a bool is true or false, a duration 45m or 2h)
(exit 20)
$ lore config set policies.trash.retention_days 30
ERROR: [E_INVALID_INPUT] config key "policies.trash.retention_days" is a policy key; lore config set does not edit policies:
  hint: nothing written; edit policies: in ~/.lore/config.yaml by hand (it only tightens), or set a team rule with `lore policy set`
(exit 20)
$ lore config set live.autopush true
✓ live.autopush = true (~/.lore/config.yaml)
$ lore config check
✓ ~/.lore/config.yaml is valid: 0 org entries
```
<!-- /example -->

Run `lore config check` after every hand edit of `~/.lore/config.yaml`: a mistake in that file blocks reading and writing notes in every repo until it is fixed. The [config reference](../reference/config.md#what-lore-config-check-refuses) shows its output for a broken file.

## Step 4 — Fix a mistake or remove a rule

Each key takes one kind of value, and a value it cannot take is refused with `E_INVALID_INPUT` and a hint naming what it takes. `lore policy unset <key>` removes the row at that level (the repo by default, `--to` for another), so the next level up or the built-in default applies again; `lore policy show` then says `(unset)` and `default`:

<!-- example: ops#policy-unset -->
```console
# alice, in website
$ lore policy set trash.retention_days soon --yes
ERROR: [E_INVALID_INPUT] policy set trash.retention_days: "soon" is not a valid value
  hint: trash.retention_days takes a whole number, 0 or more
(exit 20)
$ lore policy unset trash.retention_days --yes
lore: wrote ~/work/website/.lore/data/policies/pol_28f8…03df.json
✓ trash.retention_days unset in this repo (pol_…03df); the next level up or the default applies
$ lore policy show trash.retention_days
trash.retention_days	(unset)	default
```
<!-- /example -->

## Try it

Run `lore config list` to see the keys you set in `~/.lore/config.yaml`, and `lore policy show <prefix>` in a repo to see the team rules under one prefix, each with its value and where it comes from (`default` when nobody set it). Without a prefix, `lore policy show` lists every rule:

<!-- example: ops#try-settings -->
```console
# alice, in website
$ lore config list
default_branch=main
live.autopush=true
$ lore policy show secrets
secrets.agent_reveal	ask	default
secrets.manager_timeout	5	default
secrets.on_detect	ask	default
secrets.password_pair_prose_filter	true	default
secrets.use_repo_gitleaks	true	default
```
<!-- /example -->

## Pitfalls

- **`lore config` is per person, not per repo.** It edits `~/.lore/config.yaml` on your machine only, so a teammate never sees it. Use `lore policy set` for anything the team must share.
- **Only keys lore reads are accepted.** A made-up key such as `team.note` is refused, not stored for later; nothing is written. The full list of keys is in the [config reference](../reference/config.md).
- **`lore config list` shows what you set, not the defaults.** A key you never set is missing from the list and `lore config get` answers `E_NOT_FOUND` for it, even though lore uses its default.
- **`lore config set` keeps your comments and key order**, and writes the file in one step with its existing permissions, so a hand-commented file stays readable.
- **`policies:` in `~/.lore/config.yaml` can only tighten.** A looser value than the team's (or than the default) is ignored without an error. Check the source column of `lore policy show` to see which level won.
- **A repo cannot loosen its org's rule.** Setting a looser value in the repo leaves the org's stricter value in force; `lore policy show` names the level that applies.
- **`lore policy set` asks first.** Without a terminal and without `--yes` it does not write.

## Related

- [Rules, memories and decisions](../knowledge/rules-memories-decisions.md): advice for people and agents, as opposed to settings
- [Secrets and personal data](secrets-and-pii.md): the `secrets.*` policies in use
- [Use lore in CI](ci.md)
- [Config file reference](../reference/config.md)
- [Operations index](README.md)
