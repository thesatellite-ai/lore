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
org        agency  (matched by: link in github.com/agency/notes client/, project id + remote)
identity   Alice Doe <human:alice@agency.example>   act_7a67…9504  @alice  (from: org entry (config.yaml orgs[agency].identity))
repo       client repo  (guards on)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_2ad3…f891
remotes    github.com/client/website
stores     git@github.com:agency/notes on branch main  (org folder org)
internal   github.com/agency/notes client/
links      known  1 link(s) for this repo (stores of 2 entries scanned)
personal   me -> ~/.lore/personal

# alice, in client3-api
$ lore whoami
org        client3  (matched by: own pattern github.com/client3/* of client3)
identity   Alice Doe <human:alice@client3.example>   act_162d…9666  @alice  (from: org entry (config.yaml orgs[client3].identity))
repo       own repo  (no client guards)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_54fe…28f2
remotes    github.com/client3/api
stores     git@github.com:client3/notes on branch main  (org folder org)
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   me -> ~/.lore/personal

# alice, in forked-tool
$ lore whoami
org        none  (matched by: nothing matched)   hint: lore org use --entry <entry> (agency, client3)
identity   Alice Doe <human:alice@example.com>   act_05e9…4603  (from: top-level identity (config.yaml))
repo       no org  (fallback identity)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_c0f3…b5b8
remotes    github.com/someone/tool
links      known  0 link(s) for this repo (stores of 2 entries scanned)
personal   me -> ~/.lore/personal
```
<!-- /example -->

Each repo resolves to one org, with that org's identity and actor id. The unrelated repo resolves to none and uses the top-level identity, never a company one. `lore whoami --explain` prints every org rule with hit or miss. Its output for `forked-tool` is shown in [Concepts](../concepts.md#orgs-identity-and-whoami).

`whoami` names the store URLs and folders the same way on a terminal, in a script, in a CI job or in an agent's tool call; only a credential inside a URL is shown as `<redacted>`.

Personal notes follow the same split. A note written in an agency repo is filed under `agency/` and loads only in agency repos:

<!-- example: two-orgs#personal-notes-split -->
```console
# alice, in website
$ lore memory add --body 'ask the client PM before touching the payment flow' --to me
lore: wrote ~/.lore/personal/agency/actors/act_7a67…9504.json (store me)
lore: wrote ~/.lore/personal/agency/memories/mem_76bd…3f10.json (store me)
lore: wrote ~/.lore/personal/agency/machines/mch_d431…313a.json (store me)
✓ mem_76bd…3f10 [me/agency]
  scope: [master]

# alice, in client3-api
$ lore memory add --body 'client3 deploys only on Tuesdays' --to me
lore memory add: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/client3-api/.git/config; hook blocks in ~/work/client3-api/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
lore: wrote ~/.lore/personal/client3/actors/act_162d…9666.json (store me)
lore: wrote ~/.lore/personal/client3/memories/mem_b4f4…a5b0.json (store me)
lore: wrote ~/.lore/personal/client3/machines/mch_d431…313a.json (store me)
✓ mem_b4f4…a5b0 [me/client3]
  scope: [master]

# alice, in website
$ lore memory list
mem_76bd…3f10 [retrieved] ask the client PM before touching the payment flow [me/agency]

# alice, in client3-api
$ lore memory list
mem_b4f4…a5b0 [retrieved] client3 deploys only on Tuesdays [me/client3]

# alice, in forked-tool
$ lore memory list
lore memory list: git is not set up for lore's row files in this clone: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/forked-tool/.git/config; hook blocks in ~/work/forked-tool/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
(no memories)
```
<!-- /example -->

## What happened

```text
 ~/work/website      → agency   alice@agency.example   agency/notes   personal: me (notes filed under me/agency)
 ~/work/client3-api  → client3  alice@client3.example  client3/notes  personal: me (notes filed under me/client3)
 ~/work/forked-tool  → none     alice@example.com      (no store)     personal: me
```

## Pitfalls

- **Never two orgs in one repo.** Two entries may match a repo at the same rule (say both list `github.com/client/*`). Then lore refuses org-scoped commands with `E_ORG_AMBIGUOUS` until you choose with `lore org use --entry <entry>`. It never guesses. See [Troubleshooting](../troubleshooting.md#e_org_ambiguous).
- **Set a top-level `identity`** once you have orgs. Without it, lore falls back to the git email chain, but skips any value equal to an org identity. So a personal repo is never stamped with a company identity. Lore prints a hint to set one.
- **Two identities, two actors.** The same person in two orgs is two roster rows (two entries on two lists of people and agents), never linked automatically.
- **Agents for two orgs** should run as different OS users or in different containers. Lore keeps orgs apart in what it loads. But an agent with plain file access could read another org's clone in `~/.lore/stores`.

## Related

- [Contractor limited to one client](contractor.md).
- [Personal notes](personal-notes.md).
- [Guided setup](../guided-setup.md) and [Setups from the command line](../setup-cli.md): one setup per company, chosen by folder.
- [Identity and stores](../identity-and-stores.md): `lore identity set --org`.
