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
✓ created store agency (sto_7a67…9504) at git@github.com:agency/notes; recorded in .lore/stores.yaml; commit it to share
✓ created store client (sto_2ad3…f891) at git@github.com:agency/notes-client; recorded in .lore/stores.yaml; commit it to share
✓ created store client2 (sto_162d…9666) at git@github.com:agency/notes-client2; recorded in .lore/stores.yaml; commit it to share

# alice, in website
$ lore org link agency --project client --store git@github.com:agency/notes-client --yes
This marks github.com/client/website as a CLIENT repo of agency: internal notes default to github.com/agency/notes-client client/,
and the leak guards turn on for everyone in agency.
  link: project prj_54fe…28f2 at github.com/client/website → github.com/agency/notes-client@main:client/ (default=false)
✓ github.com/client/website (prj_54fe…28f2) linked to github.com/agency/notes-client client/ (created; pushed)
  nothing was written to this repo
$ lore actor register
✓ act_05e9…4603 Alice Doe (@alice) registered in agency ./
$ lore memory add --body 'the client staging database is restored every Sunday night'
✓ mem_c0f3…b5b8 [agency/client]
  filed: defaults.client_repos.memory = agency (a kind not listed goes to the linked project) → the linked project client/
  scope: [master]
$ lore rule add --to agency/. --body 'every client estimate is reviewed by two people before it is sent'
✓ rul_76bd…3f10 [agency/.]
  scope: [master] severity: must
```
<!-- /example -->

Bob, in the same client repo, sees the link, the client's note and the company stores. He has clones of only the two stores in his config:

<!-- example: contractor#contractor-view -->
```console
# bob, in website
$ lore whoami
org        agency  (matched by: link in github.com/agency/notes-client client/, project id + remote)
identity   Bob Roe <human:bob@agency.example>   act_d431…313a  @bob  (from: org entry (config.yaml orgs[agency].identity))
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
✓ act_d431…313a Bob Roe (@bob) registered in agency ./
$ lore memory list
mem_c0f3…b5b8  the client staging database is restored every Sunday night  [agency/client]

# bob, in ~
$ lore store show
git@github.com:agency/notes
  entries   agency   branches main
  clone     ~/.lore/clones-v017/github.com/agency/notes.git (bare)   default branch main   last fetch 2026-11-02T10:00:30Z
git@github.com:agency/notes-client
  entries   agency   branches main
  clone     ~/.lore/clones-v017/github.com/agency/notes-client.git (bare)   default branch main   last fetch 2026-11-02T10:00:30Z
$ ls ~/.lore/clones/github.com/agency
notes
notes-client
notes-client.git
notes.git
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
