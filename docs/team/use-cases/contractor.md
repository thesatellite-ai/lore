# Contractor limited to one client

**In short:** the agency hires Bob, a contractor, to work only for the client. Bob gets the company rules and that one client's private notes. He gets nothing about other clients, because each client's notes sit in their own repo, and GitHub decides who can open it.

## The situation

The agency hires Bob (a contractor, handle `bob`) to work only on the client. Bob must see the agency's company rules and the client's internal notes. He must see nothing about any other client.

GitHub cannot limit read access to a folder inside a repo. So the only way to keep one client's notes from a contractor is to keep them in a separate repo. A store is such a repo: a separate private repo that only the agency can open, like a locked filing cabinet. Here the agency uses **one store per client**.

## The config

The agency splits its stores. There is one company store, and one store per client. In the company store, `org_folder: "."` means the store's root project is the org folder: it holds the roster (the list of people and agents) and the company rules. The entry of Alice (an agency developer) lists every store:

```yaml
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: "."}
      - {url: "git@github.com:agency/notes-client"}
      - {url: "git@github.com:agency/notes-client2"}
    repos: [github.com/client/*, github.com/client2/*]
```

Bob's entry lists only the company store and the client's store. GitHub gives Bob access to only those two repos:

```yaml
orgs:
  - name: agency
    identity: {key: "human:bob@agency.example", name: "Bob Roe", handle: bob}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: "."}
      - {url: "git@github.com:agency/notes-client"}
    repos: [github.com/client/website]
```

## The commands

Alice creates the three stores (from a plain folder of hers, `stores`, which attaches them by name; never from the client's repo). She links `client/website` to the client's store with `--store`. Then she writes one client note and one company rule (`--to agency/.` is the company store's root project):

<!-- example: contractor#one-store-per-client -->
```console
# alice, in stores
$ lore store init agency git@github.com:agency/notes --yes && lore store init client git@github.com:agency/notes-client --yes && lore store init client2 git@github.com:agency/notes-client2 --yes
lore store init: store clone ~/.lore/stores/agency/notes: git user set to Alice Doe <alice@agency.example> (your identity in org agency)
lore store init: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/stores/.git/config; hook blocks in ~/work/stores/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
✓ created store agency (sto_7a67…9504) at git@github.com:agency/notes; recorded in .lore/stores.yaml; commit it to share
lore: added .lore/lore.db, .lore/lore.db-shm, .lore/lore.db-wal, .lore/lore.db.broken.*, .lore/state/, .lore/backups/, .lore/lock to this clone's .git/info/exclude (never committed) so lore's local files stay out of git status; this repo holds no lore data, so lore changes none of its tracked files (`lore where` lists everything lore keeps here)
lore store init: store clone ~/.lore/stores/agency/notes-client: git user set to Alice Doe <alice@agency.example> (your identity in org agency)
✓ created store client (sto_2ad3…f891) at git@github.com:agency/notes-client; recorded in .lore/stores.yaml; commit it to share
lore store init: store clone ~/.lore/stores/agency/notes-client2: git user set to Alice Doe <alice@agency.example> (your identity in org agency)
✓ created store client2 (sto_162d…9666) at git@github.com:agency/notes-client2; recorded in .lore/stores.yaml; commit it to share

# alice, in website
$ lore org link agency --project client --store git@github.com:agency/notes-client --yes
lore org link: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/website/.git/config; hook blocks in ~/work/website/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: pushed store github.com/agency/notes-client: commit 601d285 to branch main
This marks github.com/client/website as a CLIENT repo of agency: internal notes default to github.com/agency/notes-client client/,
and the leak guards turn on for everyone in agency.
  link: project prj_54fe…28f2 at github.com/client/website → github.com/agency/notes-client@main:client/ (default=false)
✓ github.com/client/website (prj_54fe…28f2) linked to github.com/agency/notes-client client/ (created; pushed)
  nothing was written to this repo
$ lore actor register
lore: pushed store agency/.: commit 86dbae4 to branch main
lore: pushed store agency/.: commit 9b40ce7 to branch main
lore: wrote ~/.lore/stores/agency/notes/_store/projects/prj_05e9…4603.json (store agency/.)
lore: wrote ~/.lore/stores/agency/notes/_store/actors/act_c0f3…b5b8.json (store agency/.)
✓ act_c0f3…b5b8 Alice Doe (@alice) registered in agency ./
$ lore memory add --body 'the client staging database is restored every Sunday night'
lore: pushed store agency: commit 05513aa to branch main
lore: wrote 2 row file(s) in ~/.lore/personal/agency (store me)
lore: wrote 2 row file(s) in ~/.lore/stores/agency/notes-client/client (store agency)
✓ mem_76bd…3f10 [agency/client]
  filed: defaults.client_repos.memory = agency (a kind not listed goes to the linked project) → the linked project client/
  scope: [master]
$ lore rule add --to agency/. --body 'every client estimate is reviewed by two people before it is sent'
lore: pushed store agency/.: commit dfd8dc8 to branch main
lore: wrote ~/.lore/stores/agency/notes/_store/rules/rul_d431…313a.json (store agency/.)
✓ rul_d431…313a [agency/.]
  scope: [master] severity: must
```
<!-- /example -->

Bob, in the same client repo, sees the link, the client's note and the company stores. He has clones of only the two stores in his config:

<!-- example: contractor#contractor-view -->
```console
# bob, in website
$ lore whoami
org        agency  (matched by: link in github.com/agency/notes-client client/, project id + remote)
identity   Bob Roe <human:bob@agency.example>   act_b4f4…a5b0  @bob  (from: org entry (config.yaml orgs[agency].identity))
repo       client repo  (guards on)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_54fe…28f2
remotes    github.com/client/website
stores     git@github.com:agency/notes on branch main  (org folder .)
           git@github.com:agency/notes-client on branch main
internal   github.com/agency/notes-client client/
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   me -> ~/.lore/personal
$ lore actor register
lore actor register: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/website/.git/config; hook blocks in ~/work/website/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore actor register: store clone ~/.lore/stores/agency/notes: git user set to Bob Roe <bob@agency.example> (your identity in org agency)
lore actor register: store clone ~/.lore/stores/agency/notes: set up for plain git (merge driver, pre-commit hook; `lore where` lists them)
lore: pushed store agency/.: commit cf563f3 to branch main
lore: wrote ~/.lore/stores/agency/notes/_store/actors/act_b4f4…a5b0.json (store agency/.)
✓ act_b4f4…a5b0 Bob Roe (@bob) registered in agency ./
$ lore memory list
mem_76bd…3f10 [retrieved] the client staging database is restored every Sunday night [agency/client]

# bob, in ~
$ lore store show
git@github.com:agency/notes
  entries   agency   branches main
  clone     ~/.lore/stores/agency/notes   default branch main   last fetch 2026-11-02T10:00:35Z
git@github.com:agency/notes-client
  entries   agency   branches main
  clone     ~/.lore/stores/agency/notes-client   default branch main   last fetch 2026-11-02T10:00:30Z
$ ls ~/.lore/stores/agency
notes
notes-client
```
<!-- /example -->

## What happened

```text
 agency/notes          (.)       roster, company rules         Alice ✓   Bob ✓
 agency/notes-client   client/   the client's internal notes   Alice ✓   Bob ✓
 agency/notes-client2  client2/  client 2's internal notes     Alice ✓   Bob ✗ (not in config, no GitHub access)
```

## Pitfalls

- **GitHub permissions are the access control.** Leaving a store out of Bob's config is only a convenience. Removing Bob's access to that repo is what actually keeps it out of reach.
- **Folder names are unique across an org's stores**, and exactly one store sets `org_folder`.
- **Offboarding** (when Bob leaves) means removing two repo permissions. Then run `lore task revoke` / `lore task reassign` for his open work and `lore actor deactivate @bob`. The roster row stays, marked inactive, because old rows point at it. Copies Bob already fetched stay on Bob's machine, like any git clone.
- The three layouts (one store for everything, one per client, or a mix) are all plain config. The commands do not change. One store per client is the recommended layout once you have contractors or NDAs (secrecy agreements).

## Related

- [Agency with a client repo](agency-client-repo.md): the same org with one store for everything.
- [One person in two companies](two-companies.md).
- [Team profiles](../profiles.md): contractor stints and offboarding.
- [Identity and stores](../identity-and-stores.md).
