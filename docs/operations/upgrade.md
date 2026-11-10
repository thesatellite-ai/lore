# Upgrade lore: `lore upgrade` and version pins

**In short:** `lore upgrade` replaces the lore program with the newest release, with the version your project pins, or with exactly the version you name. A team can pin one version in `.lore/data/_meta.json` so everyone (and CI) runs the same lore, and once a day lore prints one line when a newer release is out. This page shows what each case does and how to switch the notice off.

## What it is and when you want it

lore is one program. A new release can add commands, fix bugs and, now and then, write notes in a way older versions cannot read. So two things matter: getting new releases without fuss, and keeping a team on versions that understand each other's notes.

- `lore upgrade` installs a release. It works out how lore was installed and does the matching thing: Homebrew installs are handed to `brew upgrade thesatellite-ai/tap/lore`; installs from the install script download the release archive, check it against the release's `checksums.txt`, run the new program once as a smoke test (`lore version --json`), and then swap it in; a program built from source only prints how to update it; anything else (a package manager lore does not know) is left alone.
- A **version pin** is the version a project wants: `lore_version_pin` in `.lore/data/_meta.json` (committed, so the whole team shares it), or `LORE_VERSION_PIN` in the environment, which wins over the file (handy in CI). `lore upgrade` installs the pin instead of the newest release, and `lore doctor` says when your lore is not the pinned one.
- The **upgrade notice** is one line, at most once a day, after a command finished: `lore 0.1.19 is available (you have 0.1.18). Run: lore upgrade`. `upgrade_notice` and `auto_upgrade` in `~/.lore/config.yaml` turn it off, or make lore upgrade itself instead.

You want this page when the notice appears, when a teammate's notes need a newer lore, or when you set up CI and want the build to use one known version.

## Before you start

- lore was installed with Homebrew or the install script. A source build is updated with `git pull` and a new build; `lore upgrade` tells you so (Step 1).
- The folder that holds the lore program is writable by you. If it is not, `lore upgrade` stops with `E_UPGRADE_PERMISSION`; upgrade through the tool that installed it, or with the rights that folder needs.
- Running lore processes keep the old version until they exit, so restart long-running ones (an editor's agent session, a `lore api` server) after the upgrade.

## Step 1 — See whether a newer release exists

`lore upgrade --check` reports and changes nothing. On an installed release it prints either `lore 0.1.18 is the newest release; nothing to do.` or `lore 0.1.19 is available (you have 0.1.18, installed by script). Run: lore upgrade`. Asking for the newest release needs the network.

The examples on these pages run a lore built from source, which never asks for releases, so this is what it prints there (a real run, no network):

<!-- example: ops#upgrade only=1,2 -->
```console
# alice, in website
$ lore version
lore dev
  db_schema:        v1
  bundle_format:    v1 (deferred to v0.2)
  plugin_protocol:  v1 (deferred to v0.2)
  mcp_tool:         v1 (deferred to v0.2)
$ lore upgrade --check
This lore (dev) was built from source. Update it with: git pull && task lore:build
```
<!-- /example -->

## Step 2 — Upgrade

```console
$ lore upgrade
```

On a script install it prints the old and new version and where the program lives, the archive it downloaded and its size, `sha256 ok`, then `Done. Running lore processes keep 0.1.18 until they exit.` Inside a project it also runs the new program's `lore setup --schema-only`, which brings the local cache's schema up to date. Only one upgrade runs at a time on a machine: a second one waits for the lock in `~/.lore/state/upgrade.lock` and then finds the new version already there.

`lore upgrade --version v0.1.18` installs exactly that release, older ones included. Homebrew keeps only the newest formula, so `--version` is refused for a Homebrew install (`E_UNSUPPORTED`).

Going back to an older version that could not write this project (it is below the project's `min_lore_version`, or older than a feature the project uses) asks first: `E_NEEDS_CONFIRM` without a terminal, answered with `lore upgrade --version <tag> --yes`. An older lore can still read such a project; it refuses writes until you upgrade again.

## Step 3 — Pin a version for the team

Add `lore_version_pin` to `.lore/data/_meta.json` and commit it. From then on, `lore upgrade` in this project installs the pin, not the newest release, and prints `lore 0.1.18 is this project's pinned version (lore_version_pin in .lore/data/_meta.json). Nothing to do.` when you already run it. `lore upgrade --version <other>` is refused with `E_UPGRADE_PINNED` until you add `--ignore-pin`, and `--ignore-pin` alone installs the newest release anyway (outside CI).

A pin below the project's `min_lore_version` would leave everyone on it read-only, so `lore doctor` reports it as BROKEN. Here the file pins 0.1.0 in a project that needs 0.1.18 (the edit is reverted after the check):

<!-- example: ops#upgrade only=3,5,6 -->
```console
# alice, in website
$ cat .lore/data/_meta.json
{
  "_v": 2,
  "features": [
    "hlc",
    "policy",
    "trash"
  ],
  "min_lore_version": "0.1.18",
  "project_id": "prj_76bd…3f10",
  "repo_id": "rep_c0f3…b5b8",
  "urls": [
    "github.com/client/website"
  ]
}
$ cp .lore/data/_meta.json ~/meta.copy && python3 -c "import json; p='.lore/data/_meta.json'; m=json.load(open(p)); m['lore_version_pin']='0.1.0'; json.dump(m, open(p,'w'), indent=2)" && lore doctor | grep -iE '^Status|pin'
Status: BROKEN
  - the pinned lore 0.1.0 (lore_version_pin in .lore/data/_meta.json) is below this project's min_lore_version 0.1.18: everyone on the pin is read-only; raise the pin
$ cp ~/meta.copy .lore/data/_meta.json && lore doctor | grep '^Status'
Status: HEALTHY
```
<!-- /example -->

When the pin is fine but your lore is another version, doctor adds a note instead: `this lore is 0.1.17; the project pins 0.1.18 (…): run lore upgrade`.

## Step 4 — Pin the version in CI

In CI (the `CI` variable is `true`, as GitHub Actions and most CI systems set it), `lore upgrade` without a pin and without `--version` is refused with `E_UPGRADE_PINNED`: a build should never pick up whatever release came out that morning. Set the version in the job:

```yaml
env:
  LORE_VERSION_PIN: "0.1.18"
```

`LORE_VERSION_PIN` wins over `lore_version_pin` in the file, for `lore upgrade` and for doctor's pin check alike:

<!-- example: ops#upgrade only=4 -->
```console
# alice, in website
$ LORE_VERSION_PIN=0.0.1 lore doctor | grep -iE '^Status|pin'
Status: BROKEN
  - the pinned lore 0.0.1 (LORE_VERSION_PIN) is below this project's min_lore_version 0.1.18: everyone on the pin is read-only; raise the pin
```
<!-- /example -->

## Step 5 — Turn the notice off, or upgrade automatically

The notice is checked at most once every 24 hours; the time of the last check is kept in `~/.lore/state/upgrade-check.json`, and a failed check is silent and tried again a day later. In a pinned project you only hear about a newer pin, never about newer releases. Nothing is checked at all (no network request) when any of these holds:

- `CI` is set, or stderr is not a terminal, or the command has `--json`;
- `LORE_NO_UPGRADE_NOTICE=1` is set (one shell or one CI job);
- the command is `lore upgrade` or `lore version`;
- lore was built from source or is a snapshot build;
- an agent runs the command;
- both `upgrade_notice` and `auto_upgrade` are `false`.

In `~/.lore/config.yaml`:

```yaml
upgrade_notice: false   # no daily line
auto_upgrade: true      # upgrade instead of printing the line
```

With `auto_upgrade: true` lore installs the new release itself and prints one line naming both versions (for example, upgraded 0.1.18 → 0.1.19). It only does this for Homebrew and script installs (others get `not upgraded automatically (installed by …)`), never past a pin, and a failed attempt leaves the old program in place and says why.

## Try it

`lore upgrade --check` is safe to run anywhere: it changes nothing. On an installed release it asks for the newest version once; on a source build it answers at once:

<!-- example: ops#upgrade only=2 -->
```console
# alice, in website
$ lore upgrade --check
This lore (dev) was built from source. Update it with: git pull && task lore:build
```
<!-- /example -->

A real upgrade is not shown on this page: the release address is built into lore and is never read from a setting or the environment (so nothing can point an upgrade at another server), which means an example would have to download a real release from the network. The examples on these pages never use the network.

## Pitfalls

- **A source build never upgrades and never hears about releases.** `lore upgrade` only prints `git pull && task lore:build`, and the daily notice stays quiet.
- **`LORE_VERSION_PIN` beats the file.** A pin left in your shell profile makes every project look pinned to that version; set it in CI jobs, not at home.
- **Only `1` silences the notice.** `LORE_NO_UPGRADE_NOTICE=true` is ignored.
- **A pin below `min_lore_version` makes doctor BROKEN.** Raise the pin when the team raises the floor; doctor names both versions.
- **Downgrades are allowed, writes may not be.** An older lore reads a newer project but refuses to write until you upgrade again; that is why the downgrade asks first.
- **After an upgrade, update the agent skill.** If a coding agent's lore skill is older than the new program, lore prints a hint to run `lore skill install --all` ([Install the agent skill](../knowledge/skill-install.md)).

## Related

- [Check lore's health with `lore doctor`](doctor.md)
- [Use lore in CI](ci.md)
- [Config file reference](../reference/config.md#top-level-keys)
- [Environment variables](../reference/environment.md)
- [Error codes: upgrade and migration](../reference/errors.md#upgrade-and-migration)
- [Operations index](README.md)
