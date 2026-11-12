# Resources: servers, domains and the services on them

**In short:** `lore resource` keeps the facts about your infrastructure as typed rows: which servers you run, which services run on which server, where each password lives in your vault. You define a kind (such as `host`) with its fields once, then add one row per thing. lore checks every value against its field, follows the refs between rows, tells you what breaks if one goes down, and keeps the rows in `.lore/data` so they travel with git like your other notes.

## What it is and when you want it

A **kind** is a type of thing, such as `host`, `domain` or `service`. It has **fields**, each with a value type (`text`, `number`, `bool`, `date`, `datetime`, `duration`, `ip`, `cidr`, `dns`, `url`, `email`, `json`, `list`, `ref` or `secret_ref`) and optional rules: required, a default, a closed set of allowed values, a pattern, a min and max, sensitive. A **resource** is one row of a kind: the host `hub_prod`, the service `hub_api`. A `ref` field points at another resource (a service `runs_on` a host), and those refs are what `graph`, `impact`, `repoint` and `lint` follow. A `secret_ref` field holds the name of an entry in a secret manager (such as `gopass://acme/hub/db`), never the secret itself.

Use resources when your notes keep saying the same infrastructure facts in prose ("the API runs on the Hetzner box at 203.0.113.10") and an agent or a teammate needs to look them up, filter them, or ask "what depends on this server?". Rules, memories and decisions stay the right place for how and why; resources hold the what and where.

Resources are branch-only: they are written to `.lore/data` and shared through git commits, never on the live ref, so there is no `--live` flag. A resource is named by its bare name (`hub_prod`), its qualified name `<place>:<kind>/<name>` as `resource add` prints it (`repo:host/hub_prod`; `<place>` is `repo`, `org`, a project folder or a store's name), the short form `<kind>/<name>` (`host/hub_prod`), or its id (`res_…`).

Every subcommand:

<!-- help: resource -->
A resource is a row of a kind (server, domain, …) with typed fields. <name> accepts a bare name, a qualified name <place>:<kind>/<name> (<place> is repo, org, a project folder or a store's name, as resource add prints it), the short form <kind>/<name>, or an id (res_…). Resources are branch-only: there is no --live.

```text
Usage:
  lore resource
  lore resource <command>

Available Commands:
  add         Add a resource of a kind
  adopt       Move another place's resource kind and field rows into this place (ids kept)
  archive     Archive a resource (refs stay; new refs are refused)
  check       Stamp last_checked: a person or agent looked and the facts are right
  clone       Add a resource with another resource's values (--set overrides)
  delete      Delete a resource, or every resource of a kind matching --where (to the trash)
  edit        Edit a resource's values (stamps last_checked)
  export      Export resources of a kind in the import format (sensitive values masked)
  field       Fields of a resource kind (add, edit, rename, retype, remove)
  graph       Print the ref tree of a resource (--down: what it uses; --up: what uses it)
  history     Field changes of one resource, newest first, with actor, date and batch (sensitive values masked)
  impact      What breaks if a resource goes down: every resource that depends on it, with open linked tasks and incidents
  import      Import resources from a csv, json or yaml file (one batch, all or nothing)
  kind        Resource kinds (add, edit, rename, show)
  kinds       List resource kinds with their fields and row counts
  lint        Find dangling and bare-name refs, missing required fields, deprecated fields, due reminders and duplicates
  list        List resources of a kind
  merge       Fold a duplicate resource into another of the same kind: refs move, values fill, the duplicate is archived
  rehome      List the kinds and refs a place can no longer see after its chain changed
  rename      Rename a resource (ids and refs stay)
  repoint     Move every ref from one resource to another of the same kind, then archive the old one
  report      Report on the resources in view: counts per kind, stale, unchecked, dangling, quarantined, open links, secret references to rotate
  show        Show one resource
  stale       Resources whose last_checked is older than --days, or never set
```
<!-- /help -->

## Before you start

- lore is set up in the repo (`lore init`, see [your first project](../knowledge/first-project.md)).
- Keep real passwords and keys in your password manager. A resource stores where a secret lives, not the secret.
- The examples below follow Alice, an agency developer, recording the servers and services of a client project in the repo `client/infra`.

## Step 1 — Define the kinds and their fields

Add a kind, then one field at a time. `--values` makes a closed set, `--required` refuses a resource without the field, `--default` fills it when an add leaves it out, and `--type ref --ref-kind host` makes a field that points at a host. A `secret_ref` field is internal by default (it never reaches a client repo, see Pitfalls):

<!-- example: resources#kinds -->
```console
# alice, in infra
$ lore resource kind add host --description 'a server we run'
lore: wrote ~/work/infra/.lore/data/kinds/knd_7a67…9504.json
added kind knd_…9504 host in this repo
$ lore resource field add host provider --type text --required --values hetzner,aws
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_2ad3…f891.json
added field fld_…f891 host.provider (text)
$ lore resource field add host ip --type ip
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_162d…9666.json
added field fld_…9666 host.ip (ip)
$ lore resource field add host os --type text --default ubuntu-24.04
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_54fe…28f2.json
added field fld_…28f2 host.os (text)
$ lore resource kind add service --description 'an app we deploy'
lore: wrote ~/work/infra/.lore/data/kinds/knd_05e9…4603.json
added kind knd_…4603 service in this repo
$ lore resource field add service runs_on --type ref --ref-kind host --required
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_c0f3…b5b8.json
added field fld_…b5b8 service.runs_on (ref)
$ lore resource field add service url --type url
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_76bd…3f10.json
added field fld_…3f10 service.url (url)
$ lore resource field add service db_password --type secret_ref
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_d431…313a.json
added field fld_…313a service.db_password (secret_ref), internal by default: an entry NAME in a secret manager, never the key
```
<!-- /example -->

`kind show` prints one kind with its fields and rules; `kinds` lists every kind you can see, where it lives and how many rows it has:

<!-- example: resources#kind-show -->
```console
# alice, in infra
$ lore resource kind show host
knd_7a67…9504  host  (repo)
  a server we run
  ip  ip
  os  text  default ubuntu-24.04
  provider  text  required  values hetzner, aws
$ lore resource kinds
ID	KIND	STORE	ROWS	FIELDS
knd_7a67…9504	host	repo	0	ip, os, provider
knd_05e9…4603	service	repo	0	db_password, runs_on, url
```
<!-- /example -->

Other `field add` options: `--pattern` (an RE2 expression a text value must match), `--min` / `--max` (numbers and dates), `--sensitive` (printed as `•••`, Step 13), `--internal`, `--label`, `--description`, `--deprecated` with `--replaced-by`, `--remind-before 30d` (dates; `lint` lists rows within that time), and `--strict` (when two branches change the value, a person decides instead of the newer change winning). `kind add --name-format dns|ip|text` lets resource names be domain names, IP addresses or free text instead of the default snake_case id.

## Step 2 — Add resources

`--set field=value` sets one field; repeat it for each. lore prints the row with its place (`repo:host/hub_prod`) and which fields came from a default:

<!-- example: resources#add -->
```console
# alice, in infra
$ lore resource add host hub_prod --set provider=hetzner --set ip=203.0.113.10
lore: wrote ~/work/infra/.lore/data/resources/res_b4f4…a5b0.json
created res_…a5b0  host  repo:host/hub_prod
  ip = 203.0.113.10; os = ubuntu-24.04; provider = hetzner
  default: os
$ lore resource add host hub_stage --set provider=aws --set ip=203.0.113.11
lore: wrote ~/work/infra/.lore/data/resources/res_28f8…03df.json
created res_…03df  host  repo:host/hub_stage
  ip = 203.0.113.11; os = ubuntu-24.04; provider = aws
  default: os
```
<!-- /example -->

A value outside the closed set, a name that does not fit the kind's name format and a missing required field are each refused before anything is written:

<!-- example: resources#add-refused -->
```console
# alice, in infra
$ lore resource add host hub_test --set provider=linode
ERROR: [E_RESOURCE_INVALID] host hub_test: provider "linode" is not one of hetzner, aws
  hint: lore resource kind show host
(exit 20)
$ lore resource add host Hub-Test --set provider=aws
ERROR: [E_RESOURCE_NAME] host Hub-Test: the name does not fit the kind's name format
  hint: lore resource kind show host
(exit 20)
$ lore resource add service hub_cron
ERROR: [E_RESOURCE_INVALID] service hub_cron: required field runs_on is missing
  hint: lore resource kind show service
(exit 20)
```
<!-- /example -->

To keep a value out of your shell history, use `--set-stdin <field>` (the value comes from stdin) or `--prompt <field>` (typed at the terminal, hidden for a sensitive field; `--prompt @required` asks for every missing required field). `--set-json` sets a `list` or `json` field, and `--meta key=value` adds metadata as on other rows (see [variables and metadata](../knowledge/variables-and-metadata.md)).

## Step 3 — Point resources at each other

A `ref` field takes the name of another resource. Alice records two services that run on `hub_prod`; `db_password` is the vault entry, not the password:

<!-- example: resources#refs -->
```console
# alice, in infra
$ lore resource add service hub_api --set runs_on=hub_prod --set url=https://api.example.com --set db_password=gopass://acme/hub/db
lore: wrote ~/work/infra/.lore/data/resources/res_4c59…6c38.json
created res_…6c38  service  repo:service/hub_api
  db_password = gopass://acme/hub/db; runs_on = hub_prod; url = https://api.example.com
$ lore resource add service hub_web --set runs_on=hub_prod --set url=https://www.example.com
lore: wrote ~/work/infra/.lore/data/resources/res_3b62…e913.json
created res_…e913  service  repo:service/hub_web
  runs_on = hub_prod; url = https://www.example.com
```
<!-- /example -->

## Step 4 — List, filter and show

`list <kind>` prints one column per field. `--where` filters (`=`, `!=`, `~=`, `>`, `>=`, `<`, `<=`, `?` for set, `!?` for not set, `field:a,b` for any of), `--sort` orders by name, `last_checked`, `archived_at` or a field, and `show` prints one resource:

<!-- example: resources#list-and-show -->
```console
# alice, in infra
$ lore resource list host
KIND	NAME	IP	OS	PROVIDER	CHECKED
host	hub_prod	203.0.113.10	ubuntu-24.04	hetzner	
host	hub_stage	203.0.113.11	ubuntu-24.04	aws	
$ lore resource list service --where runs_on=hub_prod --sort name:desc
KIND	NAME	DB_PASSWORD	RUNS_ON	URL	CHECKED
service	hub_web		hub_prod	https://www.example.com	
service	hub_api	gopass://acme/hub/db	hub_prod	https://api.example.com	
$ lore resource show hub_api
res_4c59…6c38  service  repo:service/hub_api
  db_password = gopass://acme/hub/db
  runs_on = hub_prod
  url = https://api.example.com
created 2026-11-02T10:01:20Z by act_f147…5139 · updated 2026-11-02T10:01:20Z by act_f147…5139
```
<!-- /example -->

`list` hides archived rows unless you pass `--archived`; `--include-deleted` and `--deleted-only` show rows in the trash. Every command takes `--json`.

## Step 5 — See what depends on what

`graph --down` follows refs forward (what this resource uses); `graph --up` follows them backward (what uses it). `--depth` sets the hops (default 3, at most 10) and `--fields` limits the ref fields followed:

<!-- example: resources#graph -->
```console
# alice, in infra
$ lore resource graph hub_api --down
service hub_api
└── runs_on  host hub_prod
$ lore resource graph hub_prod --up
host hub_prod
├── runs_on ← service hub_api
└── runs_on ← service hub_web
```
<!-- /example -->

`impact` answers "what breaks if this goes down": every resource that depends on it, by hops, plus the open tasks and incidents linked to any of them. A task links to a resource with `--ref` and the resource's `res_` id; `--ref` on a task, decision or incident also takes the names every resource lookup takes (bare, `<kind>/<name>`, `<place>:<kind>/<name>`), and a bare name held by two resources is refused with `E_RESOURCE_AMBIGUOUS` naming the qualified names. Here Alice looks up the id of `hub_api` and adds a task for it, then asks about the host:

<!-- example: resources#impact only=2,3 -->
```console
# alice, in infra
$ lore task add 'Rotate the hub_api database password' --ref res_4c59…6c38
lore: wrote ~/work/infra/.lore/data/task_lists/tlt_2537…6bbb.json
lore: wrote ~/work/infra/.lore/data/tasks/tsk_b69e…5ed7.json
lore: wrote ~/work/infra/.lore/data/knowledge_refs/kfr_0aae…a61f.json
✓ tsk_b69e…5ed7 — Rotate the hub_api database password [medium]
lore live: (live, pushed) commit 601d285 to origin refs/lore/live
$ lore resource impact hub_prod
what breaks if host hub_prod goes down
  1  service    hub_api              runs_on → hub_prod
  1  service    hub_web              runs_on → hub_prod
  open  task tsk_b69e…5ed7 (mentions hub_api)
```
<!-- /example -->

`--all-links` also lists done tasks and resolved incidents.

## Step 6 — Check that the facts are still right

Infrastructure facts go stale. `check` stamps `last_checked` (a person or agent looked and the facts are right, with an optional `--note`), and `stale` lists resources never checked or checked more than `--days` ago (default 60):

<!-- example: resources#check-and-stale -->
```console
# alice, in infra
$ lore resource stale
KIND	NAME	LAST_CHECKED	AGE
host	hub_prod		never
host	hub_stage		never
service	hub_api		never
service	hub_web		never
$ lore resource check hub_prod --note 'looked in the provider console'
lore: wrote ~/work/infra/.lore/data/resources/res_b4f4…a5b0.json
✓ checked hub_prod now (res_…a5b0)
$ lore resource stale
KIND	NAME	LAST_CHECKED	AGE
host	hub_stage		never
service	hub_api		never
service	hub_web		never
```
<!-- /example -->

`edit` also stamps `last_checked`, so changing a value counts as a check.

## Step 7 — Edit a resource and read its history

`--set` changes a value and `--unset` removes one. `history` lists every field change, newest first, with the actor and date (sensitive values masked); `--field` narrows it to one field, and `--git` also reads older versions from git history:

<!-- example: resources#edit-and-history -->
```console
# alice, in infra
$ lore resource edit hub_prod --set os=debian-12
lore: wrote ~/work/infra/.lore/data/resources/res_b4f4…a5b0.json
✓ edited host hub_prod (res_…a5b0)
$ lore resource edit hub_stage --unset ip
lore: wrote ~/work/infra/.lore/data/resources/res_28f8…03df.json
✓ edited host hub_stage (res_…03df)
$ lore resource history hub_prod
2026-11-02  os  ubuntu-24.04 → debian-12  act_f147…5139
2026-11-02  ip  set 203.0.113.10  act_f147…5139
2026-11-02  provider  set hetzner  act_f147…5139
2026-11-02  os  set ubuntu-24.04  act_f147…5139
$ lore resource history hub_prod --field os
2026-11-02  os  ubuntu-24.04 → debian-12  act_f147…5139
2026-11-02  os  set ubuntu-24.04  act_f147…5139
```
<!-- /example -->

## Step 8 — Copy and rename

`clone` adds a new resource with another one's values (`--set` overrides some); `rename` changes the name only, so ids and refs stay as they are. `--rewrite-rows` also rewrites the name label stored in the rows that point at it:

<!-- example: resources#clone-and-rename -->
```console
# alice, in infra
$ lore resource clone hub_prod hub_prod_2 --set ip=203.0.113.12
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
created res_…ed8c  host  repo:host/hub_prod_2
  ip = 203.0.113.12; os = debian-12; provider = hetzner
$ lore resource rename hub_prod_2 hub_prod_new
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
✓ renamed host hub_prod_2 → hub_prod_new; ids and refs unchanged (res_…ed8c)
```
<!-- /example -->

## Step 9 — Change many resources at once

`edit`, `check` and `delete` have a bulk form: leave out the name and pass `--kind` with one or more `--where` filters. `--dry-run` lists what would change; without a terminal the write needs `--yes`. The whole change is one batch, so `lore batch undo <bat_ id>` reverses it:

<!-- example: resources#bulk-edit -->
```console
# alice, in infra
$ lore resource edit --kind host --where provider=hetzner --set os=debian-13 --dry-run
would edit 2 host resources (dry run, nothing written):
  repo:host/hub_prod  os debian-12 → debian-13
  repo:host/hub_prod_new  os debian-12 → debian-13
$ lore resource edit --kind host --where provider=hetzner --set os=debian-13 --yes
lore: wrote ~/work/infra/.lore/data/resources/res_b4f4…a5b0.json
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
edit 2 host resources; batch bat_4f89…b64b (undo: lore batch undo bat_4f89…b64b)
$ lore resource check --kind host --where provider=hetzner --yes
lore: wrote ~/work/infra/.lore/data/resources/res_b4f4…a5b0.json
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
check 2 host resources; batch bat_77cb…76c4 (undo: lore batch undo bat_77cb…76c4)
```
<!-- /example -->

`edit --kind … --where … --archive` archives every match.

## Step 10 — Import and export

`import` reads a csv, json or yaml file (`--kind` is required for csv; json and yaml rows name their kind). `--dry-run` shows what would be created or updated:

<!-- example: resources#import -->
```console
# alice, in infra
$ cat ~/hosts.csv
name,provider,ip
hub_db,aws,203.0.113.20
hub_cache,aws,203.0.113.21
$ lore resource import ~/hosts.csv --kind host --dry-run
  create  host hub_db
  create  host hub_cache
nothing written (dry run)
$ lore resource import ~/hosts.csv --kind host --yes
lore: wrote ~/work/infra/.lore/data/resources/res_d191…45c2.json
lore: wrote ~/work/infra/.lore/data/resources/res_b4d3…11aa.json
  create  host hub_db
  create  host hub_cache
imported 2 rows (2 created, 0 updated) from csv; batch bat_9a34…b418
```
<!-- /example -->

A ref field in the file may name a row the same file creates, by its bare name or its qualified name (`<kind>/<name>`, `<place>:<kind>/<name>`). An import is one batch, all or nothing. When one row does not fit, nothing is written; `--dry-run` shows why each row fails, and `--skip-errors` writes the valid rows and lists the rest. `--upsert-by name` updates rows that already exist under the same kind and name instead of refusing them:

<!-- example: resources#import-refused -->
```console
# alice, in infra
$ lore resource import ~/hosts2.csv --kind host --upsert-by name --yes
ERROR: [E_RESOURCE_BATCH] resource import: host hub_mail: provider "linode" is not one of hetzner, aws; nothing written (all or nothing; --skip-errors writes the valid rows)
  hint: lore resource kind show host
(exit 23)
$ lore resource import ~/hosts2.csv --kind host --upsert-by name --dry-run
  update  host hub_db
  error  host hub_mail  [E_RESOURCE_INVALID] host hub_mail: provider "linode" is not one of hetzner, aws
nothing written (dry run)
1 error (skipped)
$ lore resource import ~/hosts2.csv --kind host --upsert-by name --skip-errors --yes
lore: wrote ~/work/infra/.lore/data/resources/res_d191…45c2.json
  update  host hub_db
  error  host hub_mail  [E_RESOURCE_INVALID] host hub_mail: provider "linode" is not one of hetzner, aws
imported 1 row (0 created, 1 updated) from csv; batch bat_bfed…eeea
1 error (skipped)
```
<!-- /example -->

A second import keeps the rows you edited in lore since the last import; `--overwrite` replaces them too.

`export` writes a kind in the import format (`--format csv`, `json` (default) or `yaml`, filtered with `--where`). Sensitive values are masked as `•••` unless you pass `--show-sensitive`, which asks first:

<!-- example: resources#export -->
```console
# alice, in infra
$ lore resource export --kind host --format csv
name,ip,os,provider
hub_cache,203.0.113.21,ubuntu-24.04,aws
hub_db,203.0.113.30,ubuntu-24.04,aws
hub_prod,203.0.113.10,debian-13,hetzner
hub_prod_new,203.0.113.12,debian-13,hetzner
hub_stage,,ubuntu-24.04,aws
$ lore resource export --kind service --format yaml --where runs_on=hub_prod
resources:
    - kind: service
      name: hub_api
      fields:
        db_password: gopass://acme/hub/db
        runs_on: hub_prod
        url: https://api.example.com
    - kind: service
      name: hub_web
      fields:
        runs_on: hub_prod
        url: https://www.example.com
```
<!-- /example -->

## Step 11 — Replace a resource

When a server is replaced, `repoint <old> <new>` moves every ref from the old resource to the new one (both of one kind) and archives the old one with `replaced_by` set. After that, a new ref to the archived resource is refused with a hint to its replacement:

<!-- example: resources#repoint -->
```console
# alice, in infra
$ lore resource repoint hub_prod hub_prod_new --dry-run
would repoint 2 rows from hub_prod to hub_prod_new:
  service/hub_api runs_on
  service/hub_web runs_on
then archive hub_prod; nothing written (dry run)
$ lore resource repoint hub_prod hub_prod_new --yes
lore: wrote ~/work/infra/.lore/data/resources/res_4c59…6c38.json
lore: wrote ~/work/infra/.lore/data/resources/res_3b62…e913.json
lore: wrote ~/work/infra/.lore/data/resources/res_b4f4…a5b0.json
repointed 2 rows from hub_prod to hub_prod_new:
  service/hub_api runs_on
  service/hub_web runs_on
archived hub_prod (replaced_by hub_prod_new); batch bat_376c…8e1a
$ lore resource add service hub_admin --set runs_on=hub_prod
ERROR: [E_RESOURCE_ARCHIVED] service hub_admin: runs_on: hub_prod is archived
  hint: ref its replacement host hub_prod_new
(exit 23)
$ lore resource list host --archived
KIND	NAME	IP	OS	PROVIDER	CHECKED
host	hub_cache	203.0.113.21	ubuntu-24.04	aws	
host	hub_db	203.0.113.30	ubuntu-24.04	aws	2026-11-02
host	hub_prod	203.0.113.10	debian-13	hetzner	2026-11-02
host	hub_prod_new	203.0.113.12	debian-13	hetzner	2026-11-02
host	hub_stage		ubuntu-24.04	aws	2026-11-02
```
<!-- /example -->

`merge <duplicate> --into <survivor>` folds a duplicate away: refs move to the survivor, fields the survivor has not set are filled from the duplicate, and the duplicate is archived:

<!-- example: resources#merge -->
```console
# alice, in infra
$ lore resource edit hub_db --unset ip
lore: wrote ~/work/infra/.lore/data/resources/res_d191…45c2.json
✓ edited host hub_db (res_…45c2)
$ lore resource merge hub_cache --into hub_db --dry-run
would merge hub_cache into hub_db
would repoint 0 rows from hub_cache to hub_db:
then archive hub_cache; nothing written (dry run)
values filled from hub_cache: ip
$ lore resource merge hub_cache --into hub_db --yes
lore: wrote ~/work/infra/.lore/data/resources/res_d191…45c2.json
lore: wrote ~/work/infra/.lore/data/resources/res_b4d3…11aa.json
merged host hub_cache into hub_db
values filled from hub_cache: ip
repointed 0 rows from hub_cache to hub_db:
archived hub_cache (replaced_by hub_db); batch bat_67e2…291e
```
<!-- /example -->

## Step 12 — Archive and delete

`archive` keeps the row and its refs but refuses new refs to it. `delete` moves a row to the trash (`lore trash restore` brings it back) and asks first; without a terminal it needs `--yes`. `--with-dependents` also deletes every resource that refers to it, recursively, in one batch; try it with `--dry-run` first:

<!-- example: resources#archive-and-delete -->
```console
# alice, in infra
$ lore resource archive hub_stage
lore: wrote ~/work/infra/.lore/data/resources/res_28f8…03df.json
✓ archived hub_stage (res_…03df)
$ lore resource delete hub_db
ERROR: [E_NEEDS_CONFIRM] run resource delete?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore resource delete hub_prod_new --with-dependents --dry-run
would delete 3 resources: host hub_prod_new, service hub_api, service hub_web (dry run, nothing written)
$ lore resource delete hub_db --yes
lore: wrote ~/work/infra/.lore/data/resources/res_d191…45c2.json
✓ res_…45c2 (hub_db) moved to the trash (restore: lore trash restore res_d191…45c2)
```
<!-- /example -->

A plain `delete` of a resource that others still point at goes through and names them ("still pointing at it: …"); those refs then dangle until you repoint them, and `lint` lists them. `--purge --confirm` skips the trash.

## Step 13 — Change fields and kinds

Fields are keyed by their `fld_` id inside each row, so renaming a field touches no row. `field edit` changes rules (`--add-value`, `--remove-value`, `--rename-value old=new`, `--required` / `--no-required`, `--sensitive`, `--pattern`, `--clear <rule>`, …). `field retype` checks every value first and refuses when one does not fit (`--keep-as-text` keeps such values and marks them quarantined). `field remove` refuses while rows hold values, unless `--drop` removes them in the same commit:

<!-- example: resources#fields -->
```console
# alice, in infra
$ lore resource field edit host provider --add-value linode
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_2ad3…f891.json
✓ edited field host.provider (fld_…f891)
$ lore resource field add host region --type text
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_8567…366f.json
added field fld_…366f host.region (text)
$ lore resource edit hub_prod_new --set region=eu-1
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
✓ edited host hub_prod_new (res_…ed8c)
$ lore resource field retype host region number --dry-run
ERROR: [E_RESOURCE_TYPE] 1 value does not fit number: res_6a86…ed8c hub_prod_new
  hint: fix them, or pass --keep-as-text to keep values that do not fit as text and quarantine them; nothing written
(exit 20)
$ lore resource field rename host os os_image
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_54fe…28f2.json
✓ renamed field host.os → os_image; no values changed (fld_…28f2)
$ lore resource field remove host region
ERROR: [E_RESOURCE_IN_USE] host.region has values on 1 row
  hint: pass --drop to remove the values in the same commit
(exit 23)
$ lore resource field remove host region --drop --yes
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_8567…366f.json
✓ removed field host.region (fld_…366f); values dropped from 1 row
$ lore resource field add host renews --type date --remind-before 30d
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_71f0…c07d.json
added field fld_…c07d host.renews (date)
```
<!-- /example -->

`kind edit` changes the description, name format, render or default secret manager, and `kind rename` changes the name only (ids stay):

<!-- example: resources#kind-edit-and-rename -->
```console
# alice, in infra
$ lore resource kind edit host --description 'a server or VM we run'
lore: wrote ~/work/infra/.lore/data/kinds/knd_7a67…9504.json
✓ edited kind host (knd_…9504)
$ lore resource kind rename host server
lore: wrote ~/work/infra/.lore/data/kinds/knd_7a67…9504.json
✓ renamed kind host → server; ids unchanged (knd_…9504)
$ lore resource kinds
ID	KIND	STORE	ROWS	FIELDS
knd_7a67…9504	server	repo	4	ip, os_image, provider, renews
knd_05e9…4603	service	repo	2	db_password, runs_on, url
```
<!-- /example -->

`kind edit --reviewed` marks a kind an agent added as reviewed by a person; `kinds --unreviewed` lists the ones still waiting.

## Step 14 — Keep secrets and sensitive values out

Resource values pass the same secret check as every note (see [secrets and personal data](secrets-and-pii.md)). The answers the first refusal offers work on a resource value too: `--secret-ref <manager>://<entry>` replaces the secret with `${secret:<manager>://<entry>}` inside the value, and `--secret-unstored` with `${secret:unstored}`. Writing `${secret:<manager>://<entry>}` as the value yourself works as well:

<!-- example: resources#secret-value -->
```console
# alice, in infra
$ lore resource field add server access_key --type text
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_ada3…c018.json
added field fld_…c018 server.access_key (text)
$ lore resource edit hub_prod_new --set access_key=AKIA''IOSFODNN7EXAMPLE
ERROR: [E_NEEDS_CONFIRM] fields looks like a secret (aws_access_key) at line 1: sha256:1a5d44a2dca1: redact it with a reference, mark it unstored, mark it not a secret, or cancel?
  hint: re-run your own command with one of: --secret-ref "<manager>://<entry>" | --secret-unstored | --not-a-secret --reason "<why>" --yes
(exit 26)
$ lore resource edit hub_prod_new --set access_key=AKIA''IOSFODNN7EXAMPLE --secret-ref gopass://acme/hub/aws-key --yes
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
✓ edited server hub_prod_new (res_…ed8c)
$ lore resource show hub_prod_new
res_6a86…ed8c  server  repo:server/hub_prod_new
  access_key = ${secret:gopass://acme/hub/aws-key}
  ip = 203.0.113.12
  os_image = debian-13
  provider = hetzner
  checked 2026-11-02T10:00:00.000000001Z (by edit)
created 2026-11-02T10:02:45Z by act_f147…5139 · updated 2026-11-02T10:05:50Z by act_f147…5139
```
<!-- /example -->

The cleaner shape is a `secret_ref` field: it holds only a vault entry name, and a bare path gets the kind's secret manager (from `kind add --secret-manager`, else the policy default) in front of it. `report` lists these under "rotate":

<!-- example: resources#secret-ref-field -->
```console
# alice, in infra
$ lore resource add service hub_jobs --set runs_on=hub_prod_new --set db_password=acme/hub/jobs-db
lore: wrote ~/work/infra/.lore/data/resources/res_78c8…5b6c.json
created res_…5b6c  service  repo:service/hub_jobs
  db_password = gopass://acme/hub/jobs-db; runs_on = hub_prod_new
```
<!-- /example -->

A `--sensitive` field is not a secret, just something you do not want on screen. It prints as `•••` in `list`, `show` and `export`; `--show-sensitive` prints it and asks first:

<!-- example: resources#sensitive -->
```console
# alice, in infra
$ lore resource field add server root_user --type text --sensitive
lore: wrote ~/work/infra/.lore/data/kind_fields/fld_d7c7…7fe6.json
added field fld_…7fe6 server.root_user (text)
$ lore resource edit hub_prod_new --set root_user=deploy
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
✓ edited server hub_prod_new (res_…ed8c)
$ lore resource list server
KIND	NAME	ACCESS_KEY	IP	OS_IMAGE	PROVIDER	RENEWS	ROOT_USER	CHECKED
server	hub_prod_new	${secret:gopass://acme/hub/aws-key}	203.0.113.12	debian-13	hetzner		•••	2026-11-02
$ lore resource show hub_prod_new --show-sensitive
ERROR: [E_NEEDS_CONFIRM] resource show?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore resource show hub_prod_new --show-sensitive --yes
res_6a86…ed8c  server  repo:server/hub_prod_new
  access_key = ${secret:gopass://acme/hub/aws-key}
  ip = 203.0.113.12
  os_image = debian-13
  provider = hetzner
  root_user = deploy
  checked 2026-11-02T10:00:00.000000002Z (by edit)
created 2026-11-02T10:02:45Z by act_f147…5139 · updated 2026-11-02T10:06:10Z by act_f147…5139
```
<!-- /example -->

## Step 15 — Find problems and get a summary

`lint` finds dangling refs, bare-name refs, missing required fields, deprecated fields still in use, dates within their `--remind-before` window and duplicates; `lint --fix` resolves bare-name refs by lookup. `report` sums up everything in view: counts per kind, stale, unchecked, dangling, quarantined, open linked tasks and incidents, and the vault entries to rotate (`--kind`, `--days`; `--meta` and `--project` narrow it when someone leaves a project):

<!-- example: resources#lint-and-report -->
```console
# alice, in infra
$ lore resource edit --kind server --where provider=hetzner --set renews=2026-11-20 --yes
lore: wrote ~/work/infra/.lore/data/resources/res_6a86…ed8c.json
edit 1 server resources; batch bat_7289…c813 (undo: lore batch undo bat_7289…c813)
$ lore resource lint
remind           server hub_prod_new.renews     2026-11-20 is within 30d
1 findings
$ lore resource report
resources   5 (service 4, server 1)
stale       0
unchecked   4: service hub_api, service hub_jobs, service hub_prod_new, service hub_web
dangling    0
quarantined 0
  details: lore resource lint
open links  1: task tsk_b69e…5ed7
rotate      2: gopass://acme/hub/db, gopass://acme/hub/jobs-db
```
<!-- /example -->

The rows are files in `.lore/data`, committed like every other note:

<!-- example: resources#files -->
```console
# alice, in infra
$ git status --short .lore/data
 M .lore/data/_meta.json
?? .lore/data/kind_fields/
?? .lore/data/kinds/
?? .lore/data/knowledge_refs/
?? .lore/data/resources/
```
<!-- /example -->

## Step 16 — Share the kinds through a store

Kinds defined in one repo are only seen there. To use the same kinds in several repos, put them in a store (see [one store for several repos](../sync/multi-repo-projects.md)). `adopt --from <place> --to <place>` moves the kind and field rows, keeping their ids so existing resources still match; `add --to <store>` writes a resource into the store. `rehome` lists kinds and refs a place can no longer see after its stores changed, and writes nothing:

<!-- example: resources#adopt -->
```console
# alice, in infra
$ lore store init infra file://$HOME/stores/infra
✓ created store infra (sto_4bfb…19ce) at file://~/stores/infra; recorded in .lore/stores.yaml; commit it to share
$ lore resource adopt --from repo --to infra
ERROR: [E_NEEDS_CONFIRM] move 2 kinds (server, service) and 9 field rows from repo into infra?
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore resource adopt --from repo --to infra --yes
lore: wrote 13 row file(s) in ~/stores/infra/_store (store infra)
lore: wrote 11 row file(s) in ~/work/infra/.lore/data
✓ adopted 2 kinds (server, service) and 9 field rows from repo into infra; ids unchanged
$ lore resource kinds
ID	KIND	STORE	ROWS	FIELDS
knd_7a67…9504	server	infra	4	access_key, ip, os_image, provider, renews, root_user
knd_05e9…4603	service	infra	4	db_password, runs_on, url
$ lore resource rehome
✓ every kind and ref of this repo is visible from here
```
<!-- /example -->

When `rehome` finds something, adopt the kinds from where they are now, or repoint the refs.

## Try it

Two kinds, two rows and the question "what breaks if this host goes down?", in a fresh repo:

<!-- example: resources#try-it -->
```console
# alice, in shop
$ lore resource kind add host --description 'a server we run'
lore: wrote ~/work/shop/.lore/data/kinds/knd_7a67…9504.json
added kind knd_…9504 host in this repo
$ lore resource field add host provider --type text --required --values hetzner,aws
lore: wrote ~/work/shop/.lore/data/kind_fields/fld_2ad3…f891.json
added field fld_…f891 host.provider (text)
$ lore resource kind add service --description 'an app we deploy'
lore: wrote ~/work/shop/.lore/data/kinds/knd_05e9…4603.json
added kind knd_…4603 service in this repo
$ lore resource field add service runs_on --type ref --ref-kind host --required
lore: wrote ~/work/shop/.lore/data/kind_fields/fld_c0f3…b5b8.json
added field fld_…b5b8 service.runs_on (ref)
$ lore resource add host shop_prod --set provider=hetzner
lore: wrote ~/work/shop/.lore/data/resources/res_2e0c…0b6d.json
created res_…0b6d  host  repo:host/shop_prod
  provider = hetzner
$ lore resource add service shop_api --set runs_on=shop_prod
lore: wrote ~/work/shop/.lore/data/resources/res_e9fa…224c.json
created res_…224c  service  repo:service/shop_api
  runs_on = shop_prod
$ lore resource impact shop_prod
what breaks if host shop_prod goes down
  1  service    shop_api             runs_on → shop_prod
```
<!-- /example -->

## Pitfalls

- **A bare name held by two resources is ambiguous.** Two kinds in one place, or the same name at two levels (the repo and an attached store), make every command that takes the bare name refuse with `E_RESOURCE_AMBIGUOUS` (exit 21) and list the qualified names; name the one you mean by its qualified name, its short form `<kind>/<name>` (when the kinds differ) or its `res_` id:

<!-- example: resources#ambiguous only=2,3,4 -->
```console
# alice, in infra
$ lore resource show hub_prod_new
ERROR: [E_RESOURCE_AMBIGUOUS] "hub_prod_new" matches 2 resources: repo:server/hub_prod_new, repo:service/hub_prod_new
  hint: name it by its qualified name (repo:server/hub_prod_new or repo:service/hub_prod_new) or its res_ id
(exit 21)
$ lore resource show server/hub_prod_new | head -1
res_6a86…ed8c  server  repo:server/hub_prod_new
$ lore resource show repo:service/hub_prod_new | head -1
res_06b1…3d1c  service  repo:service/hub_prod_new
```
<!-- /example -->

- **An import refusal names the row, not the reason.** `E_RESOURCE_BATCH` says which row failed; run the same import with `--dry-run` to see the error of each row.
- **`kinds` counts archived rows.** The ROWS column includes archived resources; `list --archived` shows them.
- **`edit` stamps `last_checked`.** A row edited for one field drops out of `stale` even if nobody checked the rest; use `check --note` when you really looked.
- **Deleting a resource others point at leaves dangling refs.** The delete names them; repoint them (or use `--with-dependents`), then run `lint`.
- **Internal fields never reach a client repo.** A `secret_ref` field is internal by default (`--no-internal` changes that). Writing an internal value in a client repo is refused with `E_RESOURCE_INTERNAL` (exit 19); keep such rows in a store or your own repo.
- **Caps:** at most 200 kinds per place, 150 fields per kind and 200 allowed values per field (`E_KIND_LIMIT`). A resource name has at most 128 characters.
- **Both sides define a kind:** `adopt` refuses with `E_KIND_CONFLICT` when the target already has a kind of that name under another id; rename one first.

## Related

- [Errors: resources and kinds](../reference/errors.md#resources-and-kinds)
- [Secrets and personal data](secrets-and-pii.md)
- [One store for several repos](../sync/multi-repo-projects.md)
- [Variables and metadata](../knowledge/variables-and-metadata.md)
- [Tasks](../tasks/tasks.md)
- [Operations index](README.md)
