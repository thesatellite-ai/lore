# Comments and tags

**In short:** a *comment* is a short reply you attach to any note, like a sticky note on a document ("The client confirmed this on the call"). A *tag* is a label, like a coloured dot on a folder, that groups notes so you can find them together. Both work on any kind of row.

## What they are and when you want them

- Use a **comment** when you want to add context to a row without changing it: who agreed, what changed since, a question. The row stays as it was; the comment sits next to it.
- Use a **tag** when several rows belong together, for example everything about payments. You create the tag once, then attach it to as many rows as you like.

Both point at a row by its *table* (the kind of row, in the plural: `rules`, `memories`, `decisions`, `tasks`, …) and its id.

## Before you start

- A repo with lore set up and at least one row: see [Your first project](first-project.md) and [Rules, memories and decisions](rules-memories-decisions.md).

## Step 1 — Comment on a decision

`--on-table` names the kind of row and `--on-id` its id. The body is the comment text.

<!-- example: knowledge#comment only=1 -->
```console
# alice, in website
$ lore comment add --on-table decisions --on-id dec_d431…313a --body 'The client confirmed this on the call.'
lore: wrote ~/work/website/.lore/data/comments/cmt_06b1…3d1c.json
✓ comment cmt_06b1…3d1c on decisions/dec_d431…313a 
lore live: (live, pushed) commit 86dbae4 to origin refs/lore/live
lore live: landed on main (9b40ce7); leaves live once main is fetched
```
<!-- /example -->

Comments are *live* by default: lore shares them with everyone at once on a hidden list on the git server that is not a branch (git calls it a *ref*), and because a comment counts as finished when you write it, lore copies it to main straight away. The `lore live:` lines say that this happened. Pass `--no-live` to keep a comment with your branch instead.

## Step 2 — Tag a row

Create the tag with `lore tag add --name`, then attach it. Tag names are lowercase words joined with dashes (`payments`, `release-notes`).

<!-- example: knowledge#tag -->
```console
# alice, in website
$ lore tag add --name payments
lore: wrote ~/work/website/.lore/data/tags/tag_7289…c813.json
✓ tag payments tag_7289…c813
$ lore tag attach --tag payments --on-table decisions --on-id dec_d431…313a
lore: wrote ~/work/website/.lore/data/decisions/dec_d431…313a.json
✓ tag "payments" attached to decisions/dec_d431…313a
$ lore tag list
payments             tag_7289…c813  (1 uses)
staging              tag_c0f3…b5b8  (1 uses)
$ lore tag detach --tag payments --on-table decisions --on-id dec_d431…313a
lore: wrote ~/work/website/.lore/data/decisions/dec_d431…313a.json
✓ detached 1 tag binding(s)
```
<!-- /example -->

`(1 uses)` counts the rows that carry the tag. `lore tag detach` takes the label off again; the tag itself stays, ready to be used again.

## Step 3 — Search and delete comments

`lore comment search <words>` searches the text of every comment. `lore comment delete <id> --yes` moves a comment to the trash; `lore trash restore <id>` brings it back.

<!-- example: knowledge#comment only=3,4 -->
```console
# alice, in website
$ lore comment search confirmed
cmt_06b1…3d1c [decisions/dec_d431…313a] The client confirmed this on the call.
$ lore comment delete cmt_06b1…3d1c --yes
lore: wrote ~/work/website/.lore/data/comments/cmt_06b1…3d1c.json
✓ cmt_06b1…3d1c moved to the trash (restore: lore trash restore cmt_06b1…3d1c)
lore live: (live, pushed) commit 05513aa to origin refs/lore/live
lore live: landed on main (dfd8dc8); leaves live once main is fetched
```
<!-- /example -->

## Step 4 — Rename a tag or give it files

`lore tag edit <tag>` changes a tag's `--name`, `--color` (`#RRGGBB`) or `--globs`, the files the topic covers (comma separated; `-` clears it). Tagged rows keep the tag, because they point at its id, not its name. You can name the tag by its name or its `tag_` id.

<!-- example: knowledge#tag-edit -->
```console
# alice, in website
$ lore tag edit payments --name billing --color '#1f6feb' --globs 'src/checkout/**'
lore: wrote ~/work/website/.lore/data/tags/tag_7289…c813.json
✓ tag billing tag_7289…c813 (globs src/checkout/**)
$ lore tag list
billing              tag_7289…c813  (0 uses)
staging              tag_c0f3…b5b8  (1 uses)
```
<!-- /example -->

## Try it: read the comments on a row

`lore comment list` with `--on-table` and `--on-id` shows the comments on one row; with no flags it lists every comment in the project.

<!-- example: knowledge#comment only=2 -->
```console
# alice, in website
$ lore comment list --on-table decisions --on-id dec_d431…313a
cmt_06b1…3d1c on decisions/dec_d431…313a  The client confirmed this on the call.
```
<!-- /example -->

## Pitfalls

- **The table name is plural.** `--on-table decisions`, not `decision`. The names are the folder names under `.lore/data`.
- **A comment goes to main at once.** Because comments are live and count as finished on write, lore lands them on main without waiting for your next commit (see the `landed on main` line above). Write comments as if the whole team can read them immediately, because it can.
- **Tags belong to one project.** A tag made in one repo's project does not exist in another; create it there too.
- **Two kinds of tag.** `memory add --tag` writes a label on the memory itself; `lore tag` makes a shared label any row can carry. Use `lore tag attach` when you want `lore tag list` to see it. In the example on this page, the memory tagged `staging` while it was added does not appear in `lore tag list`.

## Related

- [Rules, memories and decisions](rules-memories-decisions.md)
- [Search](search.md)
- [Live rows](../team/concepts.md): what "live" means and how rows land on main.
- [Knowledge guides index](README.md)
