# Search

**In short:** `lore search --query <words>` looks through every note you have (rules, memories, decisions, tasks and the rest) and shows the best matches first. Your coding tool uses the same command to find what it needs before it answers.

## What it is and when you want it

The file your coding tool reads at start holds only the strongest rules and warnings. Everything else, such as memories, decisions and `should` rules, is found on demand with `lore search`. Think of it as the search box of a library: one box for every shelf.

Behind the box is an *index*: a list of every word and the rows it appears in, like the index at the back of a book. lore keeps it up to date as you add and change rows.

## Before you start

- A repo with lore set up and a few rows: see [Your first project](first-project.md).
- Nothing to run first: lore builds the search index from `.lore/data` by itself, also after an upgrade (`lore search rebuild` forces a rebuild).

## Step 1 — Search everything

Give one or more words, or ask a question in plain words. Each hit shows its kind, its id, the row's title (a resource's name and kind) and the text around the found word, and a score from 0 to 1 (higher is better). Hits of every kind are in one list, best first.

<!-- example: search#search -->
```console
# alice, in website
$ lore search --query payments
Found 2 hits for "payments" across 2 entity types:
decision  dec_7a67…9504  We use Stripe for payments  (score=0.850)
memory    mem_2ad3…f891  Old payments page is gone  (score=0.850)
```
<!-- /example -->

## Step 2 — Search one kind

`--tables` keeps the search to the kinds you name (`memory`, `rule`, `decision`, `task`, …; several with commas). The org's people (`member`: name, handle, email) and teams (`team`: handle, name) are found too, so `lore search --query mira` lists the person beside the rows that name them; they are left out when you give `--store`, `--repo`, `--born-branch` or a `key:value` condition. Each kind also has its own search command, such as `lore decision search`, which prints the row the way `list` does. It reads your words the way `lore search` does (a question works, any form of a word matches) and lists the kind's rows in the same order, with the same `score` (higher is better).

<!-- example: search#search-one-kind -->
```console
# alice, in website
$ lore search --query payments --tables decision
Found 1 hit for "payments" across 1 entity type:
decision  dec_7a67…9504  We use Stripe for payments  (score=0.850)
$ lore decision search --query Stripe
dec_7a67…9504 [accepted] We use Stripe for payments
```
<!-- /example -->

## Step 3 — Get the result as JSON

`--json` prints the same hits in a stable shape for scripts and tools: the kind, the id, the score, the snippet with the found words between `<b>` and `</b>`, the row's `title` (a title or name; empty for a rule or memory), a resource's `resource_kind`, and `partial: true` on a hit that holds only some of the words.

The snippet is the text around the found words, about 100 characters. `--full` prints the whole text (a note's body, or the longest field the words were found in) of the best 3 hits under their line, indented, in place of the snippet, so `lore search --query "ledger totals" --full` answers in one command; the other hits keep their snippet line, and a note at the end says how many and the flag that prints them whole. `--full-top <n>` sets how many of the best hits print whole and turns `--full` on: `lore search --query "ledger totals" --full-top 10`. With `--json` the same top hits carry it as `text`. People, teams and resources never carry `text`: `lore resource show --id <name>` prints a resource whole.

<!-- example: search#search-json -->
```console
# alice, in website
$ lore search --query WhatsApp --json
{
  "schema_version": 1,
  "kind": "search.global",
  "count": 1,
  "data": {
    "query": "WhatsApp",
    "count": 1,
    "hits": [
      {
        "entity_table": "memories",
        "entity": "memory",
        "id": "mem_162d…9666",
        "pretty": "mem_162d…9666",
        "score": 0.85,
        "snippet": "The client replies faster on \u003cb\u003eWhatsApp\u003c/b\u003e than email"
      }
    ],
    "external": []
  }
}
```
<!-- /example -->

## Step 4 — Include archived rows

Archived rows are left out unless you ask for them with `--include-archived`.

<!-- example: search#archived -->
```console
# alice, in website
$ lore search --query payments --tables memory
(no matches)
  searched: repo (memory)
  read as: words: payments
  try: lore search --query "payments"
  try: lore ask --query "payments"
  try: lore search --query "payments" --include-archived
$ lore search --query payments --tables memory --include-archived
Found 1 hit for "payments" across 1 entity type:
memory  mem_2ad3…f891  Old payments page is gone  (score=0.850)
```
<!-- /example -->

## Step 5 — Search one repo or all repos

A project can have several repos (see [Multi-repo projects](../sync/multi-repo-projects.md)). A row is either project-wide or belongs to one repo. These flags pick which rows you see, and they work on `list` and `search` alike:

| Flag | Shows |
|---|---|
| *(none)* | only project-wide rows, even inside one of the project's repos (a row added with `--repo`, `--scope repo` or `--scope dir:<path>` belongs to its repo and needs `--repo <name>` or `--all-repos`) |
| `--repo <name>` | that repo's rows plus the project-wide ones |
| `--repo <name> --no-inherit` | only that repo's rows |
| `--master-only` | only project-wide rows |
| `--all-repos` | every row of every repo |

<!-- example: search#repo-scope -->
```console
# alice, in website
$ lore repo add --mount-name web --origin https://github.com/client/website
lore: wrote ~/work/website/.lore/data/repos/rep_54fe…28f2.json
✓ repo web registered (rep_54fe…28f2)
$ lore memory add --repo web --body 'The web repo deploys on every merge to main'
lore: wrote ~/work/website/.lore/data/memories/mem_05e9…4603.json
✓ mem_05e9…4603
  scope: [repo:web]
$ lore memory list --repo web
mem_162d…9666 [retrieved] The client replies faster on WhatsApp than email
mem_05e9…4603 [retrieved] The web repo deploys on every merge to main
1 hidden (1 archived); --all shows them
$ lore memory list --master-only
mem_162d…9666 [retrieved] The client replies faster on WhatsApp than email
2 hidden (1 scoped to a repo, 1 archived); --all shows them
$ lore memory list --repo web --no-inherit
mem_05e9…4603 [retrieved] The web repo deploys on every merge to main
2 hidden (2 scoped to a repo); --all shows them
$ lore search --query deploys --all-repos
Found 1 hit for "deploys" across 1 entity type:
memory  mem_05e9…4603  The web repo deploys on every merge to main  (score=0.850)
```
<!-- /example -->

## Try it: whole words, prefixes and several words

Search matches whole words, upper or lower case alike, with accents ignored (`cafe` finds "café"); the Turkish dotted and dotless i count as one letter, so `ISPANAK` finds "ıspanak" and `diyarbakır` finds "DİYARBAKIR", and the hit shows the text as it was written. Other forms of a word match too: `invoice` finds "invoices", `deploy` finds "deploying" and "deployment". Put `*` at the end to match every word that starts with those letters; a title word that starts with them counts as a word in the title. Several words list the rows that hold all of them first, then the rows that hold most of them.

<!-- example: search#word-forms only=2,3,4,5 -->
```console
# alice, in website
$ lore search --query invoice
Found 2 hits for "invoice" across 2 entity types:
memory  mem_c0f3…b5b8  The client sends invoices on Fridays  (score=0.850)
task    tsk_76bd…3f10  Send the invoice reminder before Friday  (score=0.850)
$ lore search --query invoices
Found 2 hits for "invoices" across 2 entity types:
memory  mem_c0f3…b5b8  The client sends invoices on Fridays  (score=0.850)
task    tsk_76bd…3f10  Send the invoice reminder before Friday  (score=0.850)
$ lore search --query 'invoi*'
Found 2 hits for "invoi*" across 2 entity types:
memory  mem_c0f3…b5b8  The client sends invoices on Fridays  (score=0.850)
task    tsk_76bd…3f10  Send the invoice reminder before Friday  (score=0.850)
$ lore search --query 'client Fridays'
Found 3 hits for "client Fridays" across 2 entity types:
memory  mem_c0f3…b5b8  The client sends invoices on Fridays  (score=0.900)
task    tsk_76bd…3f10  Send the invoice reminder before Friday  (score=0.492)
memory  mem_162d…9666  The client replies faster on WhatsApp than email  (score=0.450)
```
<!-- /example -->

## Ask a question

Type the question as you would ask a teammate: `lore search --query "where is the stripe test mode on"`. Words that carry no topic (how, do, we, is, the, where, …) are skipped, and the rest are matched in any form. A row needs only some of the words: rows holding every word come first, a row whose title or first line opens with the words (an overview row "Alice Ng (person) — CFO …", the resource `alice_ng`) comes before rows that only mention them, a word in the title or first line counts more than one in the body, among rows whose title or first line holds every word the newest comes first (so a fact that changed shows its newest value first), and when no row holds every word the list says so and shows the closest rows. A query written with search syntax (`"a phrase"`, `word*`, `AND`, `OR`, `NOT`) still needs every hit to match all of it. Punctuation around words that does not read as search syntax (`alice (person)`, `orbit)`, an unclosed `"`, `rate^`, `{orbit}`, `orbit AND` with no word after it) is read as the plain words: the search finds the rows holding them, as for any plain question. Only a query with no word left to search for (a lone `"`, `(` or `*`, a bare `AND` / `OR` / `NOT`, `a OR`, or `'`) is refused with E_INVALID_INPUT "not a valid search query (<reason>)" and a hint. Both are the same in `lore search`, every `lore <kind> search` and `lore comment search`, and over REST (400). A word holding a colon (`error: timeout`, `time:3pm`) is plain text and finds the rows holding it. A `key:value` on a metadata key in use (`day:2026-11-18`, `env:prod`) is a condition instead: it finds the rows of every store the search reads, a store folder's rows labelled with their place as word hits are (`--store me` reads your personal store's folders).

<!-- example: search#a-question -->
```console
# alice, in website
$ lore search --query 'where is the stripe test mode on'
Found 3 hits for "where is the stripe test mode on" across 3 entity types:
hotfix    hfx_d431…313a  Stripe test mode is on in staging  (score=0.988)
decision  dec_7a67…9504  We use Stripe for payments  (score=0.350)
rule      rul_b4f4…a5b0  Run the tests before you push  (score=0.350)
$ lore search --query 'stripe invoices'
Found 4 hits for "stripe invoices" across 4 entity types:
No row holds every word; these hold some of them, most first.
decision  dec_7a67…9504  We use Stripe for payments  (score=0.492)
hotfix    hfx_d431…313a  Stripe test mode is on in staging  (score=0.492)
memory    mem_c0f3…b5b8  The client sends invoices on Fridays  (score=0.492)
task      tsk_76bd…3f10  Send the invoice reminder before Friday  (score=0.492)
```
<!-- /example -->

## Ask lore for the answer, in one call

`lore search` lists rows; `lore ask --query "<question>"` answers. Ask it as you would ask a person: `lore ask --query "who is alice-ng"`, `lore ask --query "what did we decide for orbit and why"`, `lore ask --store me --query "what do I owe alice-ng"`, `lore ask --store me --query "what happened last week"`. One call reads every store you read (`--store me`: every folder of your personal store) and prints what answers the question first, then the resource it names as context: for a status, a decision or a budget the best rows, then the project; for a debt question the open tasks, then the person; for a date the rows of those days; for "who is X" and "everything about X" (also a question that is only a name, `lore ask --query "orbit"`) the resource first. Everything about X prints under plain headings: `who:` (the resource's kind, name and folder, then one line per field and per linked resource), `latest (the newest row of each folder and the newest decision, status, budget and meeting row, word for word):` (the newest row of each folder that names it, then the newest row holding a decision, a status, a budget or a meeting that no row above holds, each on one line: its day, its folder and its text in quotes, the day the text repeats, as in "Journal 2026-11-06:", left out), `open tasks (n):`, then `more rows (word for word):` in the same shape; no id is printed before the `cited:` line. It holds the resources the question names (by name, by a name with a typo such as alise-ng, by a field value such as a nickname or a role: "who is the CFO"), each with its fields, a changed field with the day it changed and what it was, and the resources whose fields name it (for "who is X" one line per resource led by its name, its kind and aliases in brackets, then its fields: `alice-ng (person; aka Ali): company: acme-co; role: engineer`, no contact value such as an email unless the question names that field, no linked resources and no id; `lore resource show --kind <kind> --name <name>` prints all of it); the open tasks that answer a debt question (`what do I owe X`, `what does X owe me`), one line each with the title, the status when it is not todo and who owes whom when the title does not name them, the person printed under them only when a line names them by an alias; or the open tasks of the resources it names; for a question naming dates (`on 2026-11-02`, `the week of 2026-11-02`, `yesterday`, `the day before yesterday`, `3 days ago`, `last week`, `two Fridays ago`, `last month`) the rows of those days, journal rows with a `day` first, oldest first; else the three best rows in full (`--top`), about the resources it names and holding its topics, newest first, the newest of a changed fact marked "newest: it replaces …". When a row of the answer replaces an older one, the answer starts with one line that says the current value: `now: <newest text> (changed <day>; before that: <older text>)`. A fact that changed is never given as the answer: a row superseded by another (`--supersedes`), a memory past its valid until (`lore memory invalidate`), and an older row of the same folder holding the same topics and words of the question as a newer one come after every current row, marked `[history: superseded by <id>]`, `[history: valid until <day>]` or `[history: replaced by <id> from <day>]` (in `--json` the row's `history`), and appear only when the current rows leave room in `--top`. Questions may use other words than the rows: "the reasoning behind the pick" finds "we chose … because …", "how far along" finds status updates, "how much money" finds budget notes. Rows imported from a feed rank after rows you wrote. A reference in a row's text (`${res:person/alice-ng}`) prints as the resource's name (alice-ng); `--json` keeps the text as stored. The answer stays under `--max-chars` (default 4000), leaving out what ranks lowest and saying how many items it left out, and ends with `cited:` and every id in it in reading order (but the resources of a who-is answer, which its lines name); the question is not printed back, only the dates a date question was read as (`dates:`); `--json` gives the same as data, its `ranked` list naming each item (`kind` resource, task or row, and `id`) in that reading order.

A question that names a file (`lore ask --query "add a due_date column in db/migrations/0042_invoice_due.sql"`, or a folder ending in `/`) answers first with what applies to that file and to the question's own words, exactly as `lore context --for-file <path> --for-prompt "<the question>"` picks it: the rules whose globs or folder scope cover it, the hotfixes for it, the semantic rules its words name, then its scoped notes, each with why it applies. The rest of the answer then holds only rows sharing two or more words with the question, never a row the file's part already printed. Hotfixes are among the rows ask reads, so "invoice time zone trap" finds the hotfix about it.

## Leave words out

Put `-` in front of a word to drop the rows that hold it: `lore search --query "deploy -friday"` finds the rows about deploy that never mention friday. It works with a prefix (`-fri*`), a phrase (`-"on call"`), several words (`"deploy -friday -monday"`), a metadata condition (`"env:prod -friday"`) and `--include-deleted`. A `-` inside a word is part of the word: `well-known` and `x-ray` search for those words, and `-x-ray` leaves out the rows that hold `x-ray`.

The query needs at least one word to search for: `lore search --query=-friday` is refused with a hint. When the query's first word starts with `-`, give it with `=`, so the shell line does not read it as a flag: `lore search --query="-friday deploy"`.

## Keep the index healthy

`lore search status` compares the index with the rows, kind by kind; every line should say `✓ ok`. `lore search rebuild` throws the index away and builds it again from the rows (`--kind memory` for one kind). Run it when status shows a difference, or after a restore from a backup.

`lore search rebuild --store <store>[/<folder>]` rebuilds one store's index, and works from any folder: `lore search rebuild --store me/notes` from your home folder rebuilds that folder of your personal store. You rarely need it after a hand change to a store's clone: when the clone's `HEAD` no longer follows from the commit the index was built at (a `git reset --hard` to an older commit, a clone made again), lore notices on the next command and rebuilds that store's index by itself, so rows the reset dropped stop showing in search, sql and lists. In the same way, when a new lore fills in `${…}` references in more cases than the one that built a cache, that cache fills in its rows' references again on its first open, so `graph --up` lists the notes that name a resource without a rebuild.

<!-- example: search#status lines=6 -->
```console
# alice, in website
$ lore search status
ENTITY                 FTS_TABLE                   FTS   SOURCE STATUS
architecturenote       _fts_architecture_notes        0        0 ✓ ok
behaviour              _fts_behaviours               0        0 ✓ ok
comment                _fts_comments                 0        0 ✓ ok
cookbookrecipe         _fts_cookbook_recipes         0        0 ✓ ok
decision               _fts_decisions                1        1 ✓ ok
…
```
<!-- /example -->

<!-- example: search#rebuild -->
```console
# alice, in website
$ lore search rebuild
✓ rebuilt the search index of 2 stores (repo, machine)
$ lore search rebuild --kind memory
note: the core rebuilds a store's whole cache; --kind memory included
✓ rebuilt the search index of 2 stores (repo, machine)
```
<!-- /example -->

## Pitfalls

- **Word forms are English.** `deploy`, `deploys`, `deploying` and `deployment` find each other; for other languages, or a word lore does not know the forms of, end it with `*` (`invoic*`).
- **Quote what the shell would change.** `lore search --query 'invoi*'` with quotes; without them the shell may turn `*` into file names first.
- <!-- ds:block id=search-defaults-z8rpj5sv --> **Limits.** By default each kind gives at most 10 hits (`--per-table`) and the merged list stops at 20 (`--limit`); `--limit` alone raises both, so `lore search --query budget --limit 100` lists up to 100 hits of one kind. `--full` prints the whole text of the best 3 hits only (`--full-top` for more); the other hits keep their snippet. A cut list says so: it starts `Showing the best 10 hits` instead of `Found`, ends with `note: more rows match than the 10 listed (the best first); add --limit 100 to list more`, and `--json` carries `"more": true`; every `<kind> search` (`lore memory search`, `lore task search`, `lore rule search`, `lore comment search` and the rest) ends with the same note and `--json` `"more": true` when its `--limit` cut it, counting only rows in its scope. A common word in a big project can push the row you want off the list: add a second word, narrow with `--tables`, or raise `--limit`.
- **Chinese, Japanese, Korean and emoji.** Every Chinese, Japanese or Korean character is a word of its own in the index, so a word inside a sentence with no spaces is found (`lore search --query 迁移` finds 数据库迁移规则), and so is a Korean word with a particle attached. Each emoji is a word too: `lore search --query 🚀` finds rows holding it, 👍 also finds 👍🏽, and 👩‍💻 finds that joined emoji. A cache built by an older lore is rebuilt on the first search after the upgrade.
- **"(no matches)" is not an error, and it says what to try.** The command exits 0. Under the empty line, `lore search`, every `lore <kind> search` and `lore ask` (whose empty answer is `(no answer)`) say what they read and what to run next: `searched:` the stores and folders read and the kinds, `read as:` the words the query was read as (for `lore ask` also the question type and the dates it read), and one to three `try:` lines, each a command that runs where it is printed (the same `--store`; the whole store a folder belongs to; `--store me` when your personal store's folders were not read; every kind for a `<kind> search`; the query without `--tables`, `--since` or `--until`; `--all` for `lore task search`; `lore ask` for a search and `lore search` for an ask; `--include-archived`). In `--json` the same is in the top-level fields `searched` (`stores`, `kinds`, `words`, `type`, `dates`) and `try` (a list of commands); both are absent when something was found. `lore context --for-file <path>`, `--for-prompt` and `--topic` with nothing applying print `(no rows apply here)` and the same lines: `searched:` the stores and kinds lore context reads (it never reads your personal store), `read as:` the path (matched by globs, the folder scopes covering it and rows naming it or its folder) and its words, the prompt's words or the topic, and `try:` `lore search` on those words (with `--store me` first when a plain search here leaves your personal store out), `lore ask`, `lore rule list` or `lore context --topics`, each with the same `--store`; its `--json` `searched` also holds `path`, `folders` and `topic`.
- **Your personal store's folders are read only when you name them.** A search or list that names no store reads the store of the folder it runs in, never the folders of your personal store (notes written with `--to me/<folder>`). When such a read finds nothing and your personal store has folders, it says so under its answer (`note: your personal store's folders (me/alpha) were not read; add --store me to the same command to read them`; in `--json` the field `personal_unread`), and outside a repo the refusal's hint starts with `add --store me to read your personal store`. `--store me` reads every folder of your personal store from any folder.

## Related

- [Render and agent files](render-and-agent-files.md): what is pinned and what is left for search.
- [Rules, memories and decisions](rules-memories-decisions.md)
- [Multi-repo projects](../sync/multi-repo-projects.md): repos in one project.
- [A damaged cache](../operations/backup-restore-repair.md): rebuild the cache and the search index.
- [Knowledge guides index](README.md)
