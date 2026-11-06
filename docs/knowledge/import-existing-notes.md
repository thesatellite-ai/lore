# Bring in notes you already have

**In short:** if your project already has notes in markdown files (a `CLAUDE.md`, an `AGENTS.md`, a notes folder), turn each piece that is still true into a lore row of the right kind: a rule, a decision, a memory. Then delete the old file, so there is one place for notes. lore 0.1.18 has no automatic importer; this page shows the hand-made way, which is also the moment to tidy up.

## What it is and when you want it

Many repos already have a notes file that grew over months. Some lines are hard rules, some are choices with a reason, some are just facts, and some are no longer true. lore keeps each kind apart, so an agent knows a `must` rule from a passing remark. Going through the file once, section by section, sorts them.

## Before you start

- A repo with lore set up: see [Your first project](first-project.md).
- The markdown files you want to bring in, inside the repo.

## Step 1 — Look at the file

Here the repo has a small notes file with two sections:

<!-- example: knowledge#import only=1 -->
```console
# alice, in website
$ cat .ai/notes.md
# Team notes
## Prices
Ask the client before changing any price.
## Invoices
The client pays invoices on the first Monday of the month.
```
<!-- /example -->

## Step 2 — Turn each section into a row

"Ask the client before changing any price" is a rule everyone must follow, so it becomes a rule. "The client pays invoices on the first Monday" is a plain fact, so it becomes a memory. A choice the team made, with its reason, would be a decision (`lore decision add`). When everything is in lore, delete the file; git shows the new rows, ready to commit:

<!-- example: knowledge#promote -->
```console
# alice, in website
$ lore rule add --body 'Ask the client before changing any price.'
✓ rul_78c8…5b6c
  scope: [master] severity: must
$ lore memory add --body 'The client pays invoices on the first Monday of the month.'
✓ mem_d7c7…7fe6
  scope: [master]
$ rm .ai/notes.md && git status --short
?? .lore/data/memories/mem_d7c7…7fe6.json
?? .lore/data/rules/rul_78c8…5b6c.json
```
<!-- /example -->

## Register an outside source

`lore external-source` keeps a list of places outside the repo that hold notes, such as a team wiki. A new entry starts switched off (`[off]`); `enable` and `disable` take its id.

<!-- example: knowledge#external-source -->
```console
# alice, in website
$ lore external-source add --kind wiki --name 'Team wiki'
✓ external-source Team wiki added
$ lore external-source list
[off] Team wiki (wiki) ext_06b1…3d1c
$ lore external-source enable ext_06b1…3d1c && lore external-source list
✓ external-source ext_06b1…3d1c enabled
[on] Team wiki (wiki) ext_06b1…3d1c
$ lore external-source disable ext_06b1…3d1c
✓ external-source ext_06b1…3d1c disabled
```
<!-- /example -->

## Pitfalls

- **Pick the kind on purpose.** Only `must` rules are pinned into `.lore/LORE.md` every time; everything else is found with `lore search`. See [Rules, memories and decisions](rules-memories-decisions.md).
- **Leave out what is no longer true.** An old line copied into a rule is worse than no rule. When in doubt, ask the team, or add it as a memory instead.
- **`external-source` is a list only.** Registering or enabling a source does not read anything. Copy what you need from the wiki by hand, as above.
- **Commit afterwards.** New rows are files in `.lore/data` and need a commit, together with the deletion of the old notes file.

## Related

- [Your first project](first-project.md)
- [Rules, memories and decisions](rules-memories-decisions.md): which kind of row fits which note.
- [Moving an existing repo onto lore](../MIGRATE_TO_LORE.md): the full checklist for a repo with years of notes.
- [Knowledge guides index](README.md)
