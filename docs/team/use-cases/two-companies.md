# One person in two companies

## The situation

Alice works for Northwind and for Beta Labs on the same laptop, with a different email at each. In Northwind's repos she must be her Northwind self and see Northwind's notes; in Beta Labs' repos, her Beta Labs self. Her own side projects belong to neither.

## The config

Two org entries, each with its own identity and store, and a top-level identity for everything else:

```yaml
identity:
  key: human:alice@example.com
  name: Alice Doe
personal:
  store: local
orgs:
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore", org_folder: org}
    own:   [github.com/northwind/*]
    repos: [github.com/acme/*]
  - name: betalabs
    identity: {key: "human:alice@betalabs.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:betalabs/lore", org_folder: org}
    own: [github.com/betalabs/*]
```

## The commands

`lore whoami` in three repos: Acme's (linked in Northwind's store), Beta Labs' own API, and an unrelated tool:

<!-- example: two-orgs#whoami-per-repo only=1,2,3 -->
```console
# alice, in acme-app
$ lore whoami
org        northwind  (matched by: link in <redacted>, project id + remote)
identity   Alice Doe <human:alice@northwind.example>   act_0263…5705  @alice  (from: org entry (config.yaml))
repo       client repo  (guards on)
project    prj_01a1…7ef2
remotes    github.com/acme/app
stores     <redacted> on branch main  (org folder <redacted>)
internal   <redacted> <redacted>/
links      known  1 link(s) for this repo (stores of 2 entries scanned)
personal   local: northwind/, everywhere/

# alice, in betalabs-api
$ lore whoami
org        betalabs  (matched by: own pattern github.com/betalabs/* of betalabs)
identity   Alice Doe <human:alice@betalabs.example>   act_e248…9854  @alice  (from: org entry (config.yaml))
repo       own repo  (no client guards)
project    prj_01a1…dc9a
remotes    github.com/betalabs/api
stores     <redacted> on branch main  (org folder <redacted>)
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   local: betalabs/, everywhere/

# alice, in forked-tool
$ lore whoami
org        none  (matched by: nothing matched)   hint: lore org use <entry> (northwind, betalabs)
identity   Alice Doe <human:alice@example.com>   act_3686…c40e  (from: top-level identity (config.yaml))
repo       no org  (fallback identity, personal everywhere/ notes only)
project    prj_01a1…d46a
remotes    github.com/someone/tool
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   local: everywhere/
```
<!-- /example -->

Each repo resolves to one org, with that org's identity and actor id; the unrelated repo resolves to none and uses the top-level identity, never a company one. `lore whoami --explain` prints every org rule with hit or miss; its output for `forked-tool` is shown in [Concepts](../concepts.md#orgs-identity-and-whoami).

Without a terminal (in a script, a CI job, or an agent's tool call) `whoami` hides store URLs and internal folder names as `<redacted>`, which is why the output above shows them that way; on your own terminal they are printed, and `--share` prints them on a terminal on purpose.

Personal notes follow the same split: a note written in a Northwind repo is filed under `northwind/` and loads only in Northwind repos:

<!-- example: two-orgs#personal-notes-split -->
```console
# alice, in acme-app
$ lore memory add --body 'ask the Acme PM before touching the payment flow' --to me
✓ mem_01a1…312f mem_01a1…312f [me]
  filed: --to me → personal northwind/
  scope: [master]

# alice, in betalabs-api
$ lore memory add --body 'betalabs deploys only on Tuesdays' --to me
✓ mem_01a1…3c57 mem_01a1…3c57 [me]
  filed: --to me → personal betalabs/
  scope: [master]

# alice, in acme-app
$ lore memory list
mem_01a1…312f  ask the Acme PM before touching the payment flow  [me]

# alice, in betalabs-api
$ lore memory list
mem_01a1…3c57  betalabs deploys only on Tuesdays  [me]

# alice, in forked-tool
$ lore memory list
(no memories)
```
<!-- /example -->

## What happened

```text
 ~/work/acme-app      → northwind   alice@northwind.example   northwind/lore      personal: northwind/, everywhere/
 ~/work/betalabs-api  → betalabs    alice@betalabs.example    betalabs/lore       personal: betalabs/, everywhere/
 ~/work/forked-tool   → none        alice@example.com         (no store)          personal: everywhere/
```

## Gotchas

- **Never two orgs in one repo.** If two entries match a repo at the same rule (say both list `github.com/acme/*`), lore refuses org-scoped commands with `E_ORG_AMBIGUOUS` until you choose with `lore org use <entry>`; it never guesses. See [Troubleshooting](../troubleshooting.md#e_org_ambiguous).
- **Set a top-level `identity`** once you have orgs. Without it, lore falls back to the git email chain but skips any value equal to an org identity, so a personal repo is never stamped with a company identity; it prints a hint to set one.
- **Two identities, two actors.** The same person in two orgs is two roster rows, never linked automatically.
- **Agents for two orgs** should run as different OS users or in different containers: lore keeps orgs apart in what it loads, but an agent with plain file access could read another org's clone in `~/.lore/clones`.
