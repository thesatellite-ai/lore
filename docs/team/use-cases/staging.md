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

Alice (an agency developer) creates the staging branch once (from her `stores` folder, never from the client's repo) and switches this repo to staging. She links it there (links exist per store branch), adds a staging task, and switches back:

<!-- example: staging#staging-branch -->
```console
# alice, in stores
$ lore store init --name agency_stag --url 'git@github.com:agency/notes?branch=staging' --yes
lore: added .lore/lore.db, .lore/lore.db-shm, .lore/lore.db-wal, .lore/lore.db.broken.*, .lore/state/, .lore/backups/, .lore/lock to this clone's .git/info/exclude (never committed) so lore's local files stay out of git status; this repo holds no lore data, so lore changes none of its tracked files (`lore where` lists everything lore keeps here)
lore store init: store clone ~/.lore/stores/agency/notes@staging: git user set to Alice Doe <alice@agency.example> (your identity in org agency)
✓ created store agency_stag (sto_7a67…9504) at git@github.com:agency/notes?branch=staging; recorded in .lore/stores.yaml; commit it to share

# alice, in website
$ lore org use --entry agency-stag
✓ prj_2ad3…f891 uses org agency-stag on this machine
  recorded in ~/.lore/config.yaml (machine-only, nothing committed)
$ lore whoami
org        agency-stag  (matched by: lore org use (this machine))
identity   Alice Doe <human:alice@agency.example>   act_162d…9666  @alice  (from: org entry (config.yaml orgs[agency-stag].identity))
repo       client repo  (guards on)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_2ad3…f891
remotes    github.com/client/website
stores     git@github.com:agency/notes on branch staging  (org folder org)
links      known  0 link(s) for this repo on agency-stag's store branch staging (links are per store branch)
personal   me -> ~/.lore/personal
$ lore org link --org agency-stag --project client --yes
This marks github.com/client/website as a CLIENT repo of agency-stag: internal notes default to github.com/agency/notes client/,
and the leak guards turn on for everyone in agency-stag.
  link: project prj_2ad3…f891 at github.com/client/website → github.com/agency/notes@staging:client/ (default=false)
✓ github.com/client/website (prj_2ad3…f891) linked to github.com/agency/notes client/ (created; committed in the local clone; `lore sync` sends it)
  nothing was written to this repo
$ lore task add --title 'seed staging with demo data' --to agency-stag
lore: committed locally to store agency-stag; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes@staging/client/actors/act_162d…9666.json (store agency-stag)
lore: wrote ~/.lore/stores/agency/notes@staging/client/task_lists/tlt_54fe…28f2.json (store agency-stag)
lore: wrote ~/.lore/stores/agency/notes@staging/client/tasks/tsk_05e9…4603.json (store agency-stag)
✓ tsk_05e9…4603 — seed staging with demo data [medium] → [agency-stag/client]
  created tasklist tlt_54fe…28f2 "default" for tasks added without --tasklist
$ lore task list
○ tsk_05e9…4603 seed staging with demo data [medium]  [agency-stag/client]
$ lore org use --entry agency
✓ prj_2ad3…f891 uses org agency on this machine
  recorded in ~/.lore/config.yaml (machine-only, nothing committed)
$ lore task list
(no tasks)
$ LORE_ORG=agency-stag lore task list
○ tsk_05e9…4603 seed staging with demo data [medium]  [agency-stag/client]
$ git ls-remote https://github.com/agency/notes
601d2850ffd0	HEAD
601d2850ffd0	refs/heads/main
86dbae476bd6	refs/heads/staging
```
<!-- /example -->

With `agency` chosen again, the staging task is gone from the list. `LORE_ORG=agency-stag` shows it for one command. On the remote, the store has a `staging` branch next to `main`; each store branch is its own lore, written by plain commits (a store has no live ref in 0.1.18).

## What happened

```text
 github.com/agency/notes
   refs/heads/main          ◀── agency       (normal work; matched by link or pattern)
   refs/heads/staging       ◀── agency-stag  (only by lore org use / LORE_ORG)
 ~/.lore/stores/agency/notes           main's clone (agency)
 ~/.lore/stores/agency/notes@staging   staging's own clone (agency-stag)
```

## Pitfalls

- **The branch must exist first.** Linking or writing to a staging entry whose branch does not exist is refused, with a hint to create it on purpose with `lore store init --name <name> --url '<url>?branch=staging'`; `lore store add --name <name> --url <url> --branch staging` attaches a branch that already exists. A typo never creates a new lore silently. See [Troubleshooting](../troubleshooting.md#store-branch-does-not-exist).
- **Links, roster and global project are per branch.** The roster is the list of people and agents in the org. A staging entry needs its own `lore org link`, and `lore actor register` if you want `@handles` there. With a staging entry chosen, `lore whoami` counts only that branch's links: `0 link(s) for this repo on agency-stag's store branch staging (links are per store branch)` until you link it.
- **A branch name may not contain `/`** (`release/2026` is refused by `lore config check`), so a store branch name never clashes with lore's own refs.
- `lore org use` is saved in `repo_orgs:` of your own `~/.lore/config.yaml`. Nothing is committed, and teammates are not affected.
- To move rows between staging and main, merge the store branches yourself with git. Lore's merge driver (a small helper git calls to combine two edits of the same note) settles the row files.

## Related

- [Agency with a client repo](agency-client-repo.md): the real org this staging copy mirrors.
- [Setups from the command line](../setup-cli.md): a separate setup per folder.
- [Troubleshooting](../troubleshooting.md).
