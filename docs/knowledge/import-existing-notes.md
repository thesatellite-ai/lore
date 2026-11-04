# Bring in notes you already have

**In short:** if your project already has notes in markdown files (a `CLAUDE.md`, an `AGENTS.md`, a notes folder), `lore learn-from docs` reads them and cuts them into small pieces. You then look at each piece and keep it or drop it. Nothing becomes a real note until you say so.

## What it is and when you want it

Many repos already have a notes file that grew over months. Copying each line into `lore rule add` by hand is slow. `lore learn-from docs` does the cutting for you:

- each file you give it is saved whole as one *snapshot* (a dated copy, so nothing is lost);
- each section under a heading becomes one *candidate*: a proposed note waiting in a review tray.

You go through the tray with `lore learn list`, keep a candidate with `lore learn promote`, and drop it with `lore learn reject`. This is the moment to tidy up: keep what is still true, drop the rest.

## Before you start

- A repo with lore set up: see [Your first project](first-project.md).
- <!-- ds:block id=learn-from-default-paths-9c6aqzqm --> The markdown files you want to read in, inside the repo. With no `--paths`, lore reads `CLAUDE.md`, `AGENTS.md`, `README.md` and every `.ai/**/*.md`.

## Step 1 — Look at the file

Here the repo has a small notes file with two sections:

<!-- example: knowledge#import only=1 -->
```console
# alice, in website
$ cat .ai/notes.md
# Team notes
## Testing
Run the tests before you push.
## Payments
We use Stripe for payments.
```
<!-- /example -->

## Step 2 — Read it in

`--paths` names the files. The output counts the snapshot and the candidates, and prints the job id (`lcd_…`) of this import.

<!-- example: knowledge#import only=2 lines=3 -->
```console
# alice, in website
$ lore learn-from docs --paths .ai/notes.md
✓ learn-from docs complete
  snapshots:  1
  candidates: 3 (job_id=lcd_ada3…c018)
…
```
<!-- /example -->

## Step 3 — Review the tray

`lore learn list` shows the pending candidates, each with the kind it would become (`→ memories`) and its first words.

<!-- example: knowledge#import only=3 -->
```console
# alice, in website
$ lore learn list
[pending] lcd_78c8…5b6c → memories   # Team notes  
[pending] lcd_d7c7…7fe6 → memories    Run the tests before you push.  
[pending] lcd_06b1…3d1c → memories    We use Stripe for payments. 
```
<!-- /example -->

The first candidate is the file's title line with nothing under it; you will drop that one.

## Step 4 — Keep or drop each candidate

`promote` turns a candidate into a real memory. `reject` drops it. `lore learn list --status ''` shows every candidate, whatever its state (`pending`, `accepted`, `rejected` or `expired`).

<!-- example: knowledge#promote only=2,3,4 -->
```console
# alice, in website
$ lore learn promote lcd_06b1…3d1c
✓ promoted lcd_06b1…3d1c → memories
$ lore learn reject lcd_78c8…5b6c
lore sync: imported 1
✓ rejected 1 candidate(s)
$ lore learn list --status ''
[rejected] lcd_78c8…5b6c → memories   # Team notes  
[pending] lcd_d7c7…7fe6 → memories    Run the tests before you push.  
[accepted] lcd_06b1…3d1c → memories    We use Stripe for payments. 
```
<!-- /example -->

## Try it: see the new memory

<!-- example: knowledge#promote only=5 -->
```console
# alice, in website
$ lore memory list
mem_54fe…28f2 [retrieved] The client replies faster on WhatsApp than email, even on weekends
mem_c0f3…b5b8 [retrieved] The staging site is reset every Monday morning
mem_b4f4…a5b0 [retrieved] The office printer is on the second floor
mem_7289…c813 [retrieved] 
We use Stripe for payments.
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
[off] Team wiki (wiki) ext_4bfb…19ce
$ lore external-source enable ext_4bfb…19ce && lore external-source list
✓ external-source ext_4bfb…19ce enabled
[on] Team wiki (wiki) ext_4bfb…19ce
$ lore external-source disable ext_4bfb…19ce
✓ external-source ext_4bfb…19ce disabled
```
<!-- /example -->

## Pitfalls

- **Only memories for now.** `lore learn promote --target rules` (or any kind other than `memories`) stops with `E_NOT_IMPLEMENTED`. Promote the candidate as a memory; if it should be a rule, add the rule by hand with `lore rule add` and reject the candidate.

<!-- example: knowledge#promote only=1 -->
```console
# alice, in website
$ lore learn promote lcd_d7c7…7fe6 --target rules
ERROR: [E_NOT_IMPLEMENTED] promote target "rules" not yet supported in v0.1
  hint: only --target=memories is implemented
(exit 1)
```
<!-- /example -->

- **The heading is not kept.** A promoted memory holds the section's text, not its heading, and it starts with an empty line. Fix the text with `lore memory edit <id> --body '…'` if the list looks odd.
- **Title lines become candidates too.** A top `# Title` line with no text under it is a candidate of its own. Reject it.
- **`external-source` is a list only.** Registering or enabling a source does not read anything yet: `lore learn-from` reads only `docs` (markdown files in the repo). Export the wiki page to markdown and pass it with `--paths`.
- **Running it again is safe.** Reading the same, unchanged file a second time adds no new candidates (`candidates: 0`), so you can import a whole folder again after you add one file.
- **Commit afterwards.** Snapshots and promoted memories are rows like any other and need a commit of `.lore/data`.

## Related

- [Your first project](first-project.md)
- [Rules, memories and decisions](rules-memories-decisions.md): turn a promoted memory into a rule or decision.
- [Moving an existing repo onto lore](../MIGRATE_TO_LORE.md): the full checklist for a repo with years of notes.
- [Knowledge guides index](README.md)
