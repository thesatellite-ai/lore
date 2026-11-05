# Search

**In short:** `lore search <words>` looks through every note you have (rules, memories, decisions, tasks and the rest) and shows the best matches first. Your coding tool uses the same command to find what it needs before it answers.

## What it is and when you want it

The file your coding tool reads at start holds only the strongest rules and warnings. Everything else, such as memories, decisions and `should` rules, is found on demand with `lore search`. Think of it as the search box of a library: one box for every shelf.

Behind the box is an *index*: a list of every word and the rows it appears in, like the index at the back of a book. lore keeps it up to date as you add and change rows.

## Before you start

- A repo with lore set up and a few rows: see [Your first project](first-project.md).
- `lore setup` run once after you installed or upgraded lore (it builds the index).

## Step 1 — Search everything

Give one or more words. Each hit shows its kind, its id, the matching text with the found word between `<b>` and `</b>`, and a score (higher is better).

<!-- example: search#search -->
```console
# alice, in website
$ lore search payments
Found 2 hits for "payments" across 2 entity types:
[decision  ] dec_7a67…9504 We use Stripe for payments  (score=0.017)
[memory    ] mem_2ad3…f891 Old payments page is gone  (score=0.017)
```
<!-- /example -->

## Step 2 — Search one kind

`--tables` keeps the search to the kinds you name (`memory`, `rule`, `decision`, `task`, …; several with commas). Each kind also has its own search command, such as `lore decision search`, which prints the row the way `list` does.

<!-- example: search#search-one-kind -->
```console
# alice, in website
$ lore search payments --tables decision
Found 1 hit for "payments" across 1 entity type:
[decision  ] dec_7a67…9504 We use Stripe for payments  (score=0.017)
$ lore decision search Stripe
dec_7a67…9504 [accepted] We use Stripe for payments
```
<!-- /example -->

## Step 3 — Get the result as JSON

`--json` prints the same hits in a stable shape for scripts and tools: the kind, the id, the score and the snippet.

<!-- example: search#search-json -->
```console
# alice, in website
$ lore search WhatsApp --json
{
  "schema_version": 1,
  "kind": "search.global",
  "count": 1,
  "data": {
    "count": 1,
    "external": [],
    "hits": [
      {
        "entity_table": "memories",
        "entity": "memory",
        "id": "mem_162d…9666",
        "pretty": "mem_162d…9666",
        "score": 0.016666666666666666,
        "snippet": "The client replies faster on \u003cb\u003eWhatsApp\u003c/b\u003e than email"
      }
    ],
    "query": "WhatsApp"
  }
}
```
<!-- /example -->

## Step 4 — Include archived rows

Archived rows are left out unless you ask for them with `--include-archived`.

<!-- example: search#archived -->
```console
# alice, in website
$ lore search payments --tables memory
(no matches)
$ lore search payments --tables memory --include-archived
Found 1 hit for "payments" across 1 entity type:
[memory    ] mem_2ad3…f891 Old payments page is gone  (score=0.017)
```
<!-- /example -->

## Step 5 — Search one repo or all repos

A project can have several repos (see [Multi-repo projects](../sync/multi-repo-projects.md)). A row is either project-wide or belongs to one repo. These flags pick which rows you see, and they work on `list` and `search` alike:

| Flag | Shows |
|---|---|
| *(none)* or `--repo <name>` | that repo's rows plus the project-wide ones |
| `--repo <name> --no-inherit` | only that repo's rows |
| `--master-only` | only project-wide rows |
| `--all-repos` | every row of every repo |

<!-- example: search#repo-scope -->
```console
# alice, in website
$ lore repo add web --origin https://github.com/client/website
✓ repo web registered (rep_54fe…28f2)
$ lore memory add --repo web --body 'The web repo deploys on every merge to main'
✓ mem_05e9…4603
  scope: [repo:web]
$ lore memory list --repo web
mem_162d…9666 [retrieved] The client replies faster on WhatsApp than email
mem_05e9…4603 [retrieved] The web repo deploys on every merge to main
$ lore memory list --master-only
mem_162d…9666 [retrieved] The client replies faster on WhatsApp than email
$ lore memory list --repo web --no-inherit
mem_05e9…4603 [retrieved] The web repo deploys on every merge to main
$ lore search deploys --all-repos
Found 1 hit for "deploys" across 1 entity type:
[memory    ] mem_05e9…4603 The web repo deploys on every merge to main  (score=0.017)
```
<!-- /example -->

## Try it: whole words, prefixes and several words

Search matches whole words, upper or lower case alike. It does not match other forms of a word: `invoice` does not find "invoices". Put `*` at the end to match every word that starts with those letters. Several words find rows that hold all of them.

<!-- example: search#word-forms only=2,3,4,5 -->
```console
# alice, in website
$ lore search invoice
Found 2 hits for "invoice" across 2 entity types:
[memory    ] mem_c0f3…b5b8 The client sends invoices on Fridays  (score=0.017)
[task      ] tsk_76bd…3f10 Send the invoice reminder before Friday  (score=0.017)
$ lore search invoices
Found 2 hits for "invoices" across 2 entity types:
[memory    ] mem_c0f3…b5b8 The client sends invoices on Fridays  (score=0.017)
[task      ] tsk_76bd…3f10 Send the invoice reminder before Friday  (score=0.017)
$ lore search 'invoi*'
Found 2 hits for "invoi*" across 2 entity types:
[memory    ] mem_c0f3…b5b8 The client sends invoices on Fridays  (score=0.017)
[task      ] tsk_76bd…3f10 Send the invoice reminder before Friday  (score=0.017)
$ lore search 'client Fridays'
Found 1 hit for "client Fridays" across 1 entity type:
[memory    ] mem_c0f3…b5b8 The client sends invoices on Fridays  (score=0.017)
```
<!-- /example -->

## Keep the index healthy

`lore search status` compares the index with the rows, kind by kind; every line should say `✓ ok`. `lore search rebuild` throws the index away and builds it again from the rows (`--kind memory` for one kind). Run it when status shows a difference, or after a restore from a backup.

<!-- example: search#status lines=6 -->
```console
# alice, in website
$ lore search status
ENTITY                 FTS_TABLE                   FTS   SOURCE STATUS
architecturenote       architecturenote_fts          0        0 ✓ ok
behaviour              behaviour_fts                 0        0 ✓ ok
comment                comment_fts                   0        0 ✓ ok
cookbookrecipe         cookbookrecipe_fts            0        0 ✓ ok
decision               decision_fts                  1        1 ✓ ok
…
```
<!-- /example -->

<!-- example: search#rebuild -->
```console
# alice, in website
$ lore search rebuild
✓ rebuilt 23 entity FTS tables
$ lore search rebuild --kind memory
✓ rebuilt memory_fts
```
<!-- /example -->

## Pitfalls

- **No word forms.** `invoice` and `invoices` are different words to the index. Use `invoic*` when you are not sure.
- **Quote what the shell would change.** `lore search 'invoi*'` with quotes; without them the shell may turn `*` into file names first.
- <!-- ds:block id=search-defaults-87uy20io --> **Limits.** By default each kind gives at most 10 hits (`--per-table`) and the merged list stops at 20 (`--limit`). A common word in a big project can push the row you want off the list: add a second word or narrow with `--tables`.
- **"(no matches)" is not an error.** The command exits 0; check the words, or add `--include-archived` or `--all-repos`.
- **`lore querylog list` stays empty.** The query log is meant to record past searches, but in this build nothing writes to it, so it always prints `(no querylogs)`.

<!-- example: search#querylog -->
```console
# alice, in website
$ lore querylog list
(no querylogs)
```
<!-- /example -->

## Related

- [Render and agent files](render-and-agent-files.md): what is pinned and what is left for search.
- [Rules, memories and decisions](rules-memories-decisions.md)
- [Multi-repo projects](../sync/multi-repo-projects.md): repos in one project.
- [Backup, restore and repair](../operations/backup-restore-repair.md): rebuild the index after a restore.
- [Knowledge guides index](README.md)
