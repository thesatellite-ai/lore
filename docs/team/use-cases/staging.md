# Same store, separate staging

## The situation

Northwind wants a staging lore: test notes and staging-only tasks that must never mix with the real ones. It does not want a second store repo.

## The config

A second org entry on the **same store URL** with another store branch, `staging`. Each store branch is a separate lore: lore never moves rows between store branches.

```yaml
orgs:
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore", org_folder: org}
    repos: [github.com/acme/*]
  - name: northwind-stag
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore", branch: staging, org_folder: org}
```

An entry whose store branch is not the store's default branch is a **staging entry**. It never matches a repo by itself (its patterns and links are ignored for automatic matching); you choose it on purpose, with `lore org use` for one repo on this machine, or `LORE_ORG` for one shell or one command.

## The commands

Create the staging branch once, switch this repo to staging, link it there (links exist per store branch), add a staging task, and switch back:

<!-- example: staging#staging-branch -->
```console
# alice, in acme-app
$ lore store init git@github.com:northwind/lore --branch staging
✓ wrote lore-store.yaml to github.com/northwind/lore (new branch staging, first commit) and pushed
$ lore org use northwind-stag
✓ prj_01a1…5996 uses org northwind-stag on this machine
  recorded in /home/alice/.lore/config.yaml (machine-only, nothing committed)
$ lore whoami
org        northwind-stag  (matched by: lore org use (this machine))
identity   Alice Doe <human:alice@northwind.example>   act_0263…5705  @alice  (from: org entry (config.yaml))
repo       client repo  (guards on)
project    prj_01a1…5996
remotes    github.com/acme/app
stores     <redacted> on branch staging  (org folder <redacted>)
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   local: northwind-stag/, everywhere/
$ lore org link northwind-stag --project acme --yes
This marks github.com/acme/app as a CLIENT repo of northwind-stag: internal notes default to github.com/northwind/lore acme/,
and the leak guards turn on for everyone in northwind-stag.
  link: project prj_01a1…5996 at github.com/acme/app → github.com/northwind/lore@staging:acme/ (default=false)
✓ github.com/acme/app (prj_01a1…5996, github.com/acme/app) linked to github.com/northwind/lore acme/ (created; pushed)
  nothing was written to this repo
$ lore task add 'seed staging with demo data' --to northwind-stag
✓ tsk_01a1…1ef3 tsk_01a1…1ef3 — seed staging with demo data [medium] → [northwind-stag/acme]
lore live: (live, pushed)
$ lore task list
(no tasks)
tsk_01a1…1ef3  seed staging with demo data  todo  [northwind-stag/acme]
$ lore org use northwind
✓ prj_01a1…5996 uses org northwind on this machine
  recorded in /home/alice/.lore/config.yaml (machine-only, nothing committed)
$ lore task list
(no tasks)
$ LORE_ORG=northwind-stag lore task list
(no tasks)
tsk_01a1…1ef3  seed staging with demo data  todo  [northwind-stag/acme]
$ git ls-remote https://github.com/northwind/lore
dfe8b2e3a581	HEAD
dfe8b2e3a581	refs/heads/main
db7069436190	refs/heads/staging
e55edb8481a2	refs/lore/live/staging
```
<!-- /example -->

With `northwind` chosen again, the staging task is gone from the list; `LORE_ORG=northwind-stag` shows it for one command. On the remote, the store has a `staging` branch and its own live ref `refs/lore/live/staging`, next to `main`.

## What happened

```text
 github.com/northwind/lore
   refs/heads/main          ◀── northwind       (normal work; matched by link or pattern)
   refs/lore/live/main
   refs/heads/staging       ◀── northwind-stag  (only by lore org use / LORE_ORG)
   refs/lore/live/staging
 ~/.lore/clones/github.com/northwind/lore.git   one bare clone serves both entries
```

## Gotchas

- **The branch must exist first.** Linking or writing to a staging entry whose branch does not exist is refused with a hint to run `lore store init <url> --branch staging`; a typo never creates a new lore silently. See [Troubleshooting](../troubleshooting.md#store-branch-does-not-exist).
- **Links, roster and global project are per branch.** A staging entry needs its own `lore org link` (and `lore actor register` if you want `@handles` there).
- **A branch name may not contain `/`** (`release/2026` is refused by `lore config check`), so `refs/lore/live/<branch>` names can never clash.
- `lore org use` is saved in `repo_orgs:` of your own `~/.lore/config.yaml`; nothing is committed, and teammates are not affected.
- To move rows between staging and main, merge the store branches yourself with git; lore's merge driver settles the row files.
