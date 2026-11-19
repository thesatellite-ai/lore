# WP — polish of a linked repo's answers

**Who.** Ada (made-up names) is the admin of the org `acme`, whose own repos match `github.com/acme-co/*`. Her repo `~/code/web` holds no `.lore/`: it is linked to the store folder `acme/web` with `lore org link --own`, the shape the owner's own repos have.

**Why.** The owner's second use pass found answers that were right but weak in such a repo: `context --for-file main.go` returned notes whose only match was the word "main", a write named the folder `acme/web` on one line and the bare `acme` on the next, `lore sync` named one push as three stores with a count that was not git's, the context budget warning's fix named a row it had not listed, `policy set` without `--yes` asked "policy set?" (and its `--yes` answer was then refused), and `trash list --store` printed the deleter's raw `act_` id.

**Test.** `TestStoryWPPolish` (tests/stories/WP_polish_test.go), about 10 s; the budget warning is a unit test (`TestOverCoreFixNamesTheListedRows`).

## Steps

1. Ada starts the team in `~/code/platform`, adds the `own:` pattern and links `~/code/web` to `acme/web` with `--own`.
2. In `web`: two memories (one about the branch main, one about invoices); `lore context --for-file main.go`, `cmd/main.go` and `billing/invoice.go`.
3. In `web`: `lore rule add …`, then `lore sql --query "select distinct place from rules_all"`.
4. In `web`: a memory to `acme/org`, then `lore sync`; the store remote's commit count is read before and after.
5. In `web`: `lore policy set --key context.budget.internal --value 22000` (no `--to`, no `--yes`), then with `--to acme`, then the printed command.
6. In `web`: a memory added and deleted; from the home folder `lore trash list --store acme/web`.

## What must hold

- A file's generic stem or folder (main, index, init, util, test, src …) matches no note; a meaningful word of the path still does (`words billing invoice`).
- A write names its place as `acme/web` on every line: `committed locally to store acme/web` and `lore: wrote … (store acme/web)`, never `(store acme)`; sql's `place` is `acme/web`.
- One push of several folders of a store clone is one line, `lore: pushed store acme (the store clone: folders org, web): commit … to branch main (N commits, M row files)`, and N is the number of commits `git rev-list` counts on the remote.
- `policy set` asks only for a write that can happen: in the linked repo the default `--to repo` is refused (`E_STORE_READ_ONLY`, naming `--to acme`) before any question; with `--to acme` the question names `store acme/web`, the value now and the new one, `every repo linked to acme/web`, and the exact `--yes` command, which then works.
- `trash list --store acme/web` from the home folder names the deleter (`by @…`), never `by act_…`.
- The over-budget warning's fix names the largest pinned rule and hotfix it listed.

## Findings it guards

- `devco:D-599`..`devco:D-604` (REGRESSIONS.md, owner use pass 2, weak 4-9).
