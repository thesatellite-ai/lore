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

"Ask the client before changing any price" is a rule everyone must follow, so it becomes a rule. "The client pays invoices on the first Monday" is a plain fact, so it becomes a memory. A choice the team made, with its reason, would be a decision (`lore decision add`). When everything is in lore, delete the file; git shows the new rows and `.lore/LORE.md` and `.lore/LORE.full.md`, which each write brought up to date with them, ready to commit together:

<!-- example: knowledge#promote -->
```console
# alice, in website
$ lore rule add --body 'Ask the client before changing any price.'
lore: wrote ~/work/website/.lore/data/rules/rul_4bfb…19ce.json
✓ rul_4bfb…19ce
  scope: [project] severity: must
$ lore memory add --body 'The client pays invoices on the first Monday of the month.'
lore: wrote ~/work/website/.lore/data/memories/mem_2e0c…0b6d.json
✓ mem_2e0c…0b6d
  scope: [project]
$ rm .ai/notes.md && git status --short
 M .lore/LORE.full.md
 M .lore/LORE.md
?? .lore/data/memories/mem_2e0c…0b6d.json
?? .lore/data/rules/rul_4bfb…19ce.json
```
<!-- /example -->

## Hundreds of notes: one file, one commit per store

Every `lore memory add`, `rule edit`, `memory archive` or `meta set` is its own write, and on a store clone that is its own git commit, so a few hundred of them run one after another take a long time. For an import that size, put one write per line in a JSON-lines file and apply the whole file with `lore batch apply --file <file>`. Each line is `{"op": "<the command>", "input": {<its flags>}}`, with the flags spelled as on the command line (`to`, `body`, `id`, `pair`; `-` or `_`); blank lines and lines starting with `#` are skipped:

```text
{"op": "memory add", "input": {"to": "acme/notes", "body": "The client pays invoices on the first Monday of the month."}}
{"op": "rule add", "input": {"body": "Ask the client before changing any price."}}
{"op": "memory edit", "input": {"id": "mem_2e0c…0b6d", "body": "The client pays invoices on the first working Monday."}}
{"op": "memory archive", "input": {"id": "mem_9a1d…c3e0"}}
{"op": "meta set", "input": {"id": "mem_2e0c…0b6d", "pair": "team=billing"}}
```

Every line runs through the same checks as the command typed on its own (input rules, the secret scan, policy, roles, client folders), and the rows of each store land as one commit (one push on a store that pushes at once). Before anything runs, every line is checked: a bad line, an unknown command, or a command that cannot be batched (deletes, moves, restores, setup and roster commands, and `meta add`, whose new key the later lines could not see yet: run it first) is listed with its line number and nothing is written. A line a write refuses (a detected secret, a missing row) stops the file with nothing written; a secret answer given to the apply (`--not-a-secret --reason <why>`, `--secret-ref <ref>`, `--secret-unstored`) applies to every line. Two writes of the same row in one file are fine: what came before is written first, then the file goes on, one commit more. `--dry-run` runs every line and writes nothing. The output names the batch, and `lore batch undo --id <bat_id>` takes it back, from the same folder:

```console
$ lore batch apply --file notes.jsonl --dry-run
checked 5 line(s): every one would be written (stores: repo, acme/notes); nothing written (--dry-run)
$ lore batch apply --file notes.jsonl --yes
applied 5 line(s) in 2 store write(s) (stores: repo, acme/notes); batch bat_7c21…e04a (undo: lore batch undo --id bat_7c21…e04a)
```

## Try it: find the imported notes

Search for a word from the old file, then render: the memory is found by `lore search`, and the rule (a `must` rule by default) is pinned into `.lore/LORE.md`, which is what your coding tool reads. The `1` is the number of lines in the rendered file that hold the rule.

<!-- example: knowledge#imported -->
```console
# alice, in website
$ lore search --query invoices
Found 1 hit for "invoices" across 1 entity type:
memory  mem_2e0c…0b6d  The client pays invoices on the first Monday of the month.  (score=0.850)
$ lore render >/dev/null && grep -c 'Ask the client before changing any price' .lore/LORE.md
1
```
<!-- /example -->

## Register an outside source

`lore external-source` keeps a list of places outside the repo that hold notes, such as a team wiki. A new entry starts switched off (`[off]`); `enable` and `disable` take its id.

<!-- example: knowledge#external-source -->
```console
# alice, in website
$ lore external-source add --kind wiki --name 'Team wiki'
lore: wrote ~/work/website/.lore/data/external_sources/ext_e9fa…224c.json
✓ external-source Team wiki added
$ lore external-source list
[off] Team wiki (wiki) ext_e9fa…224c
$ lore external-source enable --id ext_e9fa…224c && lore external-source list
lore: wrote ~/work/website/.lore/data/external_sources/ext_e9fa…224c.json
✓ external-source ext_e9fa…224c enabled
[on] Team wiki (wiki) ext_e9fa…224c
$ lore external-source disable --id ext_e9fa…224c
lore: wrote ~/work/website/.lore/data/external_sources/ext_e9fa…224c.json
✓ external-source ext_e9fa…224c disabled
```
<!-- /example -->

## Pitfalls

- **Pick the kind on purpose.** Only `must` rules are pinned into `.lore/LORE.md` every time; everything else is found with `lore search`. See [Rules, memories and decisions](rules-memories-decisions.md).
- **Leave out what is no longer true.** An old line copied into a rule is worse than no rule. When in doubt, ask the team, or add it as a memory instead.
- **`external-source` is a list only.** Registering or enabling a source does not read anything. Copy what you need from the wiki by hand, as above.
- **Commit afterwards.** New rows are files in `.lore/data` and need a commit, together with the deletion of the old notes file.
- **Hundreds of rows: use one file.** One command per row is one store commit per row; `lore batch apply --file <file>` writes the whole file as one commit per store (see above).

## Related

- [Your first project](first-project.md)
- [Rules, memories and decisions](rules-memories-decisions.md): which kind of row fits which note.
- [Moving an existing repo onto lore](../MIGRATE_TO_LORE.md): the full checklist for a repo with years of notes.
- [Knowledge guides index](README.md)
