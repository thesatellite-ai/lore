# The GitHub workflow

**In short:** GitHub's merge button does not know how to merge lore's note files, so two pull requests that changed the same note can show a conflict that lore would settle in a second. `lore sync install-action` adds a GitHub workflow (a job GitHub runs for you) that merges main into such a pull request with lore and pushes the result. By default it runs only when you start it, so it never spends GitHub Actions time on its own; with `--auto` it runs by itself on pull requests.

## What it is and when you want it

On your machine, git calls lore's merge driver ([Hooks and the merge driver](hooks-and-merge-driver.md)). GitHub's web merge never calls custom drivers, so for GitHub, a change Alice (an agency developer) made to a rule's strength and a change Bob (another agency developer) made to its text are a plain text conflict. There are two ways out:

1. **By hand:** merge main into the pull request branch on your machine (`git pull origin main`), then push. lore's driver settles the note files.
2. **The workflow:** let GitHub run `lore sync ci-merge` for you.

`lore sync ci-merge --base <branch>` merges that branch into the current one with lore's driver and commits, but only when a plain merge would conflict, every conflict is in lore's files (`.lore/data` or `.lore/LORE.md`), and lore settles all of them. Anything else (a code conflict, the same text changed on both sides) is left for a person. It never pushes; the workflow does that.

You want the workflow when your team merges through pull requests and edits the same notes often. If conflicts in `.lore/data` are rare, merging by hand is enough.

## Before you start

- A repo on GitHub set up with lore ([How notes travel with git](how-notes-travel-with-git.md)).
- Know the cost: GitHub Actions time is billed on private repositories. Each run is usually under a minute. The default install runs only when someone starts it; the automatic version (`--auto`) runs only when lore data changes on a pull request or on main.
- To have your required checks (the tests GitHub must see pass before a merge) run again on the workflow's merge commit, a repository secret `LORE_SYNC_TOKEN` (a token, like a password for scripts, that can write to this repo). Without it the workflow uses GitHub's default token, whose pushes do not start other workflows.

## Step 1 — Try `ci-merge` on your machine

Run it where you can see it before you let GitHub run it. Bob changed the "Buttons are blue" rule's text on his branch, and Alice changed its strength on main. `--dry-run` says what would happen; the real run commits the merge, and a second run finds nothing to do:

<!-- example: sync#ci-merge only=4,5,6,7 -->
```console
# bob, in website
$ git fetch -q origin && lore sync ci-merge --base origin/main --dry-run
✓ would merge origin/main: 1 lore file(s) settled field by field
  .lore/data/rules/rul_b4f4…a5b0.json
$ lore sync ci-merge --base origin/main
✓ merged and committed origin/main: 1 lore file(s) settled field by field
  .lore/data/rules/rul_b4f4…a5b0.json
  commit 9b40ce71af28 — push it to update the pull request
$ lore rule list
rul_162d…9666 [must/always] Run the tests before you push.
rul_76bd…3f10 [must/always] Never put passwords in the code.
rul_b4f4…a5b0 [should/always] Buttons are blue, links are green.
$ lore sync ci-merge --base origin/main
✓ origin/main is already merged into this branch
```
<!-- /example -->

The outcome is always one of `up-to-date`, `clean` (GitHub can merge it as it is), `merged` (committed here, ready to push) or `needs-human` (nothing changed; a person must merge). The command exits 0 for every outcome; a non-zero exit means the command itself failed. `--json` gives the outcome for scripts.

## Step 2 — Look at the triggers before you install

`--dry-run` prints the whole workflow and writes nothing. The `on:` part is what makes it run. **By default it runs only by hand**: the only trigger is `workflow_dispatch`, a "Run workflow" button in GitHub's Actions tab:

<!-- example: sync#workflow-preview -->
```console
# alice, in website
$ lore sync install-action --dry-run | grep -A 6 '^on:'
on:
  workflow_dispatch:
    inputs:
      base:
        description: "Base branch whose open pull requests to process"
        required: true
        default: "main"
```
<!-- /example -->

## Step 3 — Install it to run only by hand

`lore sync install-action` with no flags writes the workflow with that one trigger. It runs only when someone starts it (Actions, then "lore sync merge", then "Run workflow"), so it never spends Actions time on its own:

<!-- example: sync#workflow-manual only=1 keep=^(✓|\s+(runs|re-run|commit)) -->
```console
# alice, in website
$ lore sync install-action
✓ created ~/work/website/.github/workflows/lore-sync-merge.yml
  …
  runs only when started by hand (Actions → lore sync merge → Run workflow)
  re-run with --auto to also run it when lore data changes (spends Actions time)
  commit the file to enable it; add a LORE_SYNC_TOKEN secret if required checks must re-run on its merges
```
<!-- /example -->

The `on:` part of the new file has only the by-hand trigger:

<!-- example: sync#workflow-manual only=2 -->
```console
# alice, in website
$ grep -A 6 '^on:' .github/workflows/lore-sync-merge.yml
on:
  workflow_dispatch:
    inputs:
      base:
        description: "Base branch whose open pull requests to process"
        required: true
        default: "main"
```
<!-- /example -->

Commit `.github/workflows/lore-sync-merge.yml` to enable it. When started by hand it processes every open pull request on the base branch you give it. The comment at the top of the file shows the trigger lines to add if you want it to run by itself; re-running with `--auto` (Step 4) adds them for you and keeps the file updating itself.

## Step 4 — Or let it run by itself, or remove it

`--auto` adds the automatic triggers: the workflow then runs whenever lore data changes on a pull request or on a pushed branch. `--branch` picks the branches whose pushes start it (repeat it for several, e.g. `--auto --branch main --branch staging`). To remove it, delete `.github/workflows/lore-sync-merge.yml` and commit the deletion:

<!-- example: sync#workflow-automatic keep=^(✓|on:|\s+(runs|commit|pull_request|push|workflow_dispatch|types|paths|branches)) -->
```console
# alice, in website
$ lore sync install-action --auto
✓ updated ~/work/website/.github/workflows/lore-sync-merge.yml
  …
  runs when lore data changes on a pull request or on: main
  commit the file to enable it; add a LORE_SYNC_TOKEN secret if required checks must re-run on its merges
$ grep -A 7 '^on:' .github/workflows/lore-sync-merge.yml
on:
  pull_request:
    types: [opened, synchronize, reopened]
    paths: [".lore/data/**"]
  push:
    branches: ["main"]
    paths: [".lore/data/**"]
  workflow_dispatch:
$ rm .github/workflows/lore-sync-merge.yml
```
<!-- /example -->

Re-running `lore sync install-action` without `--auto` or `--manual` keeps the triggers and branches the installed file already has, so an automatic workflow stays automatic and a manual one stays manual. `--manual` switches an automatic workflow back to run-by-hand only (it is also accepted on a new install, where it changes nothing).

## Pitfalls

- **The default is run-by-hand only.** `lore sync install-action` with no flags never runs on its own; add `--auto` to have it run on every pull request that changes lore data (that spends Actions time). Workflows installed by older lore versions keep their automatic triggers, including when lore updates their version.
- **Nothing happens until you commit the file.** Installing only writes it.
- **Pull requests from forks (someone's own copy of your repo) are skipped.** No token in your repo can push to someone else's fork; merge those by hand.
- **The lore version in the file updates itself.** The workflow downloads a lore release. When a developer runs a newer lore release in the repo, lore rewrites the file to that release and asks them to commit it; it never goes back to an older one. `--lore-version vX.Y.Z` pins a version that is never changed. If you edit the file by hand, lore leaves it alone from then on.
- **lore will not overwrite a file it did not write.** If `.github/workflows/lore-sync-merge.yml` already exists without lore's first line, install and uninstall refuse; `--force` overrides.
- **A lore project in a subfolder** gets its own file, `lore-sync-merge-<folder>.yml`, which only watches that folder's lore data.
- **`needs-human` is normal.** The same text edited on both sides is a real conflict on purpose; see [Conflicts and duplicates](conflicts-and-duplicates.md#step-2--the-same-text-changed-on-both-sides).

## Related

- [Conflicts and duplicates](conflicts-and-duplicates.md)
- [Hooks and the merge driver](hooks-and-merge-driver.md)
- [Lore for teams › Protected main](../team/how-it-works.md#protected-main-the-ride-along-commit): how finished live rows reach a protected main.
- [Feature index › Git sync](../reference/features.md#git-sync-notes-travel-with-your-commits)
