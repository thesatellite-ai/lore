# Browse your notes in the terminal

**In short:** `lore tui` is a read-only browser for every note in the repo, right in your terminal. You pick a table, page through its rows, filter them by a word, and open one row to read every field. It changes nothing; you add and edit rows with the normal `lore <kind> add|edit` commands.

## What it is and when you want it

A *TUI* (terminal user interface) is an app that runs inside the terminal window. `lore tui` reads the same rows every other lore command reads, from every store attached to the repo, one table per kind of row: memories, rules, decisions, tasks and the rest.

Use it when you want to look around rather than type exact commands: to read through all your decisions, or to find a memory you half remember.

The full-screen editing TUI of lore 0.1.17 (forms, an actions menu, vim keys) is gone in 0.1.18: every change goes through the one write path the commands use.

## Before you start

- A repo with lore set up: see [Your first project](first-project.md).

## Step 1 — Open it

Run it in the repo. It opens on the tables screen: every table with its number of rows.

```console
$ lore tui
```

`--kind` opens one table's rows first, by its name (`decisions`) or the singular (`decision`):

```console
$ lore tui --kind decision
```

## Step 2 — Move around

Each screen prints its keys. Type a key and press Enter:

| Screen | Keys |
|---|---|
| Tables | a number opens that table; `q` quits |
| Rows (20 per page: id, store, label) | a number opens that row; `n` / `p` next or previous page; `/text` keeps rows that contain the text; `b` back; `q` quits |
| One row | every field, sensitive values shown as `••••`; `b` back; `q` quits |

## Try it

Open the TUI on your decisions and open the first one to read it in full. The screens read their keys line by line, so you can also feed them from a pipe; here `1` opens the first row and `q` quits:

<!-- example: knowledge#tui -->
```console
# alice, in website
$ printf '1\nq\n' | lore tui --kind decision
decisions — 1–2 of 2 (page 1/1)
  1  dec_d431…313a  repo     We use Stripe for payments
  2  dec_b4f4…a5b0  repo     We use Stripe and bank transfer for payments
<n> show · n/p page · /text filter · b back · q quit
> 
dec_d431…313a  decisions  store repo
  body: Stripe already works in the client shop app, so we keep one payment provider.
  created_by_actor_id: act_54fe…28f2
  project_id: prj_78c8…5b6c
  source_kind: manual
  status: superseded
  superseded_by_id: dec_b4f4…a5b0
  title: We use Stripe for payments
  trust_score: 0.5
  updated: 2026-11-02 10:01 by act_54fe…28f2
b back · q quit
> 
```
<!-- /example -->

The first decision says `status: superseded` and names the decision that replaced it (see [Rules, memories and decisions](rules-memories-decisions.md)).

## Show one row without the TUI

When you already have an id, `lore show <id>` (also spelled `lore row show <id>`) prints every field of any row, whatever its kind. `--origin` adds where the row came from: the store, project, repo, branch and command that wrote it, who created and last changed it, and any moves between stores.

<!-- example: knowledge#show-any-row only=2 -->
```console
# alice, in website
$ lore row show dec_d431…313a --origin
dec_d431…313a  decisions  repo  "We use Stripe for payments"
  body: Stripe already works in the client shop app, so we keep one payment provider.
  created_by_actor_id: act_54fe…28f2
  project_id: prj_78c8…5b6c
  source_kind: manual
  status: superseded
  superseded_by_id: dec_b4f4…a5b0
  title: We use Stripe for payments
  trust_score: 0.5
created 2026-11-02T10:00:50Z by act_54fe…28f2 · updated 2026-11-02T10:01:15Z by act_54fe…28f2
origin (written once)
  store      repo
  project    website  prj_78c8…5b6c  (https://github.com/client/website)
  repo       website  rep_d7c7…7fe6  (https://github.com/client/website)
  git        branch main at 601d2850ffd0
  command    lore decision add
who and when
  created    2026-11-02T10:00:50Z   alice (act_54fe…28f2)
  updated    2026-11-02T10:01:15Z   alice (act_54fe…28f2)
last write
  lore       dev
lineage
  no moves
agent session
  kept only on the machine that wrote the row: lore session list there
```
<!-- /example -->

`--expand` fills in `${var:…}` references (see [Variables and metadata](variables-and-metadata.md)), `--json` prints the row as JSON, and `--show-sensitive` prints sensitive values after asking.

These are all the `lore tui` flags:

<!-- help: tui -->
Browse every table of the attached stores in the terminal (read-only)

```text
Usage:
  lore tui

Examples:
  lore tui
  lore tui --kind decision

Flags:
      --kind string   table to open first (e.g. memory, task, decision); default the tables screen
```
<!-- /help -->

## Pitfalls

- **It does not edit.** To change a row, copy its id from the row screen and run `lore <kind> edit <id>`.
- **Sensitive values stay hidden.** A field marked sensitive shows `••••`; use `lore show <id> --show-sensitive` when you really need the value.

## Related

- [Rules, memories and decisions](rules-memories-decisions.md)
- [Variables and metadata](variables-and-metadata.md): `lore show --expand`.
- [Search](search.md): the fast way to find one row by its words.
- [Knowledge guides index](README.md)
