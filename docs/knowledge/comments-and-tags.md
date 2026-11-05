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
$ lore comment add --on-table decisions --on-id dec_76bd…3f10 --body 'The client confirmed this on the call.'
✓ comment cmt_71f0…c07d on decisions/dec_76bd…3f10 
lore live: (live, pushed)
lore live: landed on main (601d285); leaves live once main is fetched
```
<!-- /example -->

Comments are *live* by default: lore shares them with everyone at once on a hidden list on the git server that is not a branch (git calls it a *ref*), and because a comment counts as finished when you write it, lore copies it to main straight away. The `lore live:` lines say that this happened. Pass `--no-live` to keep a comment with your branch instead.

## Step 2 — Tag a row

Create the tag with `lore tag add --name`, then attach it. Tag names are lowercase words joined with dashes (`payments`, `release-notes`).

<!-- example: knowledge#tag -->
```console
# alice, in website
$ lore tag add --name payments
✓ tag payments tag_ada3…c018
$ lore tag attach --tag payments --on-table decisions --on-id dec_76bd…3f10
✓ tag "payments" attached to decisions/dec_76bd…3f10
$ lore tag list
payments             tag_ada3…c018  (1 uses)
staging              tag_05e9…4603  (1 uses)
$ lore tag detach --tag payments --on-table decisions --on-id dec_76bd…3f10
✓ detached 1 tag binding(s)
```
<!-- /example -->

`(1 uses)` counts the rows that carry the tag. `lore tag detach` takes the label off again; the tag itself stays, ready to be used again.

## Try it: read the comments on a row

`lore comment list` with `--on-table` and `--on-id` shows the comments on one row; with no flags it lists every comment in the project.

<!-- example: knowledge#comment only=2 -->
```console
# alice, in website
$ lore comment list --on-table decisions --on-id dec_76bd…3f10
cmt_71f0…c07d on decisions/dec_76bd…3f10  The client confirmed this on the call.
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
