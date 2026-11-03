# Rules, memories and decisions

**In short:** a *rule* is an "always" or "never" ("Run the tests before you push"). A *memory* is something you learned ("The client replies faster on WhatsApp than email"). A *decision* is a choice and its reason ("We use Stripe for payments"). This page shows how to add, read, change, replace, retire and delete each one.

## What they are and when you want them

| Kind | Think of it as | Use it for | Command |
|---|---|---|---|
| Rule | a sign on the office wall | hard "must" and "must not" lines every session follows | `lore rule` |
| Memory | a sticky note | facts you learned that are useful later | `lore memory` |
| Decision | a signed note in the project folder | what you chose, why, and what it replaced | `lore decision` |

All three share the same verbs: `add`, `list`, `show`, `edit`, `search`, `archive`, `unarchive` and `delete`. Memories also have `invalidate`. Every row gets an id with a short prefix (`rul_`, `mem_`, `dec_`), and every other command takes that id.

## Before you start

- A repo with lore set up: see [Your first project](first-project.md).

## Step 1 — Add rules

`--severity` says how strict the rule is: `must` (the default; a hard stop), `should` (a warning) or `may` (a suggestion). `--activation` says when it is shown: `always` (the default), `glob` (only for files whose names match the pattern in `--globs`, for example `["*.css"]` for style files), or `manual` (only when you ask for it).

<!-- example: knowledge#rule -->
```console
# alice, in website
$ lore rule add --body 'Run the tests before you push'
✓ rul_7a67…9504 
  scope: [master] severity: must
lore sync: exported 1
lore sync: 1 file(s) in .lore/data not committed — commit them with your change
$ lore rule add --body 'Never put passwords in the code' --severity must
✓ rul_2ad3…f891 
  scope: [master] severity: must
lore sync: exported 1
lore sync: 2 file(s) in .lore/data not committed — commit them with your change
$ lore rule add --body 'Keep pull requests under 400 lines' --severity should
✓ rul_162d…9666 
  scope: [master] severity: should
lore sync: exported 1
lore sync: 3 file(s) in .lore/data not committed — commit them with your change
$ lore rule list
rul_7a67…9504 [must/always] Run the tests before you push
rul_2ad3…f891 [must/always] Never put passwords in the code
rul_162d…9666 [should/always] Keep pull requests under 400 lines
$ lore rule show rul_7a67…9504
rul_7a67…9504
  severity:   must
  activation: always
Run the tests before you push
```
<!-- /example -->

## Step 2 — Add memories

A memory has a *kind* (`--kind`): `retrieved` (the default; found by search when needed), `core` (always useful), `episodic` (something that happened once), `procedural` (how to do something) or `archival` (kept for the record). `--tag` adds a label you can search for later.

<!-- example: knowledge#memory -->
```console
# alice, in website
$ lore memory add --body 'The client replies faster on WhatsApp than email'
✓ mem_54fe…28f2 
  scope: [master]
lore sync: exported 1
lore sync: 4 file(s) in .lore/data not committed — commit them with your change
$ lore memory add --body 'The staging site is reset every Monday morning' --tag staging
✓ mem_05e9…4603 
  scope: [master]
lore sync: exported 1
lore sync: 5 file(s) in .lore/data not committed — commit them with your change
$ lore memory list
mem_54fe…28f2 [retrieved] The client replies faster on WhatsApp than email
mem_05e9…4603 [retrieved] The staging site is reset every Monday morning
```
<!-- /example -->

## Step 3 — Add a decision

A decision needs a short `--title` (the choice) and a `--body` (the reason). `--status` is `accepted` unless you say `proposed`, `superseded` or `deprecated`.

<!-- example: knowledge#decision -->
```console
# alice, in website
$ lore decision add --title 'We use Stripe for payments' --body 'Stripe already works in the client shop app, so we keep one payment provider.'
✓ dec_c0f3…b5b8 — We use Stripe for payments 
lore sync: exported 1
lore sync: 6 file(s) in .lore/data not committed — commit them with your change
$ lore decision list
dec_c0f3…b5b8 [accepted] We use Stripe for payments
$ lore decision show dec_c0f3…b5b8
dec_c0f3…b5b8
  title:  We use Stripe for payments
  status: accepted
Stripe already works in the client shop app, so we keep one payment provider.
```
<!-- /example -->

## Step 4 — Change a row

`edit` changes a row in place. Use it to fix a typo or add a detail. Only the fields you pass change.

<!-- example: knowledge#edit -->
```console
# alice, in website
$ lore memory edit mem_54fe…28f2 --body 'The client replies faster on WhatsApp than email, even on weekends'
✓ mem_54fe…28f2 updated
lore sync: exported 1
lore sync: 6 file(s) in .lore/data not committed — commit them with your change
$ lore memory show mem_54fe…28f2
mem_54fe…28f2
  kind:   retrieved
  trust:  0.50
  source: manual
The client replies faster on WhatsApp than email, even on weekends
```
<!-- /example -->

## Step 5 — Replace a row with a new one

When the meaning changes, add a new row and name the old one with `--supersedes <old id>`. This keeps the history: the old row stays readable and records which row replaced it (`superseded_by_id`, shown as `superseded-by` in `lore memory show`). A replaced decision also moves to `superseded`. `--supersedes` works on `rule add`, `memory add`, `decision add`, `hotfix add` and `pattern add`.

<!-- example: knowledge#supersedes -->
```console
# alice, in website
$ lore decision add --title 'We use Stripe and bank transfer for payments' --body 'The client asked for bank transfer too.' --supersedes dec_c0f3…b5b8
✓ dec_76bd…3f10 — We use Stripe and bank transfer for payments 
lore sync: exported 2
lore sync: 7 file(s) in .lore/data not committed — commit them with your change
$ lore decision list
dec_c0f3…b5b8 [superseded] We use Stripe for payments
dec_76bd…3f10 [accepted] We use Stripe and bank transfer for payments
```
<!-- /example -->

The old decision now says `superseded`. lore refuses `--supersedes` with an id that is not a row of the same kind in this project (`E_NOT_FOUND`), or a row that was already replaced (`E_INVALID_INPUT`: supersede the newer row instead, so the chain stays one line); a refused command writes nothing.

Rows linked by older versions point the wrong way; run `lore doctor --fix` once to repair them.

## Step 6 — Mark a memory as no longer true

`lore memory invalidate <id>` stamps the memory with a *valid until* time (the moment it stopped being true) without deleting it. Use it when a fact goes out of date, like an office move.

<!-- example: knowledge#invalidate only=2,4 -->
```console
# alice, in website
$ lore memory invalidate mem_d431…313a
✓ mem_d431…313a invalidated (valid_until=now)
lore sync: exported 1
lore sync: 8 file(s) in .lore/data not committed — commit them with your change
$ lore memory show mem_d431…313a
mem_d431…313a
  kind:   retrieved
  trust:  0.50
  source: manual
  valid-until: 2026-11-02T10:30:00Z
The office printer is on the second floor
```
<!-- /example -->

## Step 7 — Retire a rule for now

`archive` hides a row from `lore rule list`, the rendered file and search without deleting it. `lore rule list --archived` shows it again with an `[archived]` mark; `unarchive` brings it back.

<!-- example: knowledge#archive only=1,2,3,4,5 -->
```console
# alice, in website
$ lore rule archive rul_7a67…9504
✓ rul_7a67…9504 archived
lore sync: exported 1
lore sync: 8 file(s) in .lore/data not committed — commit them with your change
$ lore rule list
rul_2ad3…f891 [must/always] Never put passwords in the code
rul_162d…9666 [should/always] Keep pull requests under 400 lines
$ lore rule list --archived
rul_7a67…9504 [must/always] Run the tests before you push [archived]
rul_2ad3…f891 [must/always] Never put passwords in the code
rul_162d…9666 [should/always] Keep pull requests under 400 lines
$ lore render >/dev/null && grep -c 'Run the tests before you push' .lore/LORE.md || true
0
$ lore rule unarchive rul_7a67…9504
✓ rul_7a67…9504 unarchived
lore sync: exported 1
lore sync: 8 file(s) in .lore/data not committed — commit them with your change
```
<!-- /example -->

The `0` is the count of the archived rule in the rendered file: it is gone from what the coding tool reads.

## Step 8 — Delete a row for good

`delete` removes a row with no undo, so it asks for `--confirm`. Archive is usually what you want.

<!-- example: knowledge#delete only=2,3 -->
```console
# alice, in website
$ lore memory delete mem_b4f4…a5b0 --confirm
✓ mem_b4f4…a5b0 deleted (hard)
lore sync: removed files 1
lore sync: 8 file(s) in .lore/data not committed — commit them with your change
$ lore memory show mem_b4f4…a5b0 || true
ERROR: [E_NOT_FOUND] memory "mem_b4f4…a5b0" not found
  hint: it may have landed on main while this branch is older: `lore sync peek origin/main memories`
```
<!-- /example -->

## Try it: a password is refused

lore refuses text that looks like a password, a key or a token, because rows end up in git where everyone with the repo can read them. `--allow-secrets` lets the row into your local database anyway, but lore still checks every file before it goes to git: a row that holds a password is kept out of `.lore/data` (the `0` below counts its file), and every later command repeats the warning until you edit or delete the row.

<!-- example: knowledge#secrets -->
```console
# alice, in website
$ lore memory add --body 'The staging password is password=example-only'
ERROR: [E_SECRET_DETECTED] body contains a secret (credential pattern password_pair, preview pass****)
  hint: use --allow-secrets to override (logged loud); see also ~/.lore/pii-patterns.txt
(exit 1)
$ lore memory add --body 'The staging password is password=example-only' --allow-secrets
✓ mem_28f8…03df 
  scope: [master]
lore sync: secret-detected memories/mem_28f8…03df.json: credential pattern password_pair found — not written to git; edit the row or set LORE_SYNC_ALLOW_SECRETS=1 for one run
$ ls .lore/data/memories | grep -c mem_28f8…03df || true
0
$ lore memory delete mem_28f8…03df --confirm
lore sync: secret-detected memories/mem_28f8…03df.json: credential pattern password_pair found — not written to git; edit the row or set LORE_SYNC_ALLOW_SECRETS=1 for one run
✓ mem_28f8…03df deleted (hard)
```
<!-- /example -->

## Pitfalls

- **Archived rows are hidden from lists.** `lore rule list` and `lore memory list` skip archived rows; pass `--archived` to see them with an `[archived]` mark.
- **An invalidated memory is still listed and still found by search.** `invalidate` records the end date (`valid-until` in `lore memory show`); it does not hide the row. Archive it as well if you want it out of the way.
- **Rows written by older lore versions point the other way.** Before this was fixed, `--supersedes` stored the old id on the new row and left the old row unchanged. `lore doctor` lists such pairs and `lore doctor --fix` turns them around once (a second run changes nothing).
- **`delete` wants `--confirm`, not `--yes`.** `--yes` is the answer to lore's "are you sure?" questions elsewhere; `delete` stops with `unknown flag: --yes`.
- **Shell quotes.** Wrap the body in single quotes so the shell does not touch `$` or `!` in your text. You can also pipe the body in: `echo 'Run the tests before you push' | lore rule add`.
- **`--allow-secrets` does not put the secret in git.** Rules and memories are checked when you add them; every kind of row is checked again before its file is written to `.lore/data`. A decision with a password in its body is added without a question, then kept out of git with a `secret-detected` line on every command. Fix the row (`edit`) or delete it. `LORE_SYNC_ALLOW_SECRETS=1` lets one run write it anyway; do not use it for real passwords.
- **Not committed yet.** Every change prints `lore sync: N file(s) in .lore/data not committed`. The row is safe on your machine, but nobody else sees it until you commit `.lore/data`.

## Related

- [Other kinds of notes](other-row-kinds.md): hotfixes, patterns, playbooks and the rest.
- [Comments and tags](comments-and-tags.md): talk about a row, label rows to find them together.
- [Search](search.md) and [Render](render-and-agent-files.md).
- [Secrets and private data](../operations/secrets-and-pii.md): what lore refuses and how to add your own patterns.
- [Sharing a note with a team](../team/use-cases/sharing-a-note.md): `--to`, `--live` and where a row goes when you work with others.
- [Knowledge guides index](README.md)
