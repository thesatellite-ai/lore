# Same store, separate staging

**In short:** the agency wants a place for test notes and staging-only tasks that never mix with the real ones. It uses a second branch of the same private store repo, chosen only when you ask for it.

## The situation

The agency wants a staging lore: test notes and staging-only tasks that must never mix with the real ones. It does not want a second store repo (a store is the agency's separate private notes repo).

## The config

Add a second org entry (a second company setup) on the **same store URL**, with another store branch, `staging`. Each store branch is a separate lore. Lore never moves rows between store branches.

```yaml
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: org}
    repos: [github.com/client/*]
  - name: agency-stag
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", branch: staging, org_folder: org}
```

An entry whose store branch is not the store's default branch is a **staging entry**. It never matches a repo by itself: its patterns and links are ignored for automatic matching. You choose it on purpose. Use `lore org use` for one repo on this machine, or `LORE_ORG` for one shell or one command.

## The commands

Alice (an agency developer) creates the staging branch once and switches this repo to staging. She links it there (links exist per store branch), adds a staging task, and switches back:

<!-- example: staging#staging-branch -->
```console
# alice, in website
$ lore store init git@github.com:agency/notes --branch staging
✓ wrote lore-store.yaml to github.com/agency/notes (new branch staging, first commit) and pushed
$ lore org use agency-stag
✓ prj_7a67…9504 uses org agency-stag on this machine
  recorded in ~/.lore/config.yaml (machine-only, nothing committed)
$ lore whoami
org        agency-stag  (matched by: lore org use (this machine))
identity   Alice Doe <human:alice@agency.example>   act_2ad3…f891  @alice  (from: org entry (config.yaml))
repo       client repo  (guards on)
project    prj_7a67…9504
remotes    github.com/client/website
stores     <redacted> on branch staging  (org folder <redacted>)
links      known  0 link(s) for this repo on agency-stag's store branch staging (links are per store branch)
personal   local: agency-stag/, everywhere/
$ lore org link agency-stag --project client --yes
This marks github.com/client/website as a CLIENT repo of agency-stag: internal notes default to github.com/agency/notes client/,
and the leak guards turn on for everyone in agency-stag.
  link: project prj_7a67…9504 at github.com/client/website → github.com/agency/notes@staging:client/ (default=false)
✓ github.com/client/website (prj_7a67…9504, github.com/client/website) linked to github.com/agency/notes client/ (created; pushed)
  nothing was written to this repo
$ lore task add 'seed staging with demo data' --to agency-stag
✓ tsk_162d…9666 — seed staging with demo data [medium] → [agency-stag/client]
lore live: (live, pushed)
$ lore task list
○ tsk_162d…9666 seed staging with demo data [medium]  [agency-stag/client]
$ lore org use agency
✓ prj_7a67…9504 uses org agency on this machine
  recorded in ~/.lore/config.yaml (machine-only, nothing committed)
$ lore task list
(no tasks)
$ LORE_ORG=agency-stag lore task list
○ tsk_162d…9666 seed staging with demo data [medium]  [agency-stag/client]
$ git ls-remote https://github.com/agency/notes
601d2850ffd0	HEAD
601d2850ffd0	refs/heads/main
86dbae476bd6	refs/heads/staging
9b40ce71af28	refs/lore/live/staging
```
<!-- /example -->

With `agency` chosen again, the staging task is gone from the list. `LORE_ORG=agency-stag` shows it for one command. On the remote, the store has a `staging` branch and its own live ref `refs/lore/live/staging` (a hidden list that is not a branch), next to `main`.

## What happened

```text
 github.com/agency/notes
   refs/heads/main          ◀── agency       (normal work; matched by link or pattern)
   refs/lore/live/main
   refs/heads/staging       ◀── agency-stag  (only by lore org use / LORE_ORG)
   refs/lore/live/staging
 ~/.lore/clones/github.com/agency/notes.git   one bare clone serves both entries
```

## Pitfalls

- **The branch must exist first.** Linking or writing to a staging entry whose branch does not exist is refused, with a hint to run `lore store init <url> --branch staging`. A typo never creates a new lore silently. See [Troubleshooting](../troubleshooting.md#store-branch-does-not-exist).
- **Links, roster and global project are per branch.** The roster is the list of people and agents in the org. A staging entry needs its own `lore org link`, and `lore actor register` if you want `@handles` there. With a staging entry chosen, `lore whoami` counts only that branch's links: `0 link(s) for this repo on agency-stag's store branch staging (links are per store branch)` until you link it.
- **A branch name may not contain `/`** (`release/2026` is refused by `lore config check`), so `refs/lore/live/<branch>` names can never clash.
- `lore org use` is saved in `repo_orgs:` of your own `~/.lore/config.yaml`. Nothing is committed, and teammates are not affected.
- To move rows between staging and main, merge the store branches yourself with git. Lore's merge driver (a small helper git calls to combine two edits of the same note) settles the row files.
