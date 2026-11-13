# Upgrade lore: `lore upgrade` and version pins

**In short:** `lore upgrade` replaces the lore program with the newest release, with the version your project pins, or with exactly the version you name. A team can pin one version in `.lore/data/_meta.json` so everyone (and CI) runs the same lore, and once a day lore prints one line when a newer release is out. This page shows what each case does and how to switch the notice off.

## What it is and when you want it

lore is one program. A new release can add commands, fix bugs and, now and then, write notes in a way older versions cannot read. So two things matter: getting new releases without fuss, and keeping a team on versions that understand each other's notes.

- `lore upgrade` installs a release. It works out how lore was installed and does the matching thing: Homebrew installs are handed to `brew upgrade thesatellite-ai/tap/lore`; installs from the install script download the release archive, check it against the release's `checksums.txt`, run the new program once as a smoke test (`lore version --json`), and then swap it in; a program built from source is not replaced: it prints how to update it (`git pull && task lore:build`) and, inside a project, still runs `lore fix` there (`--no-fix` skips it); anything else (a package manager lore does not know) is left alone.
- A **version pin** is the version a project wants: `lore_version_pin` in `.lore/data/_meta.json` (committed, so the whole team shares it), or `LORE_VERSION_PIN` in the environment, which wins over the file (handy in CI). `lore upgrade` installs the pin instead of the newest release, and `lore doctor` says when your lore is not the pinned one.
- The **upgrade notice** is one line, at most once a day, after a command finished: `lore 0.1.19 is available (you have 0.1.18). Run: lore upgrade`. `upgrade_notice` and `auto_upgrade` in `~/.lore/config.yaml` turn it off, or make lore upgrade itself instead.

You want this page when the notice appears, when a teammate's notes need a newer lore, or when you set up CI and want the build to use one known version.

## Before you start

- lore was installed with Homebrew or the install script. A source build is updated with `git pull` and a new build; `lore upgrade` tells you so (Step 1) and only runs `lore fix` for the project you are in.
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

On a script install it prints the old and new version and where the program lives, the archive it downloaded and its size, `sha256 ok`, then `Done. Running lore processes keep 0.1.18 until they exit.` Inside a project it also runs the new program's `lore setup --schema-only`, which brings the local cache's schema up to date, and then the new program's `lore fix` for that project (next section; `--no-fix` skips it, outside a project it says it skipped it, and a program built from source installs nothing, so `lore upgrade` there prints its one line and runs no `lore fix`). Only one upgrade runs at a time on a machine: a second one waits for the lock in `~/.lore/state/upgrade.lock` and then finds the new version already there.

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
  "project_id": "prj_c0f3…b5b8",
  "repo_id": "rep_05e9…4603",
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

## Fix a project: `lore fix`

`lore fix` brings the project you are in up to date in one go. It runs, in order, everything `lore doctor` can repair for it and prints one line per step (what, where, result), then checks again and prints the status last. It never changes a project's format by itself: in a project still on the lore 0.1.17 format, `lore fix` (and `lore fix --dry-run`) only prints `<project> is on the lore 0.1.17 format; run lore migrate run to upgrade it (teammates need lore 0.1.18)` and exits 24 (`E_FORMAT_OLD`), changing nothing. `lore fix --migrate` migrates it (its migrate step is `lore migrate run`) and then runs the other steps; run it once, as a person, when the team is ready for lore 0.1.18. `lore doctor --fix` never migrates either: its migrate step prints the same line.

In a project that is already current every step says `current`:

<!-- example: files#lore-fix -->
```console
# alice, in website
$ lore fix --dry-run
lore fix: ~/work/website
  --     migrate (~/work/website/.lore/data): current
  --     folder scopes (~/work/website): current
  --     render (~/work/website/.lore/LORE.md): current
  --     agent hooks (~/work/website): current
  --     old clones (~/.lore/clones): current
  --     0.1.17 files (~/work/website/.lore): current
  --     doctor fixes: current
Nothing changed (dry run).
Status: HEALTHY
$ lore fix
lore fix: ~/work/website
  --     migrate (~/work/website/.lore/data): current
  --     folder scopes (~/work/website): current
  --     render (~/work/website/.lore/LORE.md): current
  --     agent hooks (~/work/website): current
  --     old clones (~/.lore/clones): current
  --     0.1.17 files (~/work/website/.lore): current
  --     doctor fixes: current
Everything is current.
Status: HEALTHY
```
<!-- /example -->

A step that changed something says `ok` and what it did, with one line per detail under it. Rows migrated in a code repo are never committed by lore (you commit them with your code), so the output ends, just above the status, with the one command to run, for example `1 upgraded file in .lore/data waits for your commit: git add .lore/data && git commit -m "lore: migrate rows to format 2"`. After the steps, `lore fix` also runs the active setup's checks that `lore doctor` runs (git email, store access and the like; they only read), so the two give one status for the repo: each problem prints as a `warning: setup <name>: …` line with its fix, and each note as `note: setup <name>: …`, which changes nothing in the status. `--dry-run` leaves them out, since it reaches no remote.

- **migrate** (only with `--migrate`; without it a 0.1.17-format project is refused, see above) rewrites the row files lore 0.1.17 wrote (`_v: 1`), converts every store the repo uses that still has lore 0.1.17's `lore-store.yaml` (also when the repo itself holds no 0.1.17 row) and moves it to the new layout, and finishes an interrupted migration, exactly as `lore migrate run` does (backup ref, team lock, `lore migrate rollback` undoes it). A row a newer lore moved to another store (an org store, say) while this checkout kept its old copy (a row file, a `lore.db` row or a live row) is settled the way the move would have finished: the store whose copy records the move keeps the row, the old copy here becomes a tombstone (a copy edited here after the move is merged into the store's copy first), and each such row gets a `moved rows:` line naming the store.
- **render** refreshes `.lore/LORE.md` and the tools' files when they are stale, or when the migration just rewrote rows; its lines name the files it wrote. A store the migration just gave its id (`sto_…`) is rendered under that id at once, in `~/.lore/render/<repo id>/sto_….md`; files an earlier run wrote under the store's name (`name-….md`) are removed, and its machine-local audit file `~/.lore/state/audit/name-….jsonl` is folded into `sto_….jsonl` (older entries first), so each store has one name.
- **agent hooks** writes what `lore setup --agent-hooks` writes for the tools set up here, one line per settings file with the hooks it gets; never in a client repo, whose tracked files lore does not change, and never in a repo with no lore project of its own (no `.lore/data` or `.lore/stores.yaml`), where render and agent hooks both say `skipped: no lore project here`.
- **0.1.17 files** removes lore 0.1.17's `.lore/lore.db` (and its `-wal`, `-shm`) and `.lore/backups/` once migrate is current and every row the database holds is in a store here (a row file, a tombstone or a purge); otherwise it keeps them and says why (rows not migrated yet, a file committed in git, a row no store holds).
- **old clones** removes the store clones an earlier 0.1.18 build left in `~/.lore/clones`, only those `lore doctor` proved everything is in the new clone; a half clone or an old org link clone stays, and doctor names it. The folders the removed clones sat in (`<host>/<owner>/`, and `clones/` itself) go too once they are empty.
- **doctor fixes** are the checks' own repairs: `.git/info/exclude` in a client repo, a store clone's git identity, lore's merge driver, `config.yaml` style, your actor row, reversed `--supersedes` links and the rest. When the migrate step failed (the rows are still partly in the 0.1.17 format) they are skipped, so no repair rewrites rows the migration has still to finish; the checks still run and report, and the next `lore fix --migrate` finishes the migration first, then repairs.

A step that cannot run says why on its line (`FAILED`), the others still run, and the run ends with `Not everything is current: these steps failed, see their lines above: <steps>` instead of `Everything is current.`. A `lore fix` stopped half way (a timeout, Ctrl-C, a kill) is finished by the next one: it resumes the migration, skipping the rows already at `_v: 2`. Until then, row files the migration rewrote but you have not committed are never taken for hand edits. A second `lore fix` changes nothing and says `Everything is current.`; the status line after it can still be DEGRADED when a check needs something lore does not do by itself (`lore doctor` names it). `lore fix --dry-run` writes nothing, also as the very first command after an upgrade, and says what each step would do; it reaches no remote (no fetch, push or clone), so it is quick and safe offline. `lore fix --json` gives the steps and the status as JSON. The exit status is doctor's: 0 healthy, 1 degraded, 2 broken.

`lore doctor --fix` runs the same steps (it says so above its report), so the two never differ.

`lore fix --all` runs it in every repo the setups know: the repo you run it in, the repos in the enabled setups' folders and the known repos. Only a repo with a `.lore/` folder is one of them: a repo with no lore data is never run in, even when a store link names it by its remote; `--all` lists it as `skipped <repo>: no lore data …` and creates nothing there. `lore fix` inside such a repo says `no lore data in <repo>` (`E_NOT_A_STORE`) and changes nothing, and `lore doctor`, `lore doctor --fix` and reads there create nothing either (no `.lore/`, and nothing under `.git/`); `lore init` sets the repo up. It fetches every store clone once first (and every store an org entry lists, cloning it when this machine has none yet, so a new machine reads every link), then fixes `--jobs N` repos at once (default 8; `--jobs 1` one after another) and prints each repo's block as soon as that repo is done, whole and never mixed with another's. A repo that fails does not stop the others and is named with its exact error (a repo that hangs is stopped after 10 minutes and named). The last line sums it up: how many repos, how many fixed and failed, which failed, and how long it took (exit 1 when one failed). A repo still on the lore 0.1.17 format is left as it is: its block is the one line and the command that upgrades it there (`cd <repo> && lore fix --migrate`), it counts neither as fixed nor as failed, and the last line says how many such repos there are; `lore fix --all --migrate` migrates every one of them. With `--json` the blocks still stream on stderr and one JSON document (`repos`, `ok`, `jobs`, `wall_ms`) follows on stdout. A machine that comes from lore 0.1.17 has no setup folders yet, so there `--all` takes the repo you run it in, the repos lore has run in on this machine (the known repos and the repos recorded in `~/.lore/cache/uses/`) and the repos with `.lore/` up to four folders below your home folder, and says so in a line above the summary; lore 0.1.17 kept no list of its repos, so for repos elsewhere name the folders that hold them: `lore fix --all --dirs ~/code --dirs ~/work` looks through each named folder as deep as a setup's folders (a name that is not a folder is refused); or add those folders to a setup. When it finds none it says how it chooses repos and what to do, instead of only `0 repo(s)`.

**A project's format.** Every project has a format, recorded in its committed `.lore/data/_meta.json` as `"format": 2` (with `migrated_by` and `migrated_at`, who raised it and when). A project whose file has no `format` key is format 1, the lore 0.1.17 format, when it holds row files lore 0.1.17 wrote (`_v: 1`), and format 2 otherwise. `lore doctor` (the `format:` line of `[sync]`), `lore where` (beside `_meta.json`) and `lore migrate status` (`format:`) name it, for example `the lore 0.1.17 format (format 1); this lore reads it and does not write it until someone runs lore migrate run`. `lore migrate run` raises it: in a project on the lore 0.1.17 format it records `"format": 2`, `migrated_by` (you, the person, as `Name <email>`; when a coding agent ran it for you, the agent is recorded only in `migrated_via`, as a commit names it only in its `Lore-Agent` trailer) and `migrated_at` in `_meta.json` and writes `.lore/data/.0-needs-lore-0.1.18-or-newer` (one line: `this store needs lore 0.1.18 or newer: upgrade lore, then run lore fix`), which lore 0.1.17's `lore sync` names first (`.0-needs-lore-0.1.18-or-newer: not a table directory`), next to its own `upgrade lore` for each migrated row. Both files are part of the `.lore/data` change the run leaves for your commit, with the rewritten rows: one commit moves the project to the new format, and `lore migrate rollback` takes all of it back. The run rewrites every row file lore 0.1.17 wrote, its tag links (`entity_tags`) included: it first folds them into the `tag_ids` of the rows they tag, `--offline` too, so no row file is left at `_v: 1`. `_meta.json` and `_purged.json` are not rows: their own `_v` is not the row format, and the run never lists them as left behind. The run changes no tracked file outside `.lore/data`: it keeps lore's local files (`.lore/lock`, `.lore/data/**/.tmp-*`) out of git status through the clone's own `.git/info/exclude`, never `.gitignore`, and leaves the merge driver, `.gitattributes` and the hook blocks to the next lore write, `lore sync` or `lore fix`.

**A project with lore 0.1.17 rows.** Its format is lore 0.1.17's until someone runs `lore migrate run` there; until then this lore reads it normally but writes nothing into it. A read prints `lore: this project is on the lore 0.1.17 format; run lore migrate run to upgrade it (teammates need lore 0.1.18)` on stderr. A write to the project's own rows (a note, a live task, a landing, `lore land`) is refused with that same one line (`E_FORMAT_OLD`), before any other check such as a missing live list on the remote, and changes nothing, not even `.gitignore` or the git hooks; a write to your personal store or to an org store already on the new format still works. `lore sync` there syncs your stores and nothing of the repo. Commands that write only this machine's state (`lore setup check`, `apply`, `list` and `repair`, your session record, the render history) work there as anywhere else and exit 0. Nothing tracked changes until the migration: `lore render` writes no `LORE.md`, `LORE.full.md`, pointer block or folder file and says `lore render: skipped: <project> is on the lore 0.1.17 format; run lore migrate run …` (exit 0; `lore render --check` checks only the size budget, since the rendered files there are lore 0.1.17's), `lore setup --agent-hooks` refuses with `E_FORMAT_OLD`, and `lore doctor --fix` and every other command leave `.gitignore` and `.gitattributes` as they are (lore's local files stay out of git status through the clone's own `.git/info/exclude`; the merge driver and hook blocks in `.git` are still set up). `lore doctor` there names one next step, `lore migrate run` (the `format:` line and the `row_format` note), and the format alone does not make it DEGRADED: a clean 0.1.17-format repo reads HEALTHY, exit 0. Nothing migrates by itself: only `lore migrate run`, run by a person, raises the format. `lore migrate run` writes nothing to the code repo's remote: it holds only this machine's migration lock and says so; `lore migrate run --team-lock` (and `lore migrate rollback --team-lock`) also pushes a hold to `refs/lore/signals` on the remote while it runs, so two teammates never migrate at once, and removes it from the remote when it ends. Separately, the first command of the new lore on a machine (any command but `help` and `version`) moves your stores to the new layout, read or write alike (next section).

**What the migration changes, and what it does not.** The migration is a format change, not an edit: every rewritten row keeps its `updated_at`, its `_hlc` and its author, and the tags step folds each row's 0.1.17 `entity_tags` links into its `tag_ids` inside the same rewrite. So a teammate's edit made with 0.1.17 on a branch (an archived rule, a changed body) survives when that branch is rebased, merged or cherry-picked across the migration commit: lore's merge driver compares a `_v: 1` side with a `_v: 2` side after converting it the same way, and treats a `null` field and a missing one as the same value. An `entity_tags` link whose row or tag no longer exists (0.1.17 left them behind a hard delete) is skipped with one line, `tags: skipped entity_tags/<file> (<table>/<row> → <tag>): the row <row> no longer exists; the link file stays as it is`, and the migration goes on. A step that fails part way puts back every file the run changed (and resets a store clone whose commit was not pushed yet), so a failed run never leaves the repo mixed `_v: 1` / `_v: 2`; its error ends with `the run's changes were put back; the data is as it was before the run`. The actor copies the `actor_projection` step trimmed and the `tag_ids` it set are never taken for hand edits by the next command.

**Undoing the migration.** `lore migrate rollback`, on the machine that ran it, is the supported undo. A `git revert` of the migration commit puts the `_v: 1` files back but keeps every row written after the migration at `_v: 2` (and a `.0-needs-lore-0.1.18-or-newer` signpost a later commit added): neither lore 0.1.17 nor this lore can write in such a repo. `lore doctor` there warns `row_format: this repo is on the lore 0.1.17 format but N row file(s) are at _v: 2 …, as after a git revert of the migration commit …; fix: lore migrate run`. While the revert (or a merge, cherry-pick or rebase) is still in progress, lore leaves the changed row files to git and reports none of them as hand edits.

**Upgrade every machine of every teammate together.** A shared store lore 0.1.17 made (an org store, your git personal store) stays on 0.1.17's layout until someone runs `lore migrate run` (or `lore fix --migrate`, which runs it): until then this lore reads it but writes nothing into it, and a write that would go there is refused with one line, `store <name> is on the lore 0.1.17 layout; run lore migrate run to upgrade it (teammates need lore 0.1.18)` (`E_FORMAT_OLD`); the first command on 0.1.18 says that line once per store. `lore migrate run` makes the store's layout commit and the move of 0.1.17's `everywhere/` personal notes, and pushes them. A `--no-push` or `--offline` run of `lore fix` or `lore migrate run` pushes nothing at all (every store commit stays in the local clone, `store …: store commit … (local, not pushed)`) and leaves the move to the next command. Right before the first such commit of a store lore prints, on its own and once per store on each machine, `lore: store <name> moves to the 0.1.18 layout now: anyone who uses it with lore 0.1.17 (a teammate, or another machine of yours) must upgrade lore.` and the line below it says what they see. lore 0.1.17 checks only that every file of a store sits under a `.lore` folder and has no way to say that a store is newer, so once the commit reaches a teammate (or another machine of yours) still on 0.1.17, their lore says `E_NOT_A_STORE … is listed under stores: but is not a lore store (it holds non-lore files such as .0-needs-lore-0.1.18-or-newer, org/, …)`: the first name is a file lore keeps at the store's top so that 0.1.17's message names it first (it holds one line, `this store needs lore 0.1.18 or newer: upgrade lore, then run lore fix`); the hint 0.1.17 adds, `create one with lore store init`, is wrong for them, on every write that goes to that store, and in a repo linked to an org store (`lore org link`) also in the 0.1.17 pre-commit hook, so their `git commit` there fails; their reads leave out the store's rows without saying so. Nothing is lost: they install this lore and run `lore fix`, and everything is back. Until then `git commit --no-verify` commits code past the old hook. So install 0.1.18 on every machine of every teammate before anyone runs `lore migrate run`.

## From an earlier 0.1.18 build: lore moves your stores itself

Builds of 0.1.18 before the plain store layout kept each store as a bare clone plus a linked worktree under `~/.lore/clones/<host>/<owner>/<repo>.git` and `<repo>/work`, with the rows under `<folder>/.lore/data/` and the org config at `org/.lore/lore-org.yaml`. The first command of this lore (any command but `help`, `version`, a `--help` or `-h` call and the git hooks) moves all of it, without asking, and says what it moved:

```text
lore: moved store clone ~/.lore/clones/github.com/acme/lore-hub -> ~/.lore/stores/acme/lore-hub (kept 1 local commit)
lore: moved store clone ~/.lore/clones/work-gh/acme/lore-east -> ~/.lore/stores/acme/lore-east
lore: old clone folders kept; `lore doctor` names how to remove them once you have checked the moves
lore: ~/.lore/clones-v017 holds 1 org link clone(s) of the earlier build; this lore no longer uses them (`lore doctor` names the command that removes them)
lore: pushed store lore-hub: commit 3f2a9c1 to branch main
lore: store lore-hub: rows moved from <folder>/.lore/data/ to <folder>/ (14 file(s); folders: org, team): commit 8d04e7b
lore: pushed store lore-hub: commit 8d04e7b to branch main
```

- **The clone move** makes a plain clone at `~/.lore/stores/<owner>/<repo>` (`<repo>@<branch>` for a pinned branch) from the old one, on this machine, without the network: every branch is copied, so a commit that was never pushed is still waiting to be pushed, and uncommitted changes in the old worktree are copied as uncommitted changes. The host never appears in the path, also when the old clone's origin uses an ssh alias of yours (`git@work-gh:acme/lore-east.git` lands at `stores/acme/lore-east`, and the origin keeps your spelling); lore reads your `~/.ssh/config` to tell that `work-gh` is github.com, and only for a host the config does not name takes a host without a dot for an alias. A worktree in the middle of a merge or rebase, or a file changed differently in both places, is refused with the reason and the step to take, and nothing of that clone moves.
- **Pending commits** are pushed right away when the store's push mode is `now` and the remote answers; otherwise they wait in the new clone and `lore sync` pushes them, as any local commit.
- **The layout commit**: each store whose files are still in the old layout gets one commit, signed like lore's other commits there, titled `lore: store layout 0.1.18 — rows move from <folder>/.lore/data/ to <folder>/`. It moves every row and each folder's `_meta.json` and `_purged.json`, moves `org/.lore/lore-org.yaml` to `org/lore-org.yaml`, drops a wrong `org_folder` from `lore-store.yaml`, writes the store's `README.md` and the signpost `.0-needs-lore-0.1.18-or-newer` (the file lore 0.1.17 names first when it refuses the store; a store moved by an earlier build gets it with lore's next commit there), and sets `lore_store: 4` in `lore-store.yaml`. It is pushed per the store's push mode, with the push line printed. A teammate who runs the new lore first already pushed it; you then only fetch it.
- **A teammate still on an older 0.1.18 build** cannot read the new layout. After the layout commit reaches them, their lore refuses the store with `E_OLD_LORE` (`lore-store.yaml was written by a newer lore`) instead of writing rows into the old place: they upgrade, and their next command moves their own clones the same way. If an older lore did write a row into an old place after the move, the next run of the new lore moves it too (a row found at both places is merged field by field).
- **The old folders stay until you run `lore fix`.** Commands never delete `~/.lore/clones` or `~/.lore/clones-v017`; `lore fix` removes only the old clones doctor proved safe. `lore doctor` lists each old clone under `old_clones`: once everything in it is in the new clone (carried or pushed) it is a note with the exact command, for example `rm -rf ~/.lore/clones/github.com/acme/lore-hub ~/.lore/clones/github.com/acme/lore-hub.git`; while it still holds something the new clone lacks (a commit made there later, other uncommitted changes) it is a warning saying what, and the folder must stay.
- **It runs once.** The next command finds nothing to move and prints nothing; the check is a look at two folders. A run cut short (a closed laptop, a killed process) is finished by the next command: a half-built clone (`<repo>.moving`) is made again, and a layout commit cut short after the files moved is completed. What was moved is recorded in `~/.lore/state/layout-move.json`.

## From lore 0.1.17: its store clones and personal notes

lore 0.1.17 kept each store as a bare clone alone, `~/.lore/clones/<host>/<owner>/<repo>.git` (no worktree beside it), and wrote its commits straight into it. The first command of this lore moves each clone the same way as above (on this machine only, without the network) and names it as 0.1.17's; the store itself stays on 0.1.17's layout until `lore migrate run`:

```text
lore: moved store clone ~/.lore/clones/github.com/acme/lore-notes.git (0.1.17) -> ~/.lore/stores/acme/lore-notes
lore: moved store clone ~/.lore/clones/github.com/alice/lore-me.git (0.1.17) -> ~/.lore/stores/alice/lore-me (kept 2 local commits)
lore: pushed store lore-me: commit 3b0cc44 to branch main
lore: store lore-me is on the lore 0.1.17 layout; run lore migrate run to upgrade it (teammates need lore 0.1.18)
```

- **Commits 0.1.17 never pushed** (saved offline, or saved after a teammate's layout commit, when 0.1.17 answers `E_NOT_A_STORE`) are carried into the new clone and pushed per the push mode; a row in the old place is then moved by the layout commit. When the new clone already has commits the old one lacks, nothing is moved and lore names the push to run (`git -C ~/.lore/clones/…/<repo>.git push origin main`).
- **A bare clone with no commit of its own** (0.1.17 only fetched there) has nothing to carry; the store is cloned on its next use.
- **The old bare clone stays** until `lore fix` removes it: `lore doctor` names it under `old_clones` as left from lore 0.1.17 with the command, `rm -rf ~/.lore/clones/github.com/acme/lore-notes.git`, once everything in it is in the new clone.
- **Personal notes** keep their scope (see [Personal notes](../team/use-cases/personal-notes.md)): the ones 0.1.17 filed under `everywhere/` move to `_store/` and load in every repo; the ones it filed under an org's folder stay in that folder and load only in that org's repos (as `me/<org>`). `lore doctor` warns while `everywhere/` is not moved yet, and names an org folder of an org this machine has no entry for.
- **A dry run moves nothing.** A first command run with `--dry-run` (`lore fix --dry-run`) only says what the next command will move (`lore: would move store clone … (dry run: nothing moved; the next command does it)`). `lore fix --dry-run` also clones no store, writes no file under `~/.lore` and sets nothing up in a clone; a step that needs a store not on this machine yet says `would fetch, clone or move the stores this project uses …, then migrate …` instead of failing, the live line says `dry run: using live rows as fetched …` (not offline), and it exits 0 when every finding is one the real run fixes (`Status: DEGRADED (each finding is one the real lore fix run fixes)`).
- **0.1.17 live rows of a linked store** (a task 0.1.17 saved live in a client repo) are brought in by `lore migrate run`; a read names them and says to run it.

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

- **A source build never upgrades and never hears about releases.** `lore upgrade` prints `git pull && task lore:build` and, inside a project, runs `lore fix` (`--no-fix` skips it); the daily notice stays quiet.
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
