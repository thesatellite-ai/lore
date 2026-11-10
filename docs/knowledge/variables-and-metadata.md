# Variables and metadata

**In short:** a variable is one named value (a host name, a number of minutes) that notes point at with `${var:<name>}`, so you change it in one place and every note shows the new value. Metadata is a set of extra fields on any row (`client=acme`, `cost_hours=3`) that you can filter, export and check. `lore vars` manages the first, `lore meta` the second.

## What they are and when you want them

Use a **variable** when the same fact appears in several notes and will change: the API host, a port, the number of minutes a cache lives. The note keeps the text `${var:api_host}`; `lore show <id> --expand` fills in today's value. Renaming a variable keeps the old name as an alias, so notes written with the old name still resolve. Variables never hold secrets: for a password use the `secret_ref` type, which holds only a vault locator.

Use **metadata** when you want to sort or count rows by something lore has no field for: which client a task is for, how many hours it cost, a billing plan. Any row (task, memory, decision, resource) can carry metadata. The first time you set a key, lore adds it to the repo's key registry with the type it saw; you can also declare a key up front with a fixed list of allowed values, so a typo is refused instead of saved.

| | Variables | Metadata |
|---|---|---|
| What it is | one named value, its own row (`var_…`) | key=value pairs on another row |
| Where it is used | `${var:<name>}` inside any text field | filters (`--where`), `meta get`, `meta export`, `lore sql` |
| Types | text, number, url, dns, ip, cidr, email, date, datetime, duration, secret_ref | text, number, bool, date, datetime, list, json and more |
| Commands | `lore vars set · list · show · rename · rm · where` | `lore meta set · get · unset · keys · add · edit · rename · export · import · lint · history · …` |

## Before you start

- A repo with lore set up: see [Your first project](first-project.md).
- Run every command inside the repo. Variables and metadata keys are written to the repo's store (`.lore/data`) unless you pass `--to` with another place.

## Step 1 — Set a variable

`lore vars set <name> <value>` adds a variable, or changes its value if it already exists in that place. `--type` checks the value (a `dns` value must be a host name, a `number` a number); the default type is `text`. Names are snake_case.

<!-- example: meta#vars-set -->
```console
# alice, in website
$ lore vars set api_host api.example.com --type dns --description 'The shop API the website reads'
Added var_…28f2 api_host = api.example.com (dns) in repo (not shareable: clients see ${var:<name>}).
$ lore vars set cache_minutes 5 --type number
Added var_…4603 cache_minutes = 5 (number) in repo (not shareable: clients see ${var:<name>}).
$ lore vars list
NAME	VALUE	TYPE	WHERE
cache_minutes	5	number	repo
api_host	api.example.com	dns	repo
$ ls .lore/data/vars | wc -l
2
```
<!-- /example -->

Each variable is one row file under `.lore/data/vars`, so it is committed and shared like every other note.

## Step 2 — Use it in a note

Write `${var:<name>}` anywhere in a text field. `lore show <id>` prints the text as written; `--expand` fills in the values and lists every reference it resolved.

<!-- example: meta#vars-use -->
```console
# alice, in website
$ lore memory add --body 'Products come from ${var:api_host} and are cached for ${var:cache_minutes} minutes'
✓ mem_7a67…9504
  scope: [master]
$ lore show mem_7a67…9504
mem_7a67…9504  memories  repo
  body: Products come from ${var:api_host} and are cached for ${var:cache_minutes} minutes
  created_by_actor_id: act_2ad3…f891
  kind: retrieved
  project_id: prj_162d…9666
  source_kind: manual
  trust_score: 0.5
  tx_at: 2026-11-02T10:00:00.000000001Z
created 2026-11-02T10:00:25Z by act_2ad3…f891 · updated 2026-11-02T10:00:25Z by act_2ad3…f891
$ lore show mem_7a67…9504 --expand
mem_7a67…9504  memories  repo
  body: Products come from api.example.com and are cached for 5 minutes
  created_by_actor_id: act_2ad3…f891
  kind: retrieved
  project_id: prj_162d…9666
  source_kind: manual
  trust_score: 0.5
  tx_at: 2026-11-02T10:00:00.000000001Z
created 2026-11-02T10:00:25Z by act_2ad3…f891 · updated 2026-11-02T10:00:25Z by act_2ad3…f891
references:
  ${var:api_host}  api.example.com  (var_54fe…28f2)
  ${var:cache_minutes}  5  (var_05e9…4603)
```
<!-- /example -->

## Step 3 — Change the value once

Set the variable again with `lore vars set <name> <new value>`. Every note that points at it shows the new value the next time it is expanded; no note is rewritten. The type stays what it was. [Try it](#try-it) below shows this step with real output.

## Step 4 — Rename a variable

`lore vars rename <old> <new>` changes the name and keeps the old one as an alias, so the memory written with `${var:api_host}` still resolves. `lore vars where <name>` lists every place that defines the name (the repo, a store folder, your personal store); the nearest definition wins.

<!-- example: meta#vars-rename -->
```console
# alice, in website
$ lore vars rename api_host shop_api_host
✓ renamed variable api_host → shop_api_host (old name kept as an alias) (var_…28f2)
$ lore vars list
NAME	VALUE	TYPE	WHERE
cache_minutes	10	number	repo
shop_api_host	api.example.com	dns	repo
$ lore show mem_7a67…9504 --expand
mem_7a67…9504  memories  repo
  body: Products come from api.example.com and are cached for 10 minutes
  created_by_actor_id: act_2ad3…f891
  kind: retrieved
  project_id: prj_162d…9666
  source_kind: manual
  trust_score: 0.5
  tx_at: 2026-11-02T10:00:00.000000001Z
created 2026-11-02T10:00:25Z by act_2ad3…f891 · updated 2026-11-02T10:00:25Z by act_2ad3…f891
references:
  ${var:api_host}  api.example.com  (var_54fe…28f2)
  ${var:cache_minutes}  10  (var_05e9…4603)
$ lore vars where shop_api_host
NAME	VALUE	TYPE	WHERE
shop_api_host	api.example.com	dns	repo
```
<!-- /example -->

## Step 5 — Remove a variable

`lore vars rm <name> --yes` removes it and keeps a tombstone. A note that still points at it is left as written: `--expand` prints the reference as `dangling`, so you can find and fix it.

<!-- example: meta#vars-rm -->
```console
# alice, in website
$ lore vars rm cache_minutes --yes
✓ removed variable cache_minutes (var_…4603) from repo
$ lore vars list
NAME	VALUE	TYPE	WHERE
shop_api_host	api.example.com	dns	repo
$ lore show mem_7a67…9504 --expand
mem_7a67…9504  memories  repo
  body: Products come from api.example.com and are cached for ${var:cache_minutes} minutes
  created_by_actor_id: act_2ad3…f891
  kind: retrieved
  project_id: prj_162d…9666
  source_kind: manual
  trust_score: 0.5
  tx_at: 2026-11-02T10:00:00.000000001Z
created 2026-11-02T10:00:25Z by act_2ad3…f891 · updated 2026-11-02T10:00:25Z by act_2ad3…f891
references:
  ${var:api_host}  api.example.com  (var_54fe…28f2)
  ${var:cache_minutes}  dangling  no such target; left as written
```
<!-- /example -->

## Step 6 — Put metadata on a row

`lore meta set <id> key=value` sets one field. Add a type with `key:type=value` (here `cost_hours:number=3`); without one a new key is `text`. The first use of a key adds it to the repo's registry and the output says so. `lore meta get <id>` lists a row's fields. Commands that add rows take `--meta key=value` too (`lore task add 'Fix it' --meta client=acme`).

<!-- example: meta#meta-set -->
```console
# alice, in website
$ lore task add 'Fix the Safari checkout button'
✓ tsk_c0f3…b5b8 — Fix the Safari checkout button [medium]
lore live: (live, pushed)
$ lore meta set tsk_c0f3…b5b8 client=acme
tasks tsk_…b5b8: set client = acme
new keys in repo: client (text)
lore live: (live, pushed)
$ lore meta set tsk_c0f3…b5b8 cost_hours:number=3
tasks tsk_…b5b8: set cost_hours = 3
new keys in repo: cost_hours (number)
lore live: (live, pushed)
$ lore meta get tsk_c0f3…b5b8
client	acme
cost_hours	3
```
<!-- /example -->

`lore meta keys` lists every key the repo knows, its type and how many rows use it:

<!-- example: meta#meta-keys -->
```console
# alice, in website
$ lore meta keys
KEY	SCOPE	TYPE	ROWS	VALUES
client	repo	text	1	
cost_hours	repo	number	1	
```
<!-- /example -->

## Step 7 — Declare a key with allowed values

`lore meta add <name> --type <type>` declares a key before anyone uses it. `--values` gives a closed list and `--strict` turns a wrong value into a refusal (without `--strict` it is a warning). `--description` says what the key means, `--sensitive` hides its values from agents, render and lists, and `--internal` keeps it out of client repos.

<!-- example: meta#meta-add -->
```console
# alice, in website
$ lore meta add priority_band --type text --strict --values low,normal,urgent --description 'How fast the client needs it'
added mky_…7ab0 priority_band (text, strict, values low, normal, urgent) to this repo (not internal)
$ lore meta set tsk_c0f3…b5b8 priority_band=urgent
tasks tsk_…b5b8: set priority_band = urgent
lore live: (live, pushed)
$ lore meta set tsk_c0f3…b5b8 priority_band=asap
ERROR: [E_META_VALUE] meta: "asap" is not an allowed value of priority_band; allowed: low, normal, urgent
  hint: lore meta value add priority_band <value>
(exit 20)
```
<!-- /example -->

The refusal names the allowed values and the command that adds a new one (`lore meta value add priority_band <value>`).

## Step 8 — Set a field on many rows at once

`--where key=value` picks every row whose metadata matches (repeat it to AND several). Run it with `--dry-run` first to see the rows; the real run is one batch you can undo with `lore batch undo`. `lore meta export` prints the values as CSV (or JSON lines with `--format json`), ready for a spreadsheet; `lore meta import <file>` reads the same shape back as one batch.

<!-- example: meta#meta-where -->
```console
# alice, in website
$ lore task add 'Update the footer links' --meta client=acme && lore task add 'Add dark mode' --meta client=globex
✓ tsk_76bd…3f10 — Update the footer links [medium]
lore live: (live, pushed)
✓ tsk_d431…313a — Add dark mode [medium]
lore live: (live, pushed)
$ lore meta set --where client=acme billing=monthly --dry-run
would set billing on 2 rows (tasks 2): tsk_c0f3…b5b8, tsk_76bd…3f10
dry run: nothing written
$ lore meta set --where client=acme billing=monthly --yes
set billing on 2 rows; batch bat_b4f4…a5b0 (undo: lore batch undo bat_b4f4…a5b0)
new keys in repo: billing (text)
lore live: (live, pushed)
$ lore meta export --key client,billing
id,key,value
tsk_c0f3…b5b8,billing,monthly
tsk_c0f3…b5b8,client,acme
tsk_76bd…3f10,billing,monthly
tsk_76bd…3f10,client,acme
tsk_d431…313a,client,globex
```
<!-- /example -->

## Step 9 — Rename a value or a key

`lore meta edit <key> --rename-value old=new` renames an allowed value; its id stays, so the rows keep pointing at it. `lore meta rename <key> <new>` renames the key itself; `--rewrite-rows` also rewrites the label stored on every row now, in one batch, without changing any value or date. Without it (and for a value rename, which has no `--rewrite-rows`), each row's label catches up on that row's next write (any `meta set`, `meta unset` or edit of the row).

<!-- example: meta#meta-edit -->
```console
# alice, in website
$ lore meta edit priority_band --rename-value urgent=asap
priority_band: renamed value val_…c1c9 urgent → asap; row labels refresh on each row's next write; no values changed
$ lore meta get tsk_c0f3…b5b8 priority_band
priority_band	urgent
$ lore meta keys --key priority_band
KEY	SCOPE	TYPE	ROWS	VALUES
priority_band	repo	text	1	asap, low, normal
```
<!-- /example -->

<!-- example: meta#meta-rename -->
```console
# alice, in website
$ lore meta rename client customer --rewrite-rows
renamed client → customer in the registry; rewrote 3 row labels (batch bat_28f8…03df; labels only, no values or dates changed)
lore live: (live, pushed)
lore live: landed on main (601d285); leaves live once main is fetched
$ lore meta get tsk_c0f3…b5b8
billing	monthly
cost_hours	3
customer	acme
priority_band	urgent
```
<!-- /example -->

## Step 10 — Remove a field, check and look back

`lore meta unset <id> <key>` removes one field from a row (`--where` removes it from many). `lore meta lint` lists values that do not fit their key and registry problems, and `lore meta history <id>` reads the dated changes of a row's metadata from git history.

<!-- example: meta#meta-unset -->
```console
# alice, in website
$ lore meta unset tsk_c0f3…b5b8 cost_hours
tasks tsk_…b5b8: removed cost_hours
lore live: (live, pushed)
lore live: 4 row(s) left live (main holds them)
$ lore meta get tsk_c0f3…b5b8
billing	monthly
customer	acme
priority_band	asap
```
<!-- /example -->

<!-- example: meta#meta-lint -->
```console
# alice, in website
$ lore meta lint
no metadata problems
```
<!-- /example -->

<!-- example: meta#meta-history -->
```console
# alice, in website
$ lore meta history tsk_c0f3…b5b8
2026-11-02 10:01  alice           client  set acme
2026-11-02 10:01  alice           cost_hours  set 3
2026-11-02 10:02  alice           priority_band  set urgent
2026-11-02 10:02  alice           billing  set monthly
2026-11-02 10:03  alice           cost_hours  unset (was 3)
```
<!-- /example -->

## Try it

After Steps 1 and 2, change `cache_minutes` from 5 to 10 and expand the memory again. The body follows the variable; the stored text never changes.

<!-- example: meta#vars-change -->
```console
# alice, in website
$ lore vars set cache_minutes 10
Changed var_…4603 cache_minutes = 10 (number) in repo (not shareable: clients see ${var:<name>}).
$ lore vars show cache_minutes
var_05e9…4603 cache_minutes = 10 (number) in repo
created 2026-11-02T10:00:10Z by act_2ad3…f891 · updated 2026-11-02T10:00:40Z by act_2ad3…f891
$ lore show mem_7a67…9504 --expand
mem_7a67…9504  memories  repo
  body: Products come from api.example.com and are cached for 10 minutes
  created_by_actor_id: act_2ad3…f891
  kind: retrieved
  project_id: prj_162d…9666
  source_kind: manual
  trust_score: 0.5
  tx_at: 2026-11-02T10:00:00.000000001Z
created 2026-11-02T10:00:25Z by act_2ad3…f891 · updated 2026-11-02T10:00:25Z by act_2ad3…f891
references:
  ${var:api_host}  api.example.com  (var_54fe…28f2)
  ${var:cache_minutes}  10  (var_05e9…4603)
```
<!-- /example -->

## Pitfalls

- **A type is checked on every set.** `--type email` refuses a value that is not an address, with `E_INVALID_INPUT`:

<!-- example: meta#vars-type -->
```console
# alice, in website
$ lore vars set support_email not-an-email --type email
ERROR: [E_INVALID_INPUT] vars set: "not-an-email" is not a email
(exit 20)
```
<!-- /example -->

- **A value label shown by `meta get` can lag behind a rename.** After `meta edit --rename-value` the registry has the new name at once (`lore meta keys` shows it) while `meta get` still prints the label stored on the row until that row's next write (any `meta set`, `meta unset` or edit of it); after that write the row answers to the new name. For key names, `lore meta rename --rewrite-rows` refreshes every row now.
- **Variables are not secrets.** A value that looks like a password is stopped by the secret check, like any other note. Store the secret in your password manager and keep only its locator in a `secret_ref` variable.
- **`--where` matches metadata keys only.** `--where status=open` is refused with `E_META_UNKNOWN` because `status` is a task field, not a metadata key; the error lists the keys you can use.
- **A removed variable leaves its references dangling.** `lore vars rm` never edits the notes that use it; search for `${var:<name>}` and fix them, or set the variable again.

## Related

- [Rules, memories and decisions](rules-memories-decisions.md): the rows you most often put metadata on.
- [Tasks](../tasks/tasks.md): `--meta` on `lore task add`.
- [Search](search.md): finding rows by text.
- [Comments and tags](comments-and-tags.md): tags are the other way to group rows.
- [Error codes](../reference/errors.md#metadata): every `E_META_*` code.
