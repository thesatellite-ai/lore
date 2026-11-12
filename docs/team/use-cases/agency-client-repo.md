# Agency with a client repo

**In short:** the agency builds the client's website, and the client can read everything in that repo. The agency's private notes and tasks about the client go to a separate repo that only the agency can open. The shared project rules stay in the client's repo. This page sets that up and shows what each side sees.

## The situation

The agency builds the client's website, `github.com/client/website`. The client can read the whole repo. The agency wants to keep notes ("they approve estimates faster on WhatsApp") and tasks ("send the invoice reminder before the demo") about this project that the client never sees. The real project rules ("Run the tests before you push") stay in the repo, where both sides read them.

Remember the rule behind everything: **hidden is not private**. Anything in the client's repo, on any branch or hidden list, the client can download. So the agency's notes go into a store: a separate private repo that only the agency can open, like a locked filing cabinet next to the client's repo.

## The config

Each agency person has an entry like this in `~/.lore/config.yaml`. This one is for Alice (an agency developer). Bob (another agency developer) has the same with his own identity:

```yaml
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own:   [github.com/agency/*]
    repos: [github.com/client/*, github.com/client2/*, github.com/client3/*]
    defaults:
      client_repos: {task: agency, memory: agency, rule: repo, decision: repo}
```

`repos:` marks the client's repos as client repos. That turns the leak guards on: checks that stop internal notes from reaching the client's repo. `defaults.client_repos` says where each kind of row (note) goes in a client repo when you pass no `--to`. Tasks and memories go to the agency's internal project. Rules and decisions go into the repo.

## The commands

Once per company: turn a new, empty repo into the store. Run `lore store init <name> <url>` in one of the agency's own repos (here `tools`); it attaches the store there by name. Never run it in the client's repo, which must not name the store.

<!-- example: agency#store-init -->
```console
# alice, in tools
$ lore store init agency git@github.com:agency/notes --yes
✓ created store agency (sto_7a67…9504) at git@github.com:agency/notes; recorded in .lore/stores.yaml; commit it to share
```
<!-- /example -->

Once per client repo, anyone at the agency links it to an internal project. Lore asks first, writes the link into the store only, and changes nothing in the client's repo. Then each person registers on the roster (the list of people and agents in the org):

<!-- example: agency#link only=1,2,3,4,5,6 -->
```console
# alice, in website
$ lore whoami
org        agency  (matched by: repos pattern github.com/client/* of agency)
identity   Alice Doe <human:alice@agency.example>   act_2ad3…f891  @alice  (from: org entry (config.yaml orgs[agency].identity))
repo       client repo  (guards on)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_162d…9666
remotes    github.com/client/website
stores     git@github.com:agency/notes on branch main  (org folder org)
links      known  0 link(s) for this repo (stores of 1 entry scanned)
personal   me -> ~/.lore/personal
$ lore org link agency --project client
Continue? [y/N] y
This marks github.com/client/website as a CLIENT repo of agency: internal notes default to github.com/agency/notes client/,
and the leak guards turn on for everyone in agency.
  link: project prj_162d…9666 at github.com/client/website → github.com/agency/notes@main:client/ (default=false)
✓ github.com/client/website (prj_162d…9666) linked to github.com/agency/notes client/ (created; pushed)
  nothing was written to this repo
$ git status --short
$ lore actor register
✓ act_2ad3…f891 Alice Doe (@alice) registered in agency org/

# bob, in website
$ lore actor register
✓ act_54fe…28f2 Bob Roe (@bob) registered in agency org/

# alice, in website
$ lore whoami
org        agency  (matched by: link in github.com/agency/notes client/, project id + remote)
identity   Alice Doe <human:alice@agency.example>   act_2ad3…f891  @alice  (from: org entry (config.yaml orgs[agency].identity))
repo       client repo  (guards on)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_162d…9666
remotes    github.com/client/website
stores     git@github.com:agency/notes on branch main  (org folder org)
internal   github.com/agency/notes client/
links      known  1 link(s) for this repo (stores of 1 entry scanned)
personal   me -> ~/.lore/personal
```
<!-- /example -->

`whoami` now says the org was matched by the link, and names the internal project.

`whoami` prints the same lines without a terminal (a script, an agent's tool call) or with a CI variable set: a store's address is not a secret. Only a credential inside a URL (a password, a token used as the user name) is printed as `<redacted>`, and only that part. See [Troubleshooting](../troubleshooting.md#a-url-shows-redacted).

Notes go to the internal project by default. A project rule goes into the repo with `--to repo`:

<!-- example: agency#internal-by-default only=1,2,3 lines=6 -->
```console
# alice, in website
$ lore memory add --body 'they approve estimates faster when we send them on WhatsApp first'
✓ mem_05e9…4603 [agency/client]
  filed: defaults.client_repos.memory = agency (personal config) → the linked project client/
  scope: [master]
$ lore rule add --body 'run the tests before you push' --to repo
✓ rul_c0f3…b5b8
  scope: [master] severity: must
$ git status --short
?? .lore/data/actors/act_2ad3…f891.json
?? .lore/data/rules/
```
<!-- /example -->

The memory went to `agency/client` in the store and was pushed there. The rule is a file in the client's repo, to be committed with the code.

Alice adds an internal task for Bob, from any branch. Bob sees it in his list and inbox, and finishes it from the client's repo. Nothing in the client's repo changes:

<!-- example: agency#internal-task -->
```console
# alice, in website
$ git switch -q -c feat/checkout && lore task add 'send invoice reminder before demo' --to agency --assign @bob
✓ tsk_76bd…3f10 — send invoice reminder before demo [medium] → [agency/client]

# bob, in website
$ lore sync >/dev/null; git switch -q -c feat/login && lore task list
○ tsk_76bd…3f10 send invoice reminder before demo [medium]  [agency/client]
$ lore inbox
-  assigned              tsk_76bd…3f10  send invoice reminder before demo (lead)
$ lore task done tsk_76bd…3f10
✓ tsk_76bd…3f10 done
  learned something? `lore memory add --body "…"` (or pass --lesson next time)
$ git status --short
```
<!-- /example -->

The `[agency/client]` label says where each row lives. `lore task done` found the task in the store's live rows and landed it on the store's `main`.

For coding agents, `lore render` writes the internal context (the private notes the agent should know) outside the checkout: one private file per internal store under `~/.lore/render/<repo id>/`, with an `index.md` listing them. The checkout gets nothing new besides `.lore/LORE.md`; agents read the private files through `lore context` and the hooks:

<!-- example: agency#render -->
```console
# alice, in website
$ lore render
✓ .lore/LORE.md (2554 bytes, budget 16000 codex; 1 full + 0 line rules, 0 + 0 hotfixes, 1 topics)
✓ .lore/LORE.full.md (344 bytes, every pinned entry in full; not imported)
= ~/work/website/AGENTS.md (pointer present)
✓ private context → ~/.lore/render/rep_d431…313a (2 stores, 2 rows, outside the checkout); no target tool has a private file: agents get it through `lore context` and the hooks
$ cat ~/.lore/render/*/index.md
<!-- generated by lore; private: never quote this content in committed files, client files, commits, PRs or chat -->
repo website (rep_d431…313a)
sto_b4f4…a5b0.md  store me, me/everywhere  -  internal
sto_7a67…9504.md  store agency, agency/org  -  internal
$ git status --short
?? .lore/LORE.full.md
?? .lore/LORE.md
```
<!-- /example -->

If someone pastes internal text into a file of the client's repo, the commit is blocked:

<!-- example: agency#leak-blocked only=1,2 -->
```console
# alice, in website
$ mkdir -p src && echo '// they approve estimates faster when we send them on WhatsApp' > src/notes.ts && git add src/notes.ts
$ git commit -m 'add notes'
ERROR: [E_LEAK_BLOCKED] blocked, this commit would share internal content with the client repo:
  commit src/notes.ts                      matches internal note mem_05e9…4603 (10 words)
  hint: Remove the internal text from src/notes.ts
(exit 1)
```
<!-- /example -->

Lore checks twice. At commit, the `pre-commit` hook checks the staged files and the `commit-msg` hook checks the message. At push, the `pre-push` hook checks every file version and commit message the push would send that the client's remote does not have yet. The push check refuses internal ids, internal sentences and secrets by the same rules, and it fails closed: if the store cannot be read, the push is refused. To get past it, rewrite the commits that carry the text (`git commit --amend`, or `git rebase`) and push again.

A row written to the client repo (`--to repo`, or a kind whose default is the repo) is checked before it is written: if it quotes an internal note, lore refuses with `E_LEAK_BLOCKED`, says "Nothing written" and writes nothing.

`git commit --no-verify` and `git push --no-verify` skip every local check. So the client's CI should run `lore check leaks --ci` as the backstop. It needs no store, so it catches internal rows by their ids and structure, not pasted sentences.

Agents check a pull request body or comment before posting it. The same text is refused there. An unrelated text passes:

<!-- example: agency#leak-blocked only=4,5,6 -->
```console
# alice, in website
$ printf 'Fixes checkout.\n\nThey approve estimates faster when we send them on WhatsApp first.\n' > pr.md
$ lore check pr-body pr.md
ERROR: [E_LEAK_BLOCKED] blocked, this PR body would share internal content with the client repo:
  PR body                                  matches internal note mem_05e9…4603 (11 words)
  hint: Remove the internal text before posting
(exit 19)
$ printf 'Fixes checkout totals for INR.\n' | lore check pr-body -
✓ nothing internal found (a copy-paste check: a paraphrase passes it)
```
<!-- /example -->

Agents must say where a rule goes in a client repo. Lore refuses to guess for them:

<!-- example: agency#agent-rule-refused -->
```console
# alice, in website
$ lore agent start --label agent-backend --owner @alice
✓ act_28f8…03df agent agent-backend (owner @alice) registered in agency org/; LORE_ACTOR=agent:agent-backend LORE_SESSION=s-10e90368
export LORE_ACTOR=agent:agent-backend LORE_SESSION=s-10e90368
$ lore rule add --body 'require prepayment from this client'
ERROR: [E_INVALID_INPUT] stores: this write needs an explicit --to: a rule written by an agent in a client repo must pass --to (repo or agency); nothing written
(exit 20)
```
<!-- /example -->

## What the client sees

Carol (the client's developer) uses lore in the same repo with no config. She sees the project rule, and nothing of the agency's: no memory, no internal task, no link, no store name. A search through every ref the client's remote serves finds none of the internal text:

<!-- example: agency#client-view -->
```console
# carol, in app
$ git pull -q && lore memory list
(no memories)
$ lore rule list
rul_c0f3…b5b8 [must/always] run the tests before you push
$ lore task list
(no tasks)
$ lore whoami
org        none  (matched by: nothing matched)
identity   carol <human:carol@client.example>   act_3b62…e913  (from: git config user.email)
repo       no org  (fallback identity)
machine    mch_98c3dece
agent      none  (no coding agent detected)
project    prj_162d…9666
remotes    github.com/client/website
links      not checked  store links are not read yet (they arrive with `lore org link` and stores)
personal   not set
config     ~/.lore/config.yaml does not exist (no orgs configured)
$ git fetch -q origin '+refs/*:refs/remotes/all/*' && git grep -i -c whatsapp $(git for-each-ref --format='%(refname)' refs/remotes/all) -- . || echo 'no match in any ref'
no match in any ref
```
<!-- /example -->

## What happened

```text
 Agency people (config: agency)                   github.com/agency/notes (store)
   lore memory add …              ──────────────▶ client/.lore/data/memories/…
   lore task add … --to agency    ──────────────▶ refs/lore/live/main  client/tasks/…  → lands on store main
   lore org link …                ──────────────▶ client/.lore/data/_meta.json  links: [client/website]
   lore render                    ──────────────▶ ~/.lore/render/<repo-id>/<store-id>.md  (outside the checkout)

 github.com/client/website (the client can read all of it)
   .lore/data/rules/…  ◀── lore rule add … --to repo   (the only agency row here)
```

## Pitfalls

- **The first command after a teammate registers** may not find `@bob` for up to 60 seconds (the fetch cache). `lore sync` refreshes at once.
- **`--no-verify` skips every local check.** `git commit --no-verify` skips the commit check and `git push --no-verify` the push check. Only `lore check leaks --ci` in the client's CI is left, and it sees ids, not sentences.
- **Short sentences slip through the text check.** The commit check matches runs of 8 or more words. "they approve faster on WhatsApp" (5 words) pasted into a file is not caught. A longer copy is. Paraphrase is never caught. The check stops copy-paste accidents. It is not a wall.
- **No store, no guess.** If the store cannot be read and there is no cached link index, writes without `--to` fail with `E_LINK_UNKNOWN`. See [Troubleshooting](../troubleshooting.md#e_link_unknown).
- **Rules in a client repo** default to the repo in this config, so a person at a terminal can add them. An agent (or a script without a terminal) must pass `--to repo` or `--to agency` explicitly.
- **`lore check pr-body`** checks a pull request text before an agent posts it. Pass the file, or `-` for stdin. Like the commit check, it matches copied runs of words.
- The client's own developers can use lore normally in the same repo (recipe "the client also uses lore"). Their config is empty or names their own org. They never see the agency's store.
- To share one internal note with the client on purpose, see [Sharing an internal note with the client](sharing-a-note.md).

## Related

- [Contractor limited to one client](contractor.md) and [Sharing an internal note with the client](sharing-a-note.md).
- [Cleaning up after a leak](leak-cleanup.md): when internal text reached the client repo.
- [Identity and stores](../identity-and-stores.md): `lore org import`, `lore org link`.
- [Concepts](../concepts.md): stores, links and client repos.
