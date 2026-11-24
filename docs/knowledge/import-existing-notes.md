# Bring in notes you already have

**In short:** if your project already has notes in markdown files (a `CLAUDE.md`, an `AGENTS.md`, a notes folder), turn each piece that is still true into a lore row of the right kind: a rule, a decision, a memory. Then delete the old file, so there is one place for notes. This page shows the hand-made way, which is also the moment to tidy up; for a long technical doc that should stay where it is (a `TECHNICAL.md`, a `README.md`), `lore docs import` drafts the notes for you, marked unverified until someone checks them (see below).

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

"Ask the client before changing any price" is a rule everyone must follow, so it becomes a rule. "The client pays invoices on the first Monday" is a plain fact, so it becomes a memory. A choice the team made, with its reason, would be a decision (`lore decision add`). When everything is in lore, delete the file; git shows the new rows, ready to commit. A committed `.lore/LORE.md` is not rewritten by the writes: lore's pre-commit hook refreshes it in the commit that carries the rows:

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

## Draft notes from a technical doc: lore docs import

A `TECHNICAL.md` or `README.md` already says how the code works and why some choices were made, but an agent only sees what lore holds. `lore docs import --from <file>` (repeat `--from` for several files) reads the doc and drafts one note per section: a memory whose first line is the heading path (`widget module › Overview`) followed by the section's paragraphs and lists, so a list stays with the sentence that introduces it and nested items stay under their item. A decision with status `proposed` is drafted where a section names its choice with labelled paragraphs (`Decision.` or `Chosen:`, with `Considered.`, `Why.` or `Rationale:` beside it), titled with the section's heading and holding every alternative and reason; a paragraph that states a choice and its reason ("we chose X over Y because …") is a proposed decision of its own. A section longer than about 2,000 characters is cut into parts at paragraph breaks (`… (part 2)`). Code blocks, tables, HTML, link-only lines (a table of contents, "See [X](x)."), boilerplate sections (contents, license, contributing, code of conduct, badges, acknowledgements) and sections with too few words are left out. No model is called, so the same doc always gives the same notes.

Every draft says where it came from and that nobody has checked it yet:

- `source_kind` is `imported` and `source_ref` is the doc line (`TECHNICAL.md:5`, the path relative to the repo);
- metadata `fact_evidence` holds the doc line, then each `path:line` the text names that exists in the repo (`TECHNICAL.md:5, internal/queue/worker.go:4`);
- metadata `fact_status` is `unverified`, and `fact_verified_at` is not set.

```console
$ lore docs import --from TECHNICAL.md --dry-run
  memory (new) — Orbit › How it works  [TECHNICAL.md:5, internal/queue/worker.go:4]
  decision (new) — We chose Postgres advisory locks over Redis locks because the service already…  [TECHNICAL.md:16]
TECHNICAL.md: would draft 1 memory, 1 decision; skipped nothing
would draft 2 unverified notes from TECHNICAL.md, skip 0; nothing written (--dry-run)
$ lore docs import --from TECHNICAL.md --yes
…
drafted 2 unverified notes from TECHNICAL.md in 1 store write, skipped 0; batch bat_7c21…e04a (undo: lore batch undo --id bat_7c21…e04a)
```

The notes are written like `lore batch apply`: each runs through the checks of its own command (the secret scan, policy, roles; `--secret-ref`, `--secret-unstored` and `--not-a-secret --reason <why>` answer a finding), all of them land as one commit per store under one batch, and a note a write refuses stops the import with nothing written. `--to <store>/<folder>` writes them into a store folder; `--dry-run` runs every check and writes nothing. A section that a memory or decision of any attached store already says (archived ones too, by the same test as `lore lint`'s `near_duplicate`) is skipped and listed with the id that holds it, so importing the doc again adds nothing. Inside a section, a paragraph or list that the doc said earlier or that a stored note already says is dropped and listed, and the rest of the section is still drafted under its heading. One line per doc counts what it drafted and what it skipped by reason (already stored, repeats in this import, boilerplate sections, link-only blocks, too short), for example `TECHNICAL.md: would draft 2 memories, 2 decisions; skipped 1 already stored, 2 boilerplate sections`; `--json` has the same counts under `docs` and a `reason` on each skipped entry.

Check each draft against the code, then mark it checked; the rest stay easy to find:

```console
$ lore meta set --id mem_2e0c…0b6d --pair fact_status=verified --set fact_verified_at=2026-11-23
$ lore memory list --meta fact_status=unverified
$ lore decision list --meta fact_status=unverified
```

A draft that turned out wrong is archived like any note (`lore memory archive --id <id>`); a proposed decision that holds is accepted with `lore decision edit --id <id> --status accepted`.

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
- **Imported drafts are not facts yet.** `lore docs import` copies what the doc says, right or stale; until a note's `fact_status` is `verified`, treat it as a lead to check in the code.
- **Hundreds of rows: use one file.** One command per row is one store commit per row; `lore batch apply --file <file>` writes the whole file as one commit per store (see above).

## Related

- [Your first project](first-project.md)
- [Rules, memories and decisions](rules-memories-decisions.md): which kind of row fits which note.
- [Moving an existing repo onto lore](../MIGRATE_TO_LORE.md): the full checklist for a repo with years of notes.
- [Knowledge guides index](README.md)
