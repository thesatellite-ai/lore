# One person in two companies

**In short:** Alice (an agency developer) works for two companies on one laptop, with a different email at each. Lore picks the right identity and the right private notes from the repo she is in. So one company never sees the other's notes.

## The situation

Alice works for the agency and for client 3 on the same laptop, with a different email at each. In the agency's repos she must be her agency self and see the agency's notes. In client 3's repos she must be her client 3 self. Her own side projects belong to neither.

## The config

Two org entries (one company setup each), each with its own identity and store (a private notes repo). A top-level identity covers everything else. Each identity key is an email address with `human:` in front. So Alice is a different person to each company:

```yaml
identity:
  key: human:alice@example.com
  name: Alice Doe
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: org}
    own:   [github.com/agency/*]
    repos: [github.com/client/*]
  - name: client3
    identity: {key: "human:alice@client3.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:client3/notes", org_folder: org}
    own: [github.com/client3/*]
```

## The commands

`lore whoami` in three repos: the client's (linked in the agency's store), client 3's own API, and an unrelated tool:

<!-- example: two-orgs#whoami-per-repo only=1,2,3 -->
```console
# alice, in website
$ lore whoami
org        agency  (matched by: link in <redacted>, project id + remote)
identity   Alice Doe <human:alice@agency.example>   act_7a67…9504  @alice  (from: org entry (config.yaml))
repo       client repo  (guards on)
machine    mch_fac0df3a  (mch_fac0df3a618f3d99d93273abc59a9ece0b7cedbb24ea7cd8e8af24a257cfe1bc)
agent      none  (no coding agent detected)
project    prj_2ad3…f891
remotes    github.com/client/website
stores     <redacted> on branch main  (org folder <redacted>)
internal   <redacted> <redacted>/
links      known  1 link(s) for this repo (stores of 2 entries scanned)
personal   local: agency/, everywhere/

# alice, in client3-api
$ lore whoami
org        client3  (matched by: own pattern github.com/client3/* of client3)
identity   Alice Doe <human:alice@client3.example>   act_162d…9666  @alice  (from: org entry (config.yaml))
repo       own repo  (no client guards)
machine    mch_fac0df3a  (mch_fac0df3a618f3d99d93273abc59a9ece0b7cedbb24ea7cd8e8af24a257cfe1bc)
agent      none  (no coding agent detected)
project    prj_54fe…28f2
remotes    github.com/client3/api
stores     <redacted> on branch main  (org folder <redacted>)
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   local: client3/, everywhere/

# alice, in forked-tool
$ lore whoami
org        none  (matched by: nothing matched)   hint: lore org use <entry> (agency, client3)
identity   Alice Doe <human:alice@example.com>   act_05e9…4603  (from: top-level identity (config.yaml))
repo       no org  (fallback identity, personal everywhere/ notes only)
machine    mch_fac0df3a  (mch_fac0df3a618f3d99d93273abc59a9ece0b7cedbb24ea7cd8e8af24a257cfe1bc)
agent      none  (no coding agent detected)
project    prj_c0f3…b5b8
remotes    github.com/someone/tool
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   local: everywhere/
```
<!-- /example -->

Each repo resolves to one org, with that org's identity and actor id. The unrelated repo resolves to none and uses the top-level identity, never a company one. `lore whoami --explain` prints every org rule with hit or miss. Its output for `forked-tool` is shown in [Concepts](../concepts.md#orgs-identity-and-whoami).

Without a terminal (in a script, a CI job, or an agent's tool call), `whoami` hides store URLs and internal folder names as `<redacted>`. That is why the output above shows them that way. On your own terminal they are printed. `--share` prints them on a terminal on purpose.

Personal notes follow the same split. A note written in an agency repo is filed under `agency/` and loads only in agency repos:

<!-- example: two-orgs#personal-notes-split -->
```console
# alice, in website
$ lore memory add --body 'ask the client PM before touching the payment flow' --to me
✓ mem_76bd…3f10 [me/agency]
  scope: [master]

# alice, in client3-api
$ lore memory add --body 'client3 deploys only on Tuesdays' --to me
✓ mem_d431…313a [me/client3]
  scope: [master]

# alice, in website
$ lore memory list
mem_76bd…3f10  ask the client PM before touching the payment flow  [me/agency]

# alice, in client3-api
$ lore memory list
mem_d431…313a  client3 deploys only on Tuesdays  [me/client3]

# alice, in forked-tool
$ lore memory list
(no memories)
```
<!-- /example -->

## What happened

```text
 ~/work/website      → agency   alice@agency.example   agency/notes   personal: agency/, everywhere/
 ~/work/client3-api  → client3  alice@client3.example  client3/notes  personal: client3/, everywhere/
 ~/work/forked-tool  → none     alice@example.com      (no store)     personal: everywhere/
```

## Pitfalls

- **Never two orgs in one repo.** Two entries may match a repo at the same rule (say both list `github.com/client/*`). Then lore refuses org-scoped commands with `E_ORG_AMBIGUOUS` until you choose with `lore org use <entry>`. It never guesses. See [Troubleshooting](../troubleshooting.md#e_org_ambiguous).
- **Set a top-level `identity`** once you have orgs. Without it, lore falls back to the git email chain, but skips any value equal to an org identity. So a personal repo is never stamped with a company identity. Lore prints a hint to set one.
- **Two identities, two actors.** The same person in two orgs is two roster rows (two entries on two lists of people and agents), never linked automatically.
- **Agents for two orgs** should run as different OS users or in different containers. Lore keeps orgs apart in what it loads. But an agent with plain file access could read another org's clone in `~/.lore/clones`.
