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

Open the TUI on your decisions and open one to read it in full. These are all the flags:

<!-- help: tui -->
Browse every table of the attached stores in the terminal (read-only)

```text
Usage:
  lore tui

Flags:
      --kind string   table to open first (e.g. memory, task, decision); default the tables screen
```
<!-- /help -->

## Pitfalls

- **It does not edit.** To change a row, copy its id from the row screen and run `lore <kind> edit <id>`.
- **Sensitive values stay hidden.** A field marked sensitive shows `••••`; use `lore show <id> --show-sensitive` when you really need the value.

## Related

- [Rules, memories and decisions](rules-memories-decisions.md)
- [Search](search.md): the fast way to find one row by its words.
- [Knowledge guides index](README.md)
