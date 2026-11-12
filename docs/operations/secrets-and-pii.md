# Secrets and personal data

**In short:** lore refuses to save a note that looks like a password, key or token, because notes end up in git where everyone with the repo can read them. The usual answer is a reference: the value stays in your password manager and the note keeps only the entry's name, which `lore secrets` lists, checks, renames and hands over when someone leaves. You can also add your own patterns, and a few switches turn off lore's other safety refusals when you know what you are doing.

## What it is and when you want it

A secret is anything that lets someone in: a password, an access key for a cloud account, a GitHub token, a private key file. Personal data (PII, "personally identifiable information") is anything that points at a real person, such as a customer's phone number.

lore checks the text of every note before it saves it, like a spell checker that looks for keys instead of typos. It has a built-in list of common shapes: cloud access keys, GitHub and package-registry tokens, chat-app tokens, private key blocks, `Authorization: Bearer …` headers, `password=…` pairs, web addresses with a password in them, and more. A match stops the command.

Then there is a second check: before lore writes a note into `.lore/data` (the files that get committed), it checks again. So even a note you forced in stays out of git.

A secret reference is the safe way to mention a credential in a note: `${secret:gopass://acme/website/staging-deploy}` says "the value is the entry `acme/website/staging-deploy` in gopass". Anyone can read the note; only someone whose password manager has that entry can read the value. The `lore secrets` commands work on these references across every note in the repo: list them, check that each entry still exists, reveal one value when you need it, rename an entry, and move everything a person held to someone else when they leave.

## Before you start

- lore is set up in the repo.
- Keep real secrets in a password manager or your CI's secret store, and write in the note **where** the secret lives, not the secret itself ("The staging deploy key is in the team vault under staging/deploy").

## Step 1 — See lore refuse a secret

Alice (an agency developer) tries to save a deploy key as a memory. lore stops, names the kind of secret it found, shows only the first characters, and asks what to do. At a terminal a menu offers: redact it with a reference, keep the note without the value (unstored), mark it not a secret (a typed reason is required), or cancel; `--yes` alone does not pick one. Without a terminal (a script, an agent) the question becomes `E_NEEDS_CONFIRM`; with `--json` the error carries `findings[]` (masked) and `next.add_args_one_of`, the three flag sets you can add:

<!-- example: ops#secret-refused -->
```console
# alice, in website
$ lore memory add --body 'Deploy key for staging: AKIA''IOSFODNN7EXAMPLE'
ERROR: [E_NEEDS_CONFIRM] body looks like a secret (aws_access_key) at line 1: sha256:1a5d44a2dca1: redact it with a reference, mark it unstored, mark it not a secret, or cancel?
  hint: re-run your own command with one of: --secret-ref "<manager>://<entry>" | --secret-unstored | --not-a-secret --reason "<why>" --yes
(exit 26)
```
<!-- /example -->

The example key is the public sample key from cloud documentation; it opens nothing. The `''` in the middle of it only splits the key in this page (the shell joins the two halves back into one value before lore sees it), so the page itself does not trip lore's commit hook; you type the key as one word.

## Step 2 — Say it is not a secret

When the match is not a real secret (a sample value like this one, a test string), `--not-a-secret --reason "<why>" --yes` answers the question and stores the row as not a secret (a bare `--yes` does not). For a real secret use `--secret-ref "<manager>://<entry>"` (the note keeps a reference to where it lives) or `--secret-unstored` (the note is kept, the value is left out). Its file is written to `.lore/data` with the value, together with a `.lore/data/secret_allows` entry that records your answer and its reason. The commit hook checks every staged file again: it lets this row through because its allow entry is staged with it, so the value does reach git, on purpose (`--not-a-secret` is your explicit choice). Any other file holding a credential pattern, a plain file or a row edited by hand, is refused, so a real key does not reach git by accident:

<!-- example: ops#allow-secrets only=1,2 -->
```console
# alice, in website
$ lore memory add --not-a-secret --reason 'AWS documentation example key' --yes --body 'Deploy key for staging: AKIA''IOSFODNN7EXAMPLE'
lore: wrote ~/work/website/.lore/data/memories/mem_4c59…6c38.json
lore: wrote ~/work/website/.lore/data/secret_allows/sal_3b62…e913.json
✓ mem_4c59…6c38
  scope: [master]
$ grep -rl AKIA .lore/data || echo 'not in .lore/data'
.lore/data/memories/mem_4c59…6c38.json
```
<!-- /example -->

Delete the row (or edit the key out) before you commit:

<!-- example: ops#allow-secrets only=3 -->
```console
# alice, in website
$ lore memory delete mem_4c59…6c38 --yes
lore: wrote ~/work/website/.lore/data/memories/mem_4c59…6c38.json
✓ mem_4c59…6c38 moved to the trash (restore: lore trash restore mem_4c59…6c38)
```
<!-- /example -->

If a note that is meant to be shared trips the check by mistake, edit its text so it no longer looks like a secret. `LORE_SYNC_ALLOW_SECRETS=1` is ignored since 0.1.18 (it prints a warning); keep a false positive with `--not-a-secret --reason "<why>" --yes`, and remember that what is in git stays in git history.

## Step 3 — Keep the secret in your password manager

The best answer for a real credential is a reference. You store the value in your password manager first, then give lore the entry's name with `--secret-ref "<manager>://<entry>"`. lore replaces the secret in the note with `${secret:<manager>://<entry>}`, so the note says where the value lives and the value itself is never written anywhere:

<!-- example: secrets#secret-ref -->
```console
# alice, in website
$ lore memory add --secret-ref 'gopass://acme/website/staging-deploy' --body 'Deploy key for staging: AKIA''IOSFODNN7EXAMPLE'
lore: wrote ~/work/website/.lore/data/memories/mem_7a67…9504.json
✓ mem_7a67…9504
  scope: [master]
$ lore memory list
mem_7a67…9504 [retrieved] Deploy key for staging: ${secret:gopass://acme/website/staging-deploy}
$ grep -rh staging .lore/data/memories
  "body": "Deploy key for staging: ${secret:gopass://acme/website/staging-deploy}",
$ lore secrets list
REF	HOLDER	USED IN	STATE
gopass://acme/website/staging-deploy	-	mem_7a67…9504 body	-
1 reference in 1 row.
```
<!-- /example -->

`lore secrets list` shows every reference in the repo: the entry, who holds it, the row and field it is used in, and its state from the last check. It never opens a password manager. Filter it with `--manager gopass`, `--holder <person or id>` or `--unstored`.

The managers lore knows are `gopass`, `op` (1Password), `bitwarden`, `vault`, `aws-secrets` (option `region`), `gcp-secrets` (option `project`), `azure-keyvault` and `env` (an environment variable on the machine that reads it). An option goes after a `?`, as in `aws-secrets://staging/db?region=eu-west-1`.

## Step 4 — Keep the note now, store the value later

When the password manager is not at hand, `--secret-unstored` keeps the note with `${secret:unstored}` where the value was, and adds a task to store the secret and then rotate it (the value was typed into a terminal, so treat it as seen). In the example, `$RELAY_PASSWORD` stands for the password you typed; the runner sets it to a made-up value, and any secret-looking value behaves the same:

<!-- example: secrets#unstored -->
```console
# alice, in website
$ lore memory add --secret-unstored --body "Mail relay password=$RELAY_PASSWORD"
lore: wrote 4 row file(s) in ~/work/website/.lore/data
✓ mem_2ad3…f891
  scope: [master]
lore live: (live, pushed) commit 601d285 to origin refs/lore/live
$ lore secrets list --unstored
REF	HOLDER	USED IN	STATE
unstored	-	mem_2ad3…f891 body (1)	-
1 reference in 1 row.
$ lore task list
○ tsk_162d…9666 Store the secret redacted from mem_2ad3…f891, then rotate it [medium]
```
<!-- /example -->

Once the value is in your manager, `lore secrets link <row> --at <n> --ref "<manager>://<entry>"` fills placeholder number `n` (as `lore secrets list --unstored` counts them). For a personal manager such as gopass, lore records this machine as the holder, because only this machine can read the entry; for the shared ones (`op`, `vault`, `aws-secrets`) no holder is recorded. The task stays open until you rotate the value:

<!-- example: secrets#link -->
```console
# alice, in website
$ lore secrets link mem_2ad3…f891 --at 1 --ref 'gopass://acme/website/mail-relay'
lore: wrote ~/work/website/.lore/data/memories/mem_2ad3…f891.json
lore: wrote ~/work/website/.lore/data/.json
Replaced ${secret:unstored} #1 in mem_2ad3…f891 body with ${secret:gopass://acme/website/mail-relay?holder=mch_017941fe53b8eca065d80ff150a20e9fa70f8cec380c9b853a906dd8c07a7eff}.
Its task stays open: rotate the secret, then lore task done <task>.
$ lore secrets show mem_2ad3…f891
REF	HOLDER	USED IN	STATE
gopass://acme/website/mail-relay?holder=mch_017941fe53b8eca065d80ff150a20e9fa70f8cec380c9b853a906dd8c07a7eff	mch_017941fe	mem_2ad3…f891 body	-
1 reference in 1 row. Not revealed (add --reveal).
```
<!-- /example -->

`lore secrets show <row>` lists the references of one row, and `lore secrets show <manager://entry>` every row and field that holds that entry (in USED IN, as `lore secrets list` shows them).

## Step 5 — Check that every entry still exists

`lore secrets check` asks each manager for each referenced entry by its name, drops the value unread, and marks the reference `present`, `missing` or `not_checked` (the manager's program is not installed here, or it did not answer within `secrets.manager_timeout`, 5 seconds by default). It also adds the store-then-rotate task to any row with `${secret:unstored}` that has none. Here the staging database password is an `env://` reference, missing until the variable is set (the value is a dummy). `$DB_PASSWORD` stands for the password written in the note, a made-up value the runner sets; with `--secret-ref` it never reaches the row:

<!-- example: secrets#env-ref -->
```console
# alice, in website
$ lore memory add --secret-ref 'env://STAGING_DB_PASSWORD' --body "Staging database password=$DB_PASSWORD"
lore: wrote ~/work/website/.lore/data/memories/mem_d431…313a.json
✓ mem_d431…313a
  scope: [master]
$ lore secrets check
lore: wrote ~/work/website/.lore/data/.json
REF	HOLDER	USED IN	STATE
gopass://acme/website/staging-deploy	-	mem_7a67…9504 body	not_checked
gopass://acme/website/smtp-relay?holder=act_05e9…4603	act_05e99e22	mem_2ad3…f891 body	not_checked
env://STAGING_DB_PASSWORD	-	mem_d431…313a body	missing
3 references in 3 rows.
$ lore secrets check
lore: wrote ~/work/website/.lore/data/.json
REF	HOLDER	USED IN	STATE
gopass://acme/website/staging-deploy	-	mem_7a67…9504 body	not_checked
gopass://acme/website/smtp-relay?holder=act_05e9…4603	act_05e99e22	mem_2ad3…f891 body	not_checked
env://STAGING_DB_PASSWORD	-	mem_d431…313a body	present
3 references in 3 rows.
```
<!-- /example -->

To read a value, `lore secrets show <ref> --reveal` asks every time and records the reveal in the audit log. Without a terminal it stops with `E_NEEDS_CONFIRM`; a person answers with `--yes`, and an agent alone needs `--force`, which is recorded as forced:

<!-- example: secrets#reveal -->
```console
# alice, in website
$ lore secrets show env://STAGING_DB_PASSWORD --reveal
ERROR: [E_NEEDS_CONFIRM] lore will read 1 secret(s) from env (STAGING_DB_PASSWORD) and print it in clear. This is recorded in the audit log. Continue?
  hint: ask the person, then rerun with --yes (an agent alone: --force, audited as FORCED)
(exit 26)
$ lore secrets show env://STAGING_DB_PASSWORD --reveal --force
env://STAGING_DB_PASSWORD = dummy-value
W_REVEAL_FORCED: revealed without a person's answer; recorded in the audit log as forced
```
<!-- /example -->

The team setting `secrets.agent_reveal` (default `ask`) decides what an agent may reveal; see [Your settings and team rules](plugins-and-db-config.md).

## Step 6 — A renamed entry, or a person who leaves

When an entry is renamed in the password manager, `lore secrets rename-entry --manager <scheme> --from <old> --to <new>` rewrites every reference to it in one batch. `lore secrets relabel` rewrites each reference's holder label from its holder id, for example after `lore whoami --machine-label` (here nothing needs a new label):

<!-- example: secrets#rename-entry -->
```console
# alice, in website
$ lore secrets rename-entry --manager gopass --from acme/website/mail-relay --to acme/website/smtp-relay
lore: wrote ~/work/website/.lore/data/memories/mem_2ad3…f891.json
lore: wrote ~/work/website/.lore/data/.json
Renamed 1 reference in 1 row (batch bat_54fe…28f2).
$ lore secrets list --manager gopass
REF	HOLDER	USED IN	STATE
gopass://acme/website/staging-deploy	-	mem_7a67…9504 body	-
gopass://acme/website/smtp-relay?holder=mch_017941fe53b8eca065d80ff150a20e9fa70f8cec380c9b853a906dd8c07a7eff	mch_017941fe	mem_2ad3…f891 body	-
2 references in 2 rows.
$ lore secrets relabel
Nothing to change.
```
<!-- /example -->

When a person leaves or a laptop is replaced, `lore secrets move --from <holder> --to <holder>` hands their references to someone else. It never copies a secret: it rewrites who holds the entry and adds a task to rotate every entry the old holder had. Here Bob takes over what Alice's machine held:

<!-- example: secrets#move -->
```console
# bob, in website
$ lore whoami | grep identity
identity   bob <human:bob@agency.example>   act_05e9…4603  (from: git config user.email)
repo       no org  (fallback identity)

# alice, in website
$ lore whoami --json | grep '"machine"'
    "machine": "mch_017941fe53b8eca065d80ff150a20e9fa70f8cec380c9b853a906dd8c07a7eff",
$ lore secrets move --from mch_017941fe53b8eca065d80ff150a20e9fa70f8cec380c9b853a906dd8c07a7eff --to act_05e9…4603 --yes
lore: wrote 1 row file(s) in ~/work/website/.lore
lore: wrote 3 row file(s) in ~/work/website/.lore/data
Moved 1 reference in 1 row (batch bat_c0f3…b5b8). Added task tsk_76bd…3f10 (tasklist secrets, repo).
lore live: (live, pushed) commit 86dbae4 to origin refs/lore/live
$ lore secrets list --holder act_05e9…4603
REF	HOLDER	USED IN	STATE
gopass://acme/website/smtp-relay?holder=act_05e9…4603	act_05e99e22	mem_2ad3…f891 body	-
1 reference in 1 row.
$ lore task list
○ tsk_162d…9666 Store the secret redacted from mem_2ad3…f891, then rotate it [medium]
○ tsk_76bd…3f10 Rotate secrets mch_017941fe53b8eca065d80ff150a20e9fa70f8cec380c9b853a906dd8c07a7eff held: 1 entries [medium]
```
<!-- /example -->

`--from` and `--to` take a person, a `person.machine` label set with `lore whoami --machine-label`, or a holder id (`mch_…` for a machine, `act_…` for a person); `--entry 'acme/website/*'` and `--manager` narrow the move.

## Step 7 — Review what was marked not a secret

Every `--not-a-secret` answer is recorded as an allow entry: one row, one field, one rule, with your reason and only a hash of the matched text. `lore secrets allow list` shows them, `lore secrets allow rm <sal_…>` removes one (the finding is reported again on the next scan), and `lore secrets allow <row> --rule <rule> --match <text> --reason <why>` (or `--match-hash` with the `sha256:…` fingerprint a refused commit printed, or the number `lore check leaks --rows` prints) adds one by hand for a finding on a row that is already saved:

<!-- example: secrets#allow -->
```console
# alice, in website
$ lore memory add --body 'Test fixture token: ghp_''16C7e42F292c6912E7710c838347Ae178B4a' --not-a-secret --reason 'fixture in tests' --yes
lore: wrote ~/work/website/.lore/data/memories/mem_b4f4…a5b0.json
lore: wrote ~/work/website/.lore/data/secret_allows/sal_28f8…03df.json
lore: wrote ~/work/website/.lore/data/secret_allows/sal_4c59…6c38.json
✓ mem_b4f4…a5b0
  scope: [master]
$ lore secrets allow list
ID	ROW	FIELD	RULE	REASON
sal_28f8…03df	mem_b4f4…a5b0	body	github_token	fixture in tests (confirmed not a secret by alice (memory add))
sal_4c59…6c38	mem_b4f4…a5b0	body	github-pat	fixture in tests (confirmed not a secret by alice (memory add))
$ lore secrets allow rm sal_4c59…6c38 --yes
lore: wrote ~/work/website/.lore/data/secret_allows/sal_4c59…6c38.json
lore: wrote ~/work/website/.lore/data/memories/mem_b4f4…a5b0.json
✓ removed sal_…6c38; the finding is reported again on the next scan
$ lore secrets allow list | grep -c sal_
1
$ lore secrets allow mem_b4f4…a5b0 --rule github-pat --match 'ghp_''16C7e42F292c6912E7710c838347Ae178B4a' --reason 'fixture in tests'
lore: wrote ~/work/website/.lore/data/secret_allows/sal_3b62…e913.json
lore: wrote ~/work/website/.lore/data/memories/mem_b4f4…a5b0.json
✓ github-pat: mem_…a5b0.body marked not a secret (sal_…e913)
```
<!-- /example -->

One piece of text can match more than one rule (here `github_token` and `github-pat`); each match is its own entry.

## Add your own patterns

`lore secrets rule add` adds your own pattern (a regular expression: a small text language for "this shape of text", like `INV-[0-9]{6}` for "INV- and six digits"). It is a settings row of the place you add it to (this repo by default, or `--to <store>`), so everyone who works there gets it, and lore checks every new note against it at once:

<!-- example: ops#pii-pattern -->
```console
# alice, in website
$ lore secrets rule add invoice-number --regex 'INV-[0-9]{6}' --description 'our invoice numbers' --yes
lore: wrote ~/work/website/.lore/data/secret_rules/srl_b4f4…a5b0.json
✓ invoice-number added in this repo (srl_…a5b0)
$ lore secrets rules
RULE	STATE	LEVEL	WHERE	NOTE
invoice-number	on	repo	this repo	our invoice numbers
$ lore memory add --body 'Invoice INV-123456 is still open'
ERROR: [E_NEEDS_CONFIRM] body looks like a secret (invoice-number) at line 1: sha256:0f8bf3d8b84d: redact it with a reference, mark it unstored, mark it not a secret, or cancel?
  hint: re-run your own command with one of: --secret-ref "<manager>://<entry>" | --secret-unstored | --not-a-secret --reason "<why>" --yes
(exit 26)
```
<!-- /example -->

`lore secrets rules` lists only the rule rows: rules you added, changed or switched off, with their level and where they live. With none, the built-in rules apply (the gitleaks set plus lore's own pack). `lore secrets rule off <rule> --reason <why>` switches a rule off (lore doctor lists it), `lore secrets rule edit <rule>` changes its pattern (`--regex`), `--keywords`, `--entropy` or `--description` (built-in rules included), and `lore secrets rule on <rule>` removes the off row again. Each takes `--to` for the place: `repo` (default), `me` (your own notes only), `<store>` or `<store>/<folder>`:

<!-- example: secrets#rules -->
```console
# alice, in website
$ lore secrets rules
(no rule rows: gitleaks defaults and lore's own pack apply)
$ lore secrets rule off generic-password --reason 'our tests use fake passwords' --yes
lore: wrote ~/work/website/.lore/data/secret_rules/srl_f147…5139.json
✓ generic-password off in this repo (srl_…5139)
$ lore secrets rule edit aws_access_key --description 'cloud keys, any account' --yes
lore: wrote ~/work/website/.lore/data/secret_rules/srl_2537…6bbb.json
✓ aws_access_key changed in this repo (srl_…6bbb)
$ lore secrets rules
RULE	STATE	LEVEL	WHERE	NOTE
aws_access_key	changed	repo	this repo	cloud keys, any account
generic-password	off	repo	this repo	our tests use fake passwords
$ lore secrets rule on generic-password --yes
lore: wrote ~/work/website/.lore/data/secret_rules/srl_f147…5139.json
✓ generic-password on in this repo (srl_…5139 removed)
$ lore secrets rules
RULE	STATE	LEVEL	WHERE	NOTE
aws_access_key	changed	repo	this repo	cloud keys, any account
```
<!-- /example -->

`lore secrets rules --replaced` also shows which gitleaks rule ids cover the same ground as each lore rule.

The v0.1.17 spelling `lore pii-pattern` is still there, but `add`, `enable` and `disable` are refused and name the new command; `lore pii-pattern list` only shows patterns copied from v0.1.17, which nothing checks:

<!-- example: secrets#pii-pattern -->
```console
# alice, in website
$ lore pii-pattern add --name phone --regex '[0-9]{10}'
ERROR: [E_INVALID_INPUT] lore pii-pattern add was replaced by lore secrets rule add (secret rules are settings rows)
  hint: use `lore secrets rule add`; `lore secrets rules` lists every rule
(exit 20)
$ lore pii-pattern disable phone
ERROR: [E_INVALID_INPUT] lore pii-pattern disable was replaced by lore secrets rule off (secret rules are settings rows)
  hint: use `lore secrets rule off`; `lore secrets rules` lists every rule
(exit 20)
$ lore pii-pattern list
(no patterns)
```
<!-- /example -->

## Safety switches

lore refuses a few risky setups on its own. Each refusal has one environment variable that turns it off (an environment variable is a setting you put in front of a command or in your shell, like `LORE_ALLOW_ROOT=1 lore doctor`). Each takes the value `1`; anything else counts as off.

| Variable | What lore refuses without it | Why |
|---|---|---|
| `LORE_ALLOW_ROOT` | Running as the root (administrator) user: `E_ROOT_REFUSED`. | Files created as root can no longer be changed by your normal user, and a mistake as root can damage the whole machine. |
| `LORE_ALLOW_NETWORK_FS` | A database on a network drive or a cloud-synced folder (shared network folders, folders a sync app copies between computers): `E_NETWORK_FS`. | SQLite databases get damaged silently when two machines or a sync app touch the file. Keep the repo on a local disk; git shares the notes anyway. |
| `LORE_ALLOW_DB_PATH_OUTSIDE` | A `..` (a step up out of the folder) in the database path of a `.lore/lore.yaml` pointer file: `E_BAD_PATH`. | A path that climbs out of the project can point lore at someone else's files. |
| `--secret-ref`, `--secret-unstored`, `--not-a-secret --reason … --yes` (flags, not variables) | Saving a note that looks like a secret: `E_NEEDS_CONFIRM` until you add one of them; a bare `--yes` is not an answer. | See Step 2. |

Use them for one command, not in your shell profile, so the refusal is back next time.

A repo in a `Dropbox` folder, refused and then let through for one command:

<!-- example: environment#network-drive -->
```console
# alice, in Dropbox/website
$ lore rule list
lore rule list: git is not set up for lore's row files in this clone; this command does not set it up: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/Dropbox/website/.git/config; hook blocks in ~/work/Dropbox/website/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
ERROR: [E_NETWORK_FS] rowfs: the store is inside a "dropbox" folder (cloud sync)
  hint: move the store to a local folder, or set LORE_ALLOW_NETWORK_FS=1 to accept the risk
(exit 23)
$ LORE_ALLOW_NETWORK_FS=1 lore rule list
(no rules)
```
<!-- /example -->

A pointer file whose `db_path` climbs out with `..`, refused and then let through:

<!-- example: environment#db-path-outside -->
```console
# alice, in blog
$ cat .lore/lore.yaml
db_path: ../client.db
project_id: prj_4c59…6c38
$ lore migrate run --dry-run
ERROR: [E_BAD_PATH] migrate: db_path "../client.db" has a `..` part
  hint: set LORE_ALLOW_DB_PATH_OUTSIDE=1 to allow it
(exit 20)
$ LORE_ALLOW_DB_PATH_OUTSIDE=1 lore migrate run
lore: added .lore/lore.db, .lore/lore.db-shm, .lore/lore.db-wal, .lore/lore.db.broken.*, .lore/state/, .lore/backups/, .lore/lock to this clone's .git/info/exclude (never committed) so lore's local files stay out of git status; this repo holds no lore data, so lore changes none of its tracked files (`lore where` lists everything lore keeps here)
Upgraded 1 row
  mode-b: rules/rul_3b62…e913 → store shared (~/work/stores/blog)
  mode-b: .lore/stores.yaml: add store shared file://~/work/stores/blog
  mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)
team lock: this repo has no remote origin; nobody to race, none taken
backup: refs/lore-backup/migrate/2026-11-02 (local only, never pushed)
undo: none for a shared-database conversion (`lore migrate rollback` refuses it); the shared lore.db was not changed
$ lore rule list
lore rule list: git is not set up for lore's row files in this clone; this command does not set it up: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/blog/.git/config; hook blocks in ~/work/blog/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
rul_3b62…e913 [must/always] Posts go out on Tuesdays [shared]
```
<!-- /example -->

## Try it

In a repo with lore set up, save a note whose secret is a reference, then list the references. The note keeps only the entry's name:

<!-- example: secrets#secret-ref only=1,4 -->
```console
# alice, in website
$ lore memory add --secret-ref 'gopass://acme/website/staging-deploy' --body 'Deploy key for staging: AKIA''IOSFODNN7EXAMPLE'
lore: wrote ~/work/website/.lore/data/memories/mem_7a67…9504.json
✓ mem_7a67…9504
  scope: [master]
$ lore secrets list
REF	HOLDER	USED IN	STATE
gopass://acme/website/staging-deploy	-	mem_7a67…9504 body	-
1 reference in 1 row.
```
<!-- /example -->

## Pitfalls

- **The check looks at shapes, not meaning.** A password written as plain words ("the password is blue-horse-42") does not match any pattern. Never rely on lore as your only guard.
- **Rows confirmed with `--yes` live in your local database too.** The repo cache under `~/.lore/cache/repo/` and a damaged copy lore moved aside there hold them. Do not put either in a shared folder.
- **A forced row keeps a warning in `lore doctor` while it exists.** Each refused file shows as a `secret-detected` warning and makes doctor DEGRADED. Edit the text or delete the row; once the row is gone, doctor drops the warning.
- **`lore pii-pattern list` is not your rule list.** It shows only patterns left over from v0.1.17, which nothing checks; `lore secrets rules` shows the rules lore uses.
- **`lore secrets list` never opens a password manager.** Its STATE column is this machine's last `lore secrets check`, or `-` when there was none; run the check to refresh it. A manager whose program is not installed shows `not_checked`, not `missing`.
- **`lore secrets move` does not make the old holder forget.** They saw the values; the move adds a "Rotate secrets … held" task, and the job is done only when each entry has a new value.
- **A reveal is audited.** `lore secrets show --reveal` writes an audit entry each time (`--force` ones are marked forced), and the value is printed in clear on your terminal; do not run it in a shared screen or a CI log.
- **`env://` reads the environment of the machine that runs lore.** It suits CI jobs and short tests; the reference is only `present` where the variable is set.
- **Support bundles are safe to share by default:** they carry counts, not note text. See [doctor](doctor.md#send-a-support-bundle-with-a-bug-report).

## Related

- [The audit log](audit-log.md)
- [A damaged cache](backup-restore-repair.md)
- [Use lore in CI](ci.md)
- [Rules, memories and decisions](../knowledge/rules-memories-decisions.md)
- [Environment variables](../reference/environment.md)
- [Operations index](README.md)
