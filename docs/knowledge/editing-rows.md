# Edit a row in your editor

**In short:** every note lore keeps is a plain JSON file. `lore path <id>` prints where that file is, and `lore edit <id>` opens the row in your own editor as readable Markdown: the fields at the top, the long text below. Save and close the editor and lore checks the row and writes it exactly like any other change. Editing the JSON file by hand keeps working too.

## What it is and when you want it

A row (a task, a memory, a rule, a decision, any note) is one file named after its id, `<id>.json`. In a code repo the files sit under `.lore/data/<table>/`; in a store they sit in the store's folder. You can open and read them like any file.

JSON is fine for lore but not for writing three paragraphs of text. `lore edit` gives you the same row as Markdown instead:

- the fields of the row are YAML frontmatter at the top, between two `---` lines, one `key: value` per line;
- the row's long text (`body` for most kinds; `message` for a reminder, `bio` for a person, `description` for a tech doc) is everything below the closing `---`, as Markdown.

Use it when a change is easier to make by hand than with flags: rewriting a task's description, fixing a typo in a decision, filling in several fields at once.

## Before you start

- A repo or a store folder with lore set up: see [Your first project](first-project.md).
- An editor. lore runs `$VISUAL`, else `$EDITOR`, else `vi`. An editor that opens a window must wait until you close the file: `EDITOR="code --wait"`, `EDITOR="subl -w"`, `EDITOR="emacsclient -t"`.

## Find a row's file: lore path

`lore path` (also spelled `lore open`) takes any row id and prints one absolute path, nothing else, so it works inside other commands:

```console
$ lore path tsk_01a1cf0fdf477015ae55a8e7a08273b1
/home/dev/code/app/.lore/data/tasks/tsk_01a1cf0fdf477015ae55a8e7a08273b1.json
$ $EDITOR "$(lore path tsk_01a1cf0fdf477015ae55a8e7a08273b1)"
```

`--json` adds the table, the store and the store folder:

```console
$ lore path tsk_01a1cf0fdf477015ae55a8e7a08273b1 --json
{
  "id": "tsk_01a1cf0fdf477015ae55a8e7a08273b1",
  "table": "tasks",
  "path": "/home/dev/code/app/.lore/data/tasks/tsk_01a1cf0fdf477015ae55a8e7a08273b1.json",
  "store": "repo",
  "folder": ""
}
```

The answer always comes from the store that holds the row, so a row in a team store prints the path inside your copy of that store, not inside the repo.

When there is no file to print, lore says why and what to run instead:

- **Not found.** The error names every store and folder lore looked in. Run the command in the repo or store folder the row belongs to, or find the row by its text with `lore search <words>`.
- **A live row.** Tasks, missions, handoffs and comments in a code repo are *live* by default: they are shared at once on the git ref `refs/lore/live`, before any commit, and get their file in `.lore/data` only when they land on the default branch. `lore path` names the ref and the path inside it; `lore edit` edits a live row in place.
- **Landed, not pulled.** The row is on the remote default branch and your checkout has not pulled it yet: `git pull`.

## Step 1 — Open the row

```console
$ lore edit tsk_01a1cf0fdf477015ae55a8e7a08273b1
```

Your editor opens a temporary file like this one:

```markdown
---
# lore edit: tsk_01a1cf0fdf477015ae55a8e7a08273b1 (tasks) as Markdown. Change values, save and close the editor, and lore checks the row and writes it.
# store repo; file /home/dev/code/app/.lore/data/tasks/tsk_01a1cf0fdf477015ae55a8e7a08273b1.json
# The text below the closing --- is the body field (Markdown).
# An empty value (key:) clears an optional field. id, table, _hlc, created_* and updated_* are lore's own and cannot change.
id: tsk_01a1cf0fdf477015ae55a8e7a08273b1
table: tasks
_hlc: 2026-11-12T09:21:26.394Z/0000/act_e16a8fd67eb4732194ef0c9a4e9d478e
created_at: "2026-11-12T09:21:26.394Z"
created_by: act_e16a8fd67eb4732194ef0c9a4e9d478e
updated_at: "2026-11-12T09:21:26.394Z"
updated_by: act_e16a8fd67eb4732194ef0c9a4e9d478e
project_id: prj_01a1cf028c6b7e35a3ffee5b68421338
title: 'Fix login: redirect # now'
status: todo
priority: medium
commitment: accepted
# Not set (give one a value to set it):
due_at:
assignees:
---
The login page sends people back to / instead of the page they asked for.
```

The lines starting with `#` are help; lore ignores them. Fields the row holds come first, the empty ones after the "Not set" line.

## Step 2 — Change it and save

Change any value, write the long text below the closing `---`, save and close the editor:

```console
$ lore edit tsk_01a1cf0fdf477015ae55a8e7a08273b1
✓ tsk_01a1cf0fdf477015ae55a8e7a08273b1 updated in store repo: body, title
  file /home/dev/code/app/.lore/data/tasks/tsk_01a1cf0fdf477015ae55a8e7a08273b1.json
```

The write is the same as `lore task edit` would make: lore checks every field against the table, checks references to other rows, runs the secret scan and the team's policy, stamps a new write clock (`_hlc`), `updated_by` (you) and `updated_at`, and commits and pushes the way every write in that repo or store does. The line names the fields that changed and the file.

Close the editor without changing anything and nothing is written:

```console
$ lore edit tsk_01a1cf0fdf477015ae55a8e7a08273b1
no change: tsk_01a1cf0fdf477015ae55a8e7a08273b1 is as it was (nothing written)
```

How to write values:

| You want | Write |
|---|---|
| text with `: `, `#`, or starting with `-`, `[`, `{`, `|`, `>` | quote it: `title: 'Fix login: redirect # now'` |
| several lines in a field | a block: `close_reason: |` then the lines indented by two spaces |
| a list | `assignees: [act_…, act_…]`, or one `- item` per line; empty: `[]` |
| to clear an optional field | keep the key with no value: `due_at:` |
| a date and time | `due_at: 2026-12-01T10:00:00Z` |

## When lore refuses the edit

If something in the file is wrong, nothing is written. lore prints every problem with the file and line it is on, keeps what you typed in that file and prints its path, so nothing is lost:

```console
$ lore edit tsk_01a1cf0fdf477015ae55a8e7a08273b1
ERROR: [E_INVALID_INPUT] the edited row has 3 problems:
/tmp/lore-edit-3554320025.md:6: id cannot change: the id names the row's file and every link to it, so it never changes; put back tsk_01a1cf0fdf477015ae55a8e7a08273b1
/tmp/lore-edit-3554320025.md:15: status: want one of todo, in_progress, done, cancelled, blocked
/tmp/lore-edit-3554320025.md:17: unknown key prio: tasks has no field prio (its fields: project_id, repo_id, title, status, priority, …)
  hint: fix the lines named above (an empty value `key:` clears an optional field), then run the command again
your edited text is kept in /tmp/lore-edit-3554320025.md
  fix it there, then write it back: lore edit tsk_01a1cf0fdf477015ae55a8e7a08273b1 --file /tmp/lore-edit-3554320025.md
```

What is refused:

- a value that is not right for its field (a word in a number field, a status that does not exist, a list written as one word);
- a change to a key lore owns: `id`, `table`, `_hlc`, `created_at`, `created_by`, `updated_at`, `updated_by`, and the records lore writes itself (a task's `time_open`);
- a key the table does not have, a key written twice, and a key that had a value and was deleted (to clear a field, keep the key with an empty value);
- text below the closing `---` for a kind of row that has no long text field;
- a row that changed since you opened it (someone else's write arrived, or `_hlc` was edited): open it again with `lore edit <id>` and redo your change on the current version;
- a row that holds sensitive values (a resource with a sensitive field): lore does not write those into a plain temporary file; use the row's own command.

Fix the lines and write the kept file back without opening the editor again:

```console
$ lore edit tsk_01a1cf0fdf477015ae55a8e7a08273b1 --file /tmp/lore-edit-3554320025.md
✓ tsk_01a1cf0fdf477015ae55a8e7a08273b1 updated in store repo: body, title
```

## Without an editor (scripts and agents)

`lore row markdown <id>` prints the view, and `lore row edit <id> --file <view.md>` writes an edited view back with the same checks (`lore edit <id> --file` is the same). Any tool can change the file in between:

```console
$ lore row markdown tsk_01a1cf0fdf477015ae55a8e7a08273b1 > task.md
$ sed -i 's/^status: .*/status: in_progress/' task.md
$ lore row edit tsk_01a1cf0fdf477015ae55a8e7a08273b1 --file task.md
✓ tsk_01a1cf0fdf477015ae55a8e7a08273b1 updated in store repo: status
```

`EDITOR` may also be a script: lore runs it with the file's path as its last argument and waits for it, which is how lore's own tests drive `lore edit`. Without a terminal and with neither `$VISUAL` nor `$EDITOR` set, `lore edit` refuses instead of starting `vi` where nobody can see it.

Over the local REST API (`lore api`) the same operations are `POST /v1/rows/{id}/markdown`, `PATCH /v1/rows/{id}` with the view as `markdown` in the body, and `POST /v1/rows/{id}/path`; see [the REST reference](../reference/rest-api.md).

## Editing the JSON file directly

`lore path <id>` gives you the file; any editor works on it. lore reads the file as it is on its next command. Keep it valid JSON, and leave `id`, `_table` and `_hlc` alone: lore checks hand-made changes and `lore doctor` reports a file it cannot read. In a store, the next lore command commits a valid change as you and refuses a broken file with its line and reason: see [Editing by hand](../sync/editing-by-hand.md).

## Try it

A task in a git store: `lore path` prints its file inside the store's clone, `lore row markdown` prints the view `lore edit` opens, and `lore edit` with an editor script (it sets the priority and adds a line to the body, as a person would in `vi`) writes the change back, commits it in the store and pushes it:

<!-- example: files#lore-path -->
```console
# alice, in website
$ lore path tsk_2ad3…f891
~/.lore/stores/acme/notes/_store/tasks/tsk_2ad3…f891.json
$ lore path tsk_2ad3…f891 --json
{
  "id": "tsk_2ad3…f891",
  "table": "tasks",
  "path": "~/.lore/stores/acme/notes/_store/tasks/tsk_2ad3…f891.json",
  "store": "notes",
  "folder": "."
}
```
<!-- /example -->

<!-- example: files#lore-edit lines=12 -->
```console
# alice, in website
$ lore row markdown tsk_2ad3…f891
---
# lore edit: tsk_2ad3…f891 (tasks) as Markdown. Change values, save and close the editor, and lore checks the row and writes it.
# store notes; file ~/.lore/stores/acme/notes/_store/tasks/tsk_2ad3…f891.json
# The text below the closing --- is the body field (Markdown).
# An empty value (key:) clears an optional field. id, table, _hlc, created_* and updated_* are lore's own and cannot change.
id: tsk_2ad3…f891
table: tasks
_hlc: 2026-11-02T10:00:10.000Z/0002/act_05e9…4603
created_at: "2026-11-02T10:00:10Z"
created_by: act_05e9…4603
updated_at: "2026-11-02T10:00:10Z"
updated_by: act_05e9…4603
…
$ EDITOR=$HOME/bin/edit-task lore edit tsk_2ad3…f891
lore: pushed store notes: commit dfd8dc8 to branch main
lore: wrote ~/.lore/stores/acme/notes/_store/tasks/tsk_2ad3…f891.json (store notes)
✓ tsk_2ad3…f891 updated in store notes: body, priority
  file ~/.lore/stores/acme/notes/_store/tasks/tsk_2ad3…f891.json
$ lore task show tsk_2ad3…f891
tsk_2ad3…f891
  title:    Renew the TLS certificate
  status:   todo
  priority: high
  commit:   accepted
Ask the hosting provider for the new chain first.
created 2026-11-02T10:00:10Z by alice · updated 2026-11-02T10:01:25Z by alice
```
<!-- /example -->

The whole run is the example section `files`: [`files`](../../tests/docs/sections/files.sh) ([output](../../tests/docs/transcripts/files.txt)).

## Related

- [Editing by hand](../sync/editing-by-hand.md): change, add, delete or move row files in a store clone with any editor or plain git.
- [Stores as plain folders](../sync/store-clones.md): what is in a store's clone.

- [Rules, memories and decisions](rules-memories-decisions.md) and [Other kinds of notes](other-row-kinds.md): the `edit` command of each kind, with flags.
- [Browse your notes in the terminal](tui.md): `lore show <id>` prints any row.
- [Stores](../sync/stores.md): where a store keeps its rows.
