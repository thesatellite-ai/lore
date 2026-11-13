# Editing by hand

**In short:** a store is a plain git clone on your machine (`~/.lore/stores/<owner>/<repo>`), and every row is one JSON file in it. You may open those files with any text editor, change them, add one, delete one, move one to another folder, or use plain `git pull`, `git commit` and `git push` there. The next lore command you run picks your changes up: it checks each file the way it checks its own writes, stamps a valid change with your name and the time, commits it as you and pushes it like any other lore write. A file that is not valid is refused with its line and the reason, and stays out of the store until you fix it.

## Where the files are

Each store folder keeps its rows right under it, one folder per kind of row: `<store>/<folder>/tasks/tsk_….json`, `<store>/<folder>/memories/mem_….json`; the store's own rows (its root folder) sit in `_store/` (`_store/tasks/`, `_store/memories/`, …). [Stores as plain folders](store-clones.md) explains the whole clone. `lore path --id <id>` prints the file of one row, and `lore edit --id <id>` opens a row in your editor as readable Markdown and writes it back for you. A code repo keeps its rows in `.lore/data/` instead.

## What lore does with each change

Run any lore command (a list is enough, for example `lore task list` in a repo linked to the store, or inside the store folder itself). Before it reads anything, lore runs `git status` in the store's clone and takes every changed row file through the same checks a `lore task edit` passes: the fields must be valid for the table, the rows they point at must exist, no secret may be in the text, and the store's policy must allow it.

<!-- example: files#edit-a-row-file-by-hand -->
```console
# alice, in .lore/stores/acme/notes
$ sed -i 's/reset every Monday/reset every Sunday night/' _store/memories/mem_54fe…28f2.json && git status --short
 M _store/memories/mem_54fe…28f2.json

# alice, in website
$ lore memory list --store notes
lore: store notes: 1 local commit(s) wait to be pushed; a read never pushes: run `lore sync` (or the next lore write here pushes them)
lore: picked up 1 row edited by hand in store notes: committed cf563f3 "hand edit: memories mem_54fe…28f2" in ~/.lore/stores/acme/notes
  edited memories mem_54fe…28f2 (body): ~/.lore/stores/acme/notes/_store/memories/mem_54fe…28f2.json
mem_54fe…28f2 [retrieved] The staging database is reset every Sunday night. [notes]

# alice, in .lore/stores/acme/notes
$ git log -1 --format='%an <%ae>  %s'; git status --short
alice <alice@agency.example>  hand edit: memories mem_54fe…28f2
```
<!-- /example -->

| You did | lore does |
|---|---|
| changed fields in a row file | writes the changed fields as an edit: a new HLC, `updated_by` = you, `updated_at` = now, so your change wins over older changes when teammates sync |
| added a new row file (named after its own id) | creates the row with that id |
| deleted a row file | deletes the row the way `lore task delete` (or the row's own delete) does: it goes to the trash, and `lore trash restore --id <id>` brings it back |
| moved a row file to another folder of the store (same id) | moves the row there, as a move with lore would |
| changed nothing but spacing, or only fields lore writes itself (`_hlc`, `updated_at`) | puts the file back as lore wrote it |

All valid changes of one folder become one commit authored by you: your git identity for that clone, or, in a store whose commits your org's identity signs, your lore identity (`lore whoami`) as author with the org identity as committer, so no personal git e-mail lands in an org store. The subject is `hand edit: tasks tsk_…` for one row, `hand edit: 3 rows` with each row listed in the body for several. A move is a commit of its own, `hand move: tasks tsk_… from <folder> to <folder>`. lore prints what it committed, the file and the commit, and the store's push mode decides when it is pushed, as for every lore write; when the command that picked it up only reads, the commit waits for the next write or `lore sync` (a read never pushes). In push mode `batch` a hand edit stays a commit of its own (marked `Lore-Hand-Edit: true`): lore squashes only its own commits around it, so the pushed history keeps you as the author.

The author of a hand-edit commit is always a person, never an agent: when an agent ran the command that picked the edit up, it is named in a `Lore-Agent:` trailer.

lore never changes the envelope fields from your file (`_hlc`, `created_by`, `updated_by`, `updated_at`, `_o`): the write stamps them anew. Metadata (`meta`) changed by hand is refused; change it with lore's metadata commands.

A row file deleted by hand goes to the trash:

<!-- example: files#delete-a-row-file-by-hand -->
```console
# alice, in .lore/stores/acme/notes
$ git rm -q _store/memories/mem_54fe…28f2.json && git status --short
D  _store/memories/mem_54fe…28f2.json

# alice, in website
$ lore memory list --store notes
lore: store notes: 2 local commit(s) wait to be pushed; a read never pushes: run `lore sync` (or the next lore write here pushes them)
lore: picked up 1 row edited by hand in store notes: committed df3a55c "hand edit: memories mem_54fe…28f2" in ~/.lore/stores/acme/notes
  deleted memories mem_54fe…28f2 (moved to the trash; lore trash restore --id mem_54fe…28f2 brings it back): ~/.lore/stores/acme/notes/_store/memories/mem_54fe…28f2.json
(no memories)
$ lore trash list
mem_54fe…28f2  memories  "The staging database is reset every Sund…"  2026-11-02 10:02  by alice  bat_b4f4…a5b0
1 row in the trash
```
<!-- /example -->

## When a file is refused

A file that is not a valid row is named with its line and the exact reason, followed by the next step:

<!-- example: files#a-refused-hand-edit -->
```console
# alice, in .lore/stores/acme/notes
$ sed -i 's/"status": "todo"/"status": "finished"/' _store/tasks/tsk_2ad3…f891.json

# alice, in website
$ lore task list --store notes
lore: hand edit refused: ~/.lore/stores/acme/notes/_store/tasks/tsk_2ad3…f891.json:28: tasks: field status: want one of todo, in_progress, done, cancelled, blocked
  it is not committed or pushed until the file is fixed (or put back: git -C ~/.lore/stores/acme/notes checkout -- _store/tasks/tsk_2ad3…f891.json); the other edits went ahead
○ tsk_2ad3…f891 Renew the TLS certificate [high]  [notes]

# alice, in .lore/stores/acme/notes
$ git checkout -- _store/tasks/tsk_2ad3…f891.json && git status --short
```
<!-- /example -->

The file stays exactly as you left it. Your other valid edits are committed and pushed in the same command. While the refused file waits, a sync sets it aside for its merge and puts it back afterwards, so a teammate's change never overwrites it and it never stops the sync. `lore doctor` lists every row file still changed by hand and not committed. Fix the file and run any lore command again.

## Plain git in the store

lore sets up each store clone so plain git behaves:

| Where | Entry | Why |
|---|---|---|
| `.git/config` | `merge.lore.name`, `merge.lore.driver = lore merge-driver %O %A %B %P` | a row both sides changed merges field by field, as `lore sync` merges it |
| `.git/info/attributes` | `*.json merge=lore` | row files use that driver (never git's line merge, which can break a JSON row) |
| `.git/hooks/pre-commit` | a block between `# >>> lore store check` and `# <<< lore store check` | before your commit, lore checks every staged row file (valid JSON, a row of the table and id its path names, valid fields) and scans the staged files for secrets; a problem refuses the commit with the file and line |
| `.git/config` | `core.hooksPath = <clone>/.git/hooks`, only when your global or system git config sets `core.hooksPath` | a global hooks folder would make git skip the clone's own hook |
| `.git/config` | `user.name`, `user.email`: your identity for this store (your name and email in the store's org, `orgs.<org>.identity`; for a store of no org, your lore identity from `lore whoami`) | your own `git commit` in the clone carries the identity lore signs that store's commits with, not your global git identity |

lore writes `user.name` and `user.email` when it clones the store, or when it opens a clone that has no `user.email` of its own, and prints one line naming the clone, the name and email and why. It remembers the email it wrote in `.git/lore/identity` and keeps only that value current (say your email in the org changes); an email you set yourself in the clone is never changed. Your global git config is never touched. `lore doctor` warns (`store_identity`) about a clone whose `git config user.email` is not your identity for that store and prints the exact `git -C <clone> config user.email …` line; `lore doctor --fix` writes it. `lore where` lists both entries for each clone.

Lines of your own in `.git/hooks/pre-commit` are kept around lore's block. lore's own commits in the clone never run that hook and never use the driver (it validated them before writing, and it merges rows itself during `lore sync`).

So in the store clone:

- `git pull` merges teammates' changes with lore's driver;
- `git commit` runs lore's check: a broken row or a secret is refused before it is committed;
- `git push` sends your commits; the next lore command reads them like any other.

In the store clone, `git commit` of a row with a secret in it is refused before anything is committed:

<!-- example: files#plain-git-in-the-store -->
```console
# alice, in .lore/stores/acme/notes
$ git config --get merge.lore.driver; cat .git/info/attributes; git config user.email
lore merge-driver %O %A %B %P
*.json merge=lore
alice@agency.example
$ sed -i 's/Renew the TLS certificate/Renew the TLS certificate (key AKIA''IOSFODNN7EXAMPLE)/' _store/tasks/tsk_2ad3…f891.json && git add _store/tasks/tsk_2ad3…f891.json && git commit -qm 'note the key'
ERROR: [E_LEAK_BLOCKED] blocked, this commit carries a secret:
  _store/tasks/tsk_2ad3…f891.json:30 (field title) secret: rule aws_access_key match sha256:1a5d44a2dca1
      not a secret? lore secrets allow --row-id tsk_2ad3…f891 --field title --rule aws_access_key --match-hash sha256:1a5d44a2dca1 --reason "<why>"
  hint: store the value in your vault and reference it (${secret:…}, or a placeholder such as ${DB_PASSWORD}); if a finding is not a secret, run the `not a secret?` line printed under it with your reason (a .gitleaksignore <path>:<rule>:<line> entry works too) (a store has no .lore/data: lore writes and commits the entry itself, as <folder>/secret_allows/<id>.json), then commit again (no --no-verify needed)
(exit 1)
$ git reset -q -- _store/tasks/tsk_2ad3…f891.json && git checkout -- _store/tasks/tsk_2ad3…f891.json && git status --short
```
<!-- /example -->

While a `git pull` (merge), rebase, cherry-pick, revert or bisect of yours is unfinished in a store clone, lore leaves that clone alone: it never aborts or resets it, a read does not pick up hand edits there and says so in one line, a row file the merge left unmerged (conflict markers inside its values) is read as your last commit has it and is not reported as a broken file, and a write or `lore sync` that would touch the clone is refused with `store clone <path> is in the middle of a git merge` and the commands that end it (`git add` the files and `git commit`, or `git merge --abort`). Finish it, then run the lore command again.

## Files that are not rows

A note (`notes.txt`), a folder of your own or any other file that is not a row is left alone: lore never deletes it and never reads it. `lore doctor` names such files in each store clone so you can keep them or move them out. Do not commit them: a store may hold only lore's files (plus `README*`, `LICENSE*`, `.gitattributes` and lore's GitHub workflows), and lore refuses an org store whose branch holds anything else (`E_NOT_A_STORE`).

## Code repos

In a code repo the rows live in `.lore/data/`, and lore never commits there: you commit rows with your code. A hand edit of a committed row (fields changed, its `_hlc` left as committed) is picked up the same way: checked, re-stamped with your name and the time, and written back into the file for your next commit; a refused file is named with its line and reason. New and deleted row files in a code repo keep their usual handling: the repo's pre-commit hook checks a new file when you commit it, and a missing file is reported by `lore doctor` and comes back with git (`git checkout HEAD -- <file>`) ([Hooks and the merge driver](hooks-and-merge-driver.md), [Trash, restore from git and purge](trash-and-purge.md)).

## Try it

The example section `files` makes a git store, edits, refuses and deletes row files by hand, and commits with plain git: [`files`](../../tests/docs/sections/files.sh) ([output](../../tests/docs/transcripts/files.txt)).

## Related

- [Stores as plain folders](store-clones.md)
- [Edit a row in your editor](../knowledge/editing-rows.md)
- [Where lore keeps things](../operations/where-lore-keeps-things.md)
- [Hooks and the merge driver](hooks-and-merge-driver.md)
