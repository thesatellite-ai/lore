# Branches: peek and promote

**In short:** a note written on a branch stays on that branch, like the code next to it. `lore sync peek` lets you read the notes on any branch without switching to it. `lore sync promote` copies one note onto another branch (usually main) right now, when it cannot wait for the pull request.

## What it is and when you want it

Because notes are files in `.lore/data` ([How notes travel with git](how-notes-travel-with-git.md)), switching branch switches notes too. That is usually what you want: a rule about a half-built payment page should not show up on main before the page does.

Two cases need more:

- **Peek:** you want to see what a teammate learned on their branch (their notes and tasks) before it merges, without checking it out. `lore sync peek <branch>` reads the files straight from git and imports nothing.
- **Promote:** you learned something on your branch that must apply everywhere now, for example "Never put passwords in the code". `lore sync promote <id> --to main` makes a new commit on main with that one row file. It does not switch branch and does not push; you push or open a pull request as usual.

## Before you start

- A repo set up with lore ([How notes travel with git](how-notes-travel-with-git.md)).
- For peek on a teammate's branch: `git fetch` first, then use `origin/<branch>`.

## Step 1 — Notes follow the branch

Alice (an agency developer) starts a branch for the client's payment page and writes a memory and a rule on it. On main they are not there; back on the branch they are:

<!-- example: sync#branch-notes -->
```console
# alice, in website
$ git checkout -q -b feature/payments
$ lore memory add --body 'We use Stripe for payments.'
lore: wrote ~/work/website/.lore/data/memories/mem_c0f3…b5b8.json
✓ mem_c0f3…b5b8
  scope: [master]
$ lore rule add --body 'Never put passwords in the code.'
lore: wrote ~/work/website/.lore/data/rules/rul_76bd…3f10.json
✓ rul_76bd…3f10
  scope: [master] severity: must
$ git add -A && git commit -qm 'lore: payment notes' && git push -q -u origin feature/payments
$ git checkout -q main && lore memory list
mem_05e9…4603 [retrieved] The client replies faster on WhatsApp than email.
$ git checkout -q feature/payments && lore memory list
mem_05e9…4603 [retrieved] The client replies faster on WhatsApp than email.
mem_c0f3…b5b8 [retrieved] We use Stripe for payments.
```
<!-- /example -->

## Step 2 — Peek at a teammate's branch

Bob (another agency developer) is on main. He fetches and peeks at Alice's branch. Add a table name (the kind of note: `memories`, `rules`, `tasks`, ...) to see only that kind:

<!-- example: sync#peek -->
```console
# bob, in website
$ git fetch -q origin
$ lore sync peek origin/feature/payments
actors             act_54fe…28f2  
memories           mem_05e9…4603  The client replies faster on WhatsApp than email.
memories           mem_c0f3…b5b8  We use Stripe for payments.
policies           pol_d431…313a  context.targets
projects           prj_7a67…9504  website
repos              rep_2ad3…f891  
rules              rul_162d…9666  Run the tests before you push.
rules              rul_76bd…3f10  Never put passwords in the code.
$ lore sync peek origin/feature/payments memories
memories           mem_05e9…4603  The client replies faster on WhatsApp than email.
memories           mem_c0f3…b5b8  We use Stripe for payments.
```
<!-- /example -->

Nothing changed in Bob's database: peek only reads. Use `--json` to feed the list to a script.

## Step 3 — Promote one rule to main

The rule "Never put passwords in the code" should apply on every branch today. Alice promotes it to main. Try `--dry-run` first: it says which file would be committed.

<!-- example: sync#promote only=1,2,3 -->
```console
# alice, in website
$ lore sync promote rul_76bd…3f10 --to main --dry-run
would commit .lore/data/rules/rul_76bd…3f10.json onto main
$ lore sync promote rul_76bd…3f10 --to main
✓ committed 1 file(s) onto main (86dbae476bd6) — not pushed
$ git log --oneline -2 main
86dbae4 lore: promote rul_76bd…3f10
9b40ce7 lore: whatsapp memory
```
<!-- /example -->

Then she pushes main, and Bob gets the rule with a normal pull:

<!-- example: sync#promote only=4,5 -->
```console
# alice, in website
$ git push -q origin main

# bob, in website
$ git pull -q && lore rule list
rul_162d…9666 [must/always] Run the tests before you push.
rul_76bd…3f10 [must/always] Never put passwords in the code.
```
<!-- /example -->

When Alice's branch merges later, the promoted file is already on main with the same content, so git merges it without a conflict.

## Try it — the two refusals you will meet first

Bob peeks at `feature/payments` without the `origin/` prefix: his clone has no local branch of that name, so lore stops and names the ref. Alice, still on `feature/payments`, tries to promote to the branch she is on: promote copies from your branch to another one, so it refuses:

<!-- example: sync#peek-and-promote-refusals -->
```console
# bob, in website
$ lore sync peek feature/payments
ERROR: [E_INVALID_INPUT] read feature/payments
  hint: is it a valid ref? try `git fetch` first for remote branches
(exit 20)

# alice, in website
$ git branch --show-current
feature/payments
$ lore sync promote rul_76bd…3f10 --to feature/payments
ERROR: [E_INVALID_INPUT] refusing to commit onto the checked-out branch feature/payments; edit the files and commit normally
(exit 20)
```
<!-- /example -->

## Pitfalls

- **Peek needs a fetched ref** (a ref is git's name for a branch or other named pointer; "fetched" means your clone has downloaded it). `lore sync peek feature/payments` reads your local branch; to see the remote one, run `git fetch` and use `origin/feature/payments`.
- **Promote copies the file as it is on your current branch.** Make your last edit before you promote. It cannot target the branch you are on.
- **Promote never pushes.** The new commit sits on your local main until you push it or open a pull request. If main is protected on GitHub (GitHub will not let anyone push to it directly), push it to a new branch and open a pull request instead.
- **Rows that vanish on a branch switch are not lost.** When you switch to a branch that does not have a row's file, lore stops showing that row, because rows are read from the files of the branch you are on. That is normal; switching back brings the row back from its file. Nothing goes to `lore trash list --local`, which only holds copies an older lore (v0.1.17) made. See [Trash, restore from git and purge](trash-and-purge.md).
- **Live rows do not follow branches.** Tasks are live by default: they are shared at once on every branch. See [Lore for teams](../team/README.md).

## Related

- [How notes travel with git](how-notes-travel-with-git.md)
- [Conflicts and duplicates](conflicts-and-duplicates.md): when the branch merges and two people changed the same note.
- [How it works › Lookup: which copy of a row wins](../team/how-it-works.md#lookup-which-copy-of-a-row-wins): branch rows, live rows and stores together.
- [Feature index › Git sync](../reference/features.md#git-sync-notes-travel-with-your-commits)
