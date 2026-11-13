# Other kinds of notes

**In short:** besides rules, memories and decisions, lore has fourteen more kinds of note: loud warnings (hotfixes), how-tos (playbooks), style choices ("Buttons are blue") and more. Pick the kind that says what the note is for. They all work the same way: `lore <kind> add`, then `list`, `show`, `search` and `delete`.

## What they are and when you want them

You can write almost anything as a memory. A more exact kind helps in two ways: `lore search --tables <kind>` finds just that kind, and some kinds get special treatment (a `high` or `critical` hotfix is pinned into the file your coding tool reads).

| Kind | Command | Id prefix | Think of it as | Example |
|---|---|---|---|---|
| Hotfix | `lore hotfix` | `hfx_` | a red warning sign, shown until the problem is fixed | "Checkout button is broken on Safari" |
| Pattern | `lore pattern` | `pat_` | a model answer you want copied | "Each page loads its data in one place" |
| Playbook | `lore playbook` | `pbk_` | a recipe card with numbered steps | "How to release" |
| Prompt | `lore prompt` | `prm_` | a text you paste into a tool again and again | "Review a pull request" |
| Snapshot | `lore snapshot` | `snp_` | a dated photo of where the project stands | "Week 44: checkout works, search half done" |
| Incident | `lore incident` | `inc_` | an accident report: what broke and what you did | "Site down for 20 minutes" |
| Behaviour | `lore behaviour` | `bhv_` | house manners for a tool or agent | "Ask before you delete a file you did not create" |
| Architecture note | `lore architecturenote` | `anr_` | a map of how the parts fit together | "The website reads products from the shop API" |
| Cookbook recipe | `lore cookbookrecipe` | `cbr_` | a short answer to a common task | "Add a new page" |
| Suggestion | `lore suggestion` | `sug_` | an idea in the suggestion box, not yet accepted | "Add dark mode" |
| Taste preference | `lore tastepref` | `tst_` | a style choice | "Buttons are blue" |
| Tech doc | `lore techdoc` | `tdc_` | a bookmark to outside documentation | "Payments docs" |
| Workflow | `lore workflow` | `wfl_` | the named steps of a process | "Code review: one approval before merge" |
| Workspace | `lore workspace` | `wsp_` | a folder label for a stream of work | "Website redesign" |

Most kinds take `--title` and `--body`. Two are different: `tastepref add` takes only `--body`, and `techdoc add` takes `--name`, `--base-url` and `--description` instead. Playbooks and prompts also take an optional `--description`, a one-line summary that defaults to the title.

## Before you start

- A repo with lore set up: see [Your first project](first-project.md).

## Step 1 — Add a hotfix

A hotfix has a `--severity`: `low`, `medium`, `high` (the default) or `critical`. `high` and `critical` hotfixes are pinned into the rendered file, so every session sees them first.

<!-- example: knowledge#hotfix -->
```console
# alice, in website
$ lore hotfix add --title 'Checkout button is broken on Safari' --body 'Use the old button until the fix ships.' --severity critical
lore: wrote ~/work/website/.lore/data/hotfixes/hfx_f147…5139.json
⚠ hfx_f147…5139 — Checkout button is broken on Safari [critical]
$ lore hotfix list
hfx_f147…5139 [critical] Use the old button until the fix ships.
```
<!-- /example -->

When the problem is fixed, archive the hotfix (`lore hotfix archive --id <id>`) so it stops showing.

## Step 2 — Add the other kinds

Each kind is one `add` command. Here are the ones with a title and a body:

<!-- example: knowledge#pattern -->
```console
# alice, in website
$ lore pattern add --title 'Load data in one place' --body 'Each page loads its data in its loader, never inside a button handler.'
lore: wrote ~/work/website/.lore/data/patterns/pat_2537…6bbb.json
✓ pat_2537…6bbb
```
<!-- /example -->

<!-- example: knowledge#playbook -->
```console
# alice, in website
$ lore playbook add --title 'How to release' --body '1. Run the tests. 2. Tag the release. 3. Post in the team chat.'
lore: wrote ~/work/website/.lore/data/playbooks/pbk_b69e…5ed7.json
✓ pbk_b69e…5ed7
```
<!-- /example -->

A playbook and a prompt also keep a one-line summary. `--description` sets it; without it, the title is the summary:

<!-- example: knowledge#prompt -->
```console
# alice, in website
$ lore prompt add --title 'Review a pull request' --description 'Bugs first, then style' --body 'Read the change, list bugs first, then style notes.'
lore: wrote ~/work/website/.lore/data/prompts/prm_0aae…a61f.json
✓ prm_0aae…a61f
```
<!-- /example -->

A snapshot is stamped with today's date, which `lore snapshot list` shows. `--source` says where it came from (`manual`, the default, or `learn-from`, `agent-proposal`, `plugin`, `imported`, `migrated`):

<!-- example: knowledge#snapshot -->
```console
# alice, in website
$ lore snapshot add --title 'Week 44' --body 'Checkout works. Search is half done. Demo on Friday.'
lore: wrote ~/work/website/.lore/data/snapshots/snp_6a86…ed8c.json
✓ snp_6a86…ed8c — Week 44
$ lore snapshot list
snp_6a86…ed8c Week 44   (2026-11-02)
```
<!-- /example -->

<!-- example: knowledge#incident -->
```console
# alice, in website
$ lore incident add --title 'Site down for 20 minutes' --body 'The disk filled up with logs. We now rotate logs daily.'
lore: wrote ~/work/website/.lore/data/incidents/inc_4f89…b64b.json
✓ inc_4f89…b64b
```
<!-- /example -->

<!-- example: knowledge#behaviour -->
```console
# alice, in website
$ lore behaviour add --title 'Ask before deleting' --body 'Ask before you delete any file you did not create.'
lore: wrote ~/work/website/.lore/data/behaviours/bhv_77cb…76c4.json
✓ bhv_77cb…76c4
```
<!-- /example -->

<!-- example: knowledge#architecturenote -->
```console
# alice, in website
$ lore architecturenote add --title 'How the site is built' --body 'The website reads products from the shop API and caches them for 5 minutes.'
lore: wrote ~/work/website/.lore/data/architecture_notes/anr_d191…45c2.json
✓ anr_d191…45c2
```
<!-- /example -->

<!-- example: knowledge#cookbookrecipe -->
```console
# alice, in website
$ lore cookbookrecipe add --title 'Add a new page' --body 'Copy pages/about, rename it, add a link in the menu.'
lore: wrote ~/work/website/.lore/data/cookbook_recipes/cbr_b4d3…11aa.json
✓ cbr_b4d3…11aa
```
<!-- /example -->

<!-- example: knowledge#suggestion -->
```console
# alice, in website
$ lore suggestion add --title 'Add dark mode' --body 'Several users asked for it.'
lore: wrote ~/work/website/.lore/data/suggestions/sug_9a34…b418.json
✓ sug_9a34…b418
```
<!-- /example -->

<!-- example: knowledge#workflow -->
```console
# alice, in website
$ lore workflow add --title 'Code review' --body 'One approval from a second developer before merge.'
lore: wrote ~/work/website/.lore/data/workflows/wfl_67e2…291e.json
✓ wfl_67e2…291e
```
<!-- /example -->

<!-- example: knowledge#workspace -->
```console
# alice, in website
$ lore workspace add --title 'Website redesign' --body 'Everything for the new look of the site.'
lore: wrote ~/work/website/.lore/data/workspaces/wsp_8567…366f.json
✓ wsp_8567…366f
```
<!-- /example -->

## Step 3 — Add a taste preference and a tech doc

<!-- example: knowledge#tastepref -->
```console
# alice, in website
$ lore tastepref add --body 'Buttons are blue'
lore: wrote ~/work/website/.lore/data/taste_prefs/tst_bfed…eeea.json
✓ tst_bfed…eeea
```
<!-- /example -->

<!-- example: knowledge#techdoc -->
```console
# alice, in website
$ lore techdoc add --name 'Payments docs' --base-url https://docs.example.com/payments --description 'How to take a card payment'
lore: wrote ~/work/website/.lore/data/tech_docs/tdc_376c…8e1a.json
✓ tdc_376c…8e1a Payments docs
```
<!-- /example -->

## Step 4 — Read, change and search one kind

Every kind has `show`, `search` and `delete`; most have `edit` too. `lore <kind> show <id>` prints a two-letter tag and the id, the title or name, any references and the body; `lore show --id <id>` prints every field of any row. `lore <kind> edit <id>` changes only the flags you give, and `lore <kind> search <words>` searches that kind alone.

<!-- example: knowledge#other-kinds-show-edit -->
```console
# alice, in website
$ lore pattern show --id pat_2537…6bbb
PA  pat_2537…6bbb
  title: Load data in one place
Each page loads its data in its loader, never inside a button handler.
created 2026-11-02T10:03:10Z by alice · updated 2026-11-02T10:03:10Z by alice
$ lore pattern edit --id pat_2537…6bbb --body 'Each page loads its data in its loader, never inside a button handler or an effect.'
lore: wrote ~/work/website/.lore/data/patterns/pat_2537…6bbb.json
✓ pattern pat_2537…6bbb updated
$ lore pattern search --query loader
pat_2537…6bbb Load data in one place
```
<!-- /example -->

`lore incident edit` also takes `--severity` and `--ref <id> --relation <name>`, to link the incident to the row that fixed or caused it. The link shows in `lore incident show`:

<!-- example: knowledge#incident-edit -->
```console
# alice, in website
$ lore incident edit --id inc_4f89…b64b --severity high --ref pat_2537…6bbb --relation fixed_by
lore: wrote ~/work/website/.lore/data/incidents/inc_4f89…b64b.json
lore: wrote ~/work/website/.lore/data/knowledge_refs/kfr_71f0…c07d.json
✓ inc_4f89…b64b updated
$ lore incident show --id inc_4f89…b64b
IN  inc_4f89…b64b
  title: Site down for 20 minutes
  fixed_by patterns/pat_2537…6bbb
The disk filled up with logs. We now rotate logs daily.
created 2026-11-02T10:03:35Z by alice · updated 2026-11-02T10:04:40Z by alice
```
<!-- /example -->

To read the whole incident, use `lore show`, which works for any id:

<!-- example: knowledge#show-any-row only=1 -->
```console
# alice, in website
$ lore show --id inc_4f89…b64b
inc_4f89…b64b  incidents  repo  "Site down for 20 minutes"
  body: The disk filled up with logs. We now rotate logs daily.
  created_by_actor_id: act_ada3…c018
  project_id: prj_78c8…5b6c
  severity_str: high
  title: Site down for 20 minutes
created 2026-11-02T10:03:35Z by alice · updated 2026-11-02T10:04:40Z by alice
```
<!-- /example -->

## Step 5 — Archive and bring back

Hotfixes, patterns, playbooks, prompts and snapshots can be archived: the row stays, but `list` and render skip it. `unarchive` brings it back.

<!-- example: knowledge#other-kinds-archive -->
```console
# alice, in website
$ lore snapshot archive --id snp_6a86…ed8c
lore: wrote ~/work/website/.lore/data/snapshots/snp_6a86…ed8c.json
✓ snp_6a86…ed8c archived
$ lore snapshot list
(no snapshots)
$ lore snapshot unarchive --id snp_6a86…ed8c
lore: wrote ~/work/website/.lore/data/snapshots/snp_6a86…ed8c.json
✓ snp_6a86…ed8c unarchived
```
<!-- /example -->

## Try it: list them

<!-- example: knowledge#other-kinds-listed -->
```console
# alice, in website
$ lore incident list; lore tastepref list; lore techdoc list
IN  inc_4f89…b64b  Site down for 20 minutes
TA  tst_bfed…eeea  Buttons are blue
tdc_376c…8e1a Payments docs https://docs.example.com/payments
```
<!-- /example -->

## Pitfalls

- **Some lists show only the id.** `lore incident list` and `lore tastepref list` print a two-letter tag and the id, not the text. Use `lore <kind> show <id>` or `lore search` to read them.
- **`hotfix list` shows the body, not the title.** Keep the first words of the body clear.
- **Not every kind has `edit` or `archive`.** Snapshots and tech docs have no `edit`; only rules, memories, decisions, hotfixes, patterns, playbooks, prompts and snapshots have `archive`. Run `lore <kind> --help` to see the verbs.
- **`<kind> show` is short on purpose.** For patterns, playbooks, prompts, behaviours, architecture notes, cookbook recipes, suggestions, taste preferences, workflows and workspaces it prints the tag and the id only (the 0.1.17 shape scripts depend on). Use `lore show --id <id>` or `--json` to read the title and body.
- **Only rules and hotfixes are pinned.** The other kinds reach the coding tool through `lore search`, not through the rendered file. See [Render](render-and-agent-files.md).

## Related

- [Rules, memories and decisions](rules-memories-decisions.md): change, replace, retire and delete rows.
- [Comments and tags](comments-and-tags.md)
- [Search](search.md): `--tables` to search one kind.
- [Tasks](../tasks/README.md): tasks, missions and plans are their own guides.
- [Knowledge guides index](README.md)
