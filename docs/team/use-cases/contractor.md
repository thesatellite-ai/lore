# Contractor limited to one client

## The situation

Northwind hires a contractor, Casey (`cont1`), to work only on Acme. Casey must see Northwind's company rules and Acme's internal notes, and nothing about any other client.

GitHub cannot limit read access to a folder inside a repo. So the only way to keep one client's notes from a contractor is to keep them in a separate repo: **one store per client**.

## The config

Northwind splits its stores: a company store (`org_folder: "."` means the store's root project is the org folder, with the roster and company rules), and one store per client. Alice's entry lists them all:

```yaml
orgs:
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore-org", org_folder: "."}
      - {url: "git@github.com:northwind/lore-acme"}
      - {url: "git@github.com:northwind/lore-globex"}
    repos: [github.com/acme/*, github.com/globex/*]
```

Casey's entry lists only the company store and Acme's store, and GitHub gives Casey access to only those two repos:

```yaml
orgs:
  - name: northwind
    identity: {key: "human:cont1@northwind.example", name: "Casey Contractor", handle: cont1}
    stores:
      - {url: "git@github.com:northwind/lore-org", org_folder: "."}
      - {url: "git@github.com:northwind/lore-acme"}
    repos: [github.com/acme/app]
```

## The commands

Alice creates the three stores, links `acme/app` to the Acme store with `--store`, and writes one Acme note and one company rule (`--to northwind/.` is the company store's root project):

<!-- example: contractor#one-store-per-client -->
```console
# alice, in ~
$ lore store init git@github.com:northwind/lore-org && lore store init git@github.com:northwind/lore-acme && lore store init git@github.com:northwind/lore-globex
✓ wrote lore-store.yaml to github.com/northwind/lore-org (new branch main, first commit) and pushed
✓ wrote lore-store.yaml to github.com/northwind/lore-acme (new branch main, first commit) and pushed
✓ wrote lore-store.yaml to github.com/northwind/lore-globex (new branch main, first commit) and pushed

# alice, in acme-app
$ lore org link northwind --project acme --store git@github.com:northwind/lore-acme --yes
This marks github.com/acme/app as a CLIENT repo of northwind: internal notes default to github.com/northwind/lore-acme acme/,
and the leak guards turn on for everyone in northwind.
  link: project prj_7a67…9504 at github.com/acme/app → github.com/northwind/lore-acme@main:acme/ (default=false)
✓ github.com/acme/app (prj_7a67…9504, github.com/acme/app) linked to github.com/northwind/lore-acme acme/ (created; pushed)
  nothing was written to this repo
$ lore actor register
✓ act_2ad3…f891 Alice Doe (@alice) registered in northwind ./
lore: [northwind/.] 3 file(s) pushed
$ lore memory add --body 'the Acme staging database is restored every Sunday night'
✓ mem_162d…9666 mem_162d…9666 [northwind/acme]
  filed: defaults.client_repos.memory = northwind (a kind not listed goes to the linked project) → the linked project acme/
  scope: [master]
lore: [northwind/acme] 2 file(s) pushed
$ lore rule add --to northwind/. --body 'every client estimate is reviewed by two people before it is sent'
✓ rul_54fe…28f2 rul_54fe…28f2 [northwind/.]
  filed: --to northwind/.
  scope: [master] severity: must
lore: [northwind/.] 1 file(s) pushed
```
<!-- /example -->

Casey, in the same client repo, sees the link, the Acme note and the company stores, and has clones of only the two stores in the config:

<!-- example: contractor#contractor-view -->
```console
# cont1, in acme-app
$ lore whoami
org        northwind  (matched by: link in github.com/northwind/lore-acme acme/, project id + remote)
identity   Casey Contractor <human:cont1@northwind.example>   act_05e9…4603  @cont1  (from: org entry (config.yaml))
repo       client repo  (guards on)
project    prj_7a67…9504
remotes    github.com/acme/app
stores     git@github.com:northwind/lore-org on branch main  (org folder .)
           git@github.com:northwind/lore-acme on branch main
internal   github.com/northwind/lore-acme acme/
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   local: northwind/, everywhere/
$ lore actor register
✓ act_05e9…4603 Casey Contractor (@cont1) registered in northwind ./
lore: [northwind/.] 1 file(s) pushed
$ lore memory list
mem_162d…9666  the Acme staging database is restored every Sunday night  [northwind/acme]

# cont1, in ~
$ lore store show
<redacted>
  entries   northwind   branches main
  clone     /home/cont1/.lore/clones/github.com/northwind/lore-org.git (bare)   default branch main   last fetch 2026-11-02T10:00:30Z
<redacted>
  entries   northwind   branches main
  clone     /home/cont1/.lore/clones/github.com/northwind/lore-acme.git (bare)   default branch main   last fetch 2026-11-02T10:00:30Z
$ ls ~/.lore/clones/github.com/northwind
lore-acme.git
lore-org.git
```
<!-- /example -->

## What happened

```text
 northwind/lore-org     (.)    roster, company rules        Alice ✓   Casey ✓
 northwind/lore-acme    acme/  Acme's internal notes        Alice ✓   Casey ✓
 northwind/lore-globex  globex/ Globex's internal notes     Alice ✓   Casey ✗ (not in config, no GitHub access)
```

## Gotchas

- **GitHub permissions are the access control.** Leaving a store out of Casey's config is convenience; removing Casey's access to that repo is what actually keeps it out of reach.
- **Folder names are unique across an org's stores**, and exactly one store sets `org_folder`.
- **Offboarding** is removing two repo permissions, then `lore task revoke` / `lore task reassign` for their open work and `lore actor deactivate @cont1` (the roster row stays, marked inactive, because old rows point at it). Copies Casey already fetched stay on Casey's machine, like any git clone.
- The three layouts (one store for everything, one per client, or a mix) are all plain config; the commands do not change. One store per client is the recommended layout once contractors or NDAs exist.
