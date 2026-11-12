# A lore store

This folder is a lore store: notes, rules, decisions, tasks and the other
records a team keeps with lore. Every record is one small JSON file, so you
can open, read and search the store with any editor, file browser or git
tool. lore wrote this file once; edit or replace it as you like, lore never
writes it again.

## What the folders are

- `lore-store.yaml`: the store's identity (its id, who may see its rows).
- A folder named after a kind of record (`tasks/`, `memories/`, `rules/`,
  `decisions/` and so on) holds the records of the store's top folder, one
  file per record: `tasks/tsk_….json`.
- Any other folder is a store folder (a client, a project, a team), with
  its own record folders inside: `acme/tasks/tsk_….json`. Folders can
  nest (`team/web/`). A folder may not be named like a kind of record.
- `_meta.json` in a folder holds the folder's id; `_purged.json` lists
  records removed for good. Leave both to lore.
- `org/` (when the store has one) holds the company's people, teams and
  rules, and `org/lore-org.yaml`, the company's settings.

## Browsing

Open any file: the record's text is in plain JSON fields (`title`,
`body`, `content`, ...). From a terminal inside the store,
`lore task list` (or any other lore list) shows the records of the folder you
are in, and `lore path <id>` prints the file of one record.

## Editing by hand

- `lore edit <id>` opens a record in your editor as readable Markdown and
  writes it back as JSON when you save.
- You may also edit a JSON file directly, add one, delete one, or move one
  to another folder. On its next command in this store lore checks every
  file you changed: a valid edit is stamped with your name and the time
  and committed as you ("hand edit: tasks tsk_…"), so it wins over older
  changes when teammates sync, and pushed like any lore write; a deleted
  file goes to lore's trash (`lore trash restore <id>` brings it back); a
  file moved to another folder is moved there. A file that is not valid
  is refused with its line and the reason, and it is not committed or
  pushed until it is fixed; your other edits still go.
- Plain git works here: `git pull`, `git commit` and `git push`. lore sets
  this clone up for it: `merge.lore.driver` in `.git/config` (records
  merge field by field), `*.json merge=lore` in `.git/info/attributes`,
  and a block in `.git/hooks/pre-commit` that checks the records and
  scans for secrets before your commit (lines of your own in that hook
  are kept). Only when a global `core.hooksPath` would skip the clone's
  hooks does lore set this clone's own `core.hooksPath` back to
  `.git/hooks`.
- Files that are not records (notes, a folder of your own) are left alone:
  lore never deletes or reads them, and `lore doctor` names them.

## What lore checks

Before it commits anything here lore checks that each file is a valid
record of its kind, that the records it points at exist, that no secret
(a key, a token, a password) is in it, and that it is allowed in this
store. When two people change the same record lore merges it field by
field.

## What lore keeps for itself

lore's lock, its pending audit lines and when it last fetched live in
`.git/lore/` (or `.lore-state/` in a store that is a plain folder). They
are never committed. You can delete them when no lore command is running.
