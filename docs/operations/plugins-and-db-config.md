# Plugin allow-list and database settings

**In short:** `lore plugin` keeps a list of plugin files you trust, each named with a fingerprint of its exact contents. `lore config get/set/list` reads and writes small settings stored inside the database. Both are per machine and rarely needed; this page shows what they do today and where the sharp edges are.

## What it is and when you want it

**The plugin allow-list.** A plugin is an extra program that a tool runs on your behalf. Trusting one by name alone is risky: someone could swap the file. So lore's list records the name **and** the file's SHA-256 fingerprint (a 64-character code computed from the file's contents; change one byte and the code changes completely). The list is a per-machine decision: each person decides what runs on their own computer, so it is never shared through git.

Today the list is only kept: nothing in this version of lore runs plugins yet. You can prepare it now; it does nothing else.

**Database settings.** `lore config get/set/list` works on a small key-value table inside `.lore/lore.db` (key-value: each setting is a name and a text value, like a labelled jar). lore uses it for its own bookkeeping, such as the database's schema version and a fingerprint of the search index. It is not the same as `~/.lore/config.yaml`, which holds your orgs and stores and is checked with `lore config check` (see [Lore for teams: commands](../team/commands.md#lore-config-check)).

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
lore sync: exported 1
lore sync: 3 file(s) in .lore/data not committed — commit them with your change
$ lore plugin list
tpg_76bd…3f10 hello  sha256:bfdeaeb08cff
```
<!-- /example -->

`lore plugin untrust <id>` removes an entry (the id is the `tpg_…` value in the list).

## Step 2 — Read and write database settings

`lore config get <key>` prints one value; a missing key is `E_NOT_FOUND` (exit 1). `lore config set <key> <value>` creates or replaces a value:

<!-- example: ops#db-config -->
```console
# alice, in website
$ lore config get schema_version
1
$ lore config set team.note 'Ask Bob before changing the build'
✓ team.note=Ask Bob before changing the build
$ lore config get team.note
Ask Bob before changing the build
$ lore config get color
ERROR: [E_NOT_FOUND] config key "color" not found
  hint: it may have landed on main while this branch is older: `lore sync peek origin/main`
(exit 1)
```
<!-- /example -->

`lore config list` prints every key with its value, one per line.

## Try it

Run `lore config list` in your repo. You will see lore's own keys:

| Key | What it is |
|---|---|
| `schema_version` | The version of the database layout. |
| `db_uuid` | A unique id for this database file. |
| `db_created_at` | When this database file was created. |
| `fts_registry_fingerprint` | A fingerprint of the search index layout. When it does not match the lore version, lore prints "search index is out of date" until you run `lore setup`. |
| `tx_clock_high_water` | The latest time lore gave a memory, so a later memory never gets an earlier time even when the computer clock jumps back. |
| `last_checkpoint_at`, `last_vacuum_at` | Kept for database tidy-up times; empty in this version. |

## Pitfalls

- **A short fingerprint breaks `lore plugin`.** `lore plugin trust` does not check that `--sha256` is 64 characters. With a shorter value it crashes after saving the entry, and from then on `lore plugin list` crashes too. Always pass the full fingerprint (Step 1). If it already happened, restore a backup from before (see [Back up, restore and repair](backup-restore-repair.md)).
- **`lore config set` does not check keys.** Any key is accepted, including lore's own. Changing `schema_version`, `db_uuid` or `tx_clock_high_water` by hand can confuse lore; leave them alone. Changing `fts_registry_fingerprint` only brings back the "out of date" hint until the next `lore setup` (that is how the [CI page](ci.md#step-2--quiet-the-setup-hint) shows the hint).
- **Both are per machine.** A rebuild of the database from `.lore/data` starts with an empty plugin list and lore's default settings.
- **Not for team settings.** Orgs, stores, live rows and other team settings live in `~/.lore/config.yaml`; see the [config reference](../reference/config.md).

## Related

- [Secrets and personal data](secrets-and-pii.md)
- [Use lore in CI](ci.md)
- [Config file reference](../reference/config.md)
- [Operations index](README.md)
