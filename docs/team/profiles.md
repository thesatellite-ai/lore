# Team profiles: people, contacts, projects and stints

**In short:** every person on the org's roster can have a full profile: a title, a timezone, several emails and GitHub accounts, a phone, and the projects they work on with their roles there. People can go on leave, leave the company, be archived and come back later, and every stint keeps its dates. All of a person's emails and GitHub logins point to the same actor, so one person never becomes two. Client repos still see only a name.

You want this page when your org's roster is more than a list of handles: when you need to know who works on which project in which role, how to reach someone, who is on leave, or when someone leaves the company and their work has to be handed over. Every output below is a real run of these commands (`tests/team/docs/run-examples.sh profiles`), on a test clock that walks through one year of a person's time at the company.

## Before you start

- An org with a store, and you on its roster (`lore actor register`, see [Identity and stores](identity-and-stores.md)). With no org yet, create one: see [Step 9](#step-9--create-or-rename-an-org).
- The people here are fictional: Tomas Weber (`@tomas`, the team lead, who runs the commands) and Mira Patel (`@mira`, a backend engineer with two emails and two GitHub accounts). The org is `agency`, its roster lives in the store's `org/` folder (`git@github.com:agency/notes`), and its projects are the client repos `acme-hub` and `northwind-app`. Mira registered herself with `lore actor register` on her first day, 2026-01-12.
- Every command that writes on this page is for people; see [Who may change profiles](#who-may-change-profiles).

## Where profiles live

Everything on this page is stored in the org's roster store: the store folder that holds the org's people and agents (`org/` in the `agency` store), the same place `lore actor register` writes. Writes are pushed at once, and they work from inside any repo of the org and from outside any repo.

| Where you run the command | What happens |
|---|---|
| a repo of an org, or outside any repo with an org set | profiles are read and written in the org's roster store |
| a repo with no org | writes are refused with `E_REFUSED` "team profiles live in an org store" (hint: `lore org create <name>`); a repo without an org keeps its roster in the repo's own `.lore/data`, and contacts there would be published with the code. `lore actor show` still works |
| a client repo | the client still sees only `{id, display_name, kind}` for each person; see [Client repos](#client-repos) |

Contacts, leave reasons and notes are stored and shown plain in the org store (no masking). Anyone who can read the org's store can read them, so keep that store's access as tight as its content needs.

## Who may change profiles

An agent acts for its person, so it may run every command on this page: the policy flag `agents.may_manage_people` is true by default. A team that wants these writes left to people sets it to false (`lore policy set agents.may_manage_people false --to <store>/org`); an agent then gets `E_POLICY_DENIED` naming the flag, and a level that sets false cannot be turned back on below it. Creating an org or a team (`lore org create`, the team step of an admin setup) is never refused by the flag: the store it creates holds no policy yet, so the default applies. The same goes for the roster: an agent's `lore actor register`, the team step of an admin setup and `lore agent start --label` are made as its person (the person is the org's admin and roster member; the rows record the agent as the writer), and only an agent with no person behind it is refused. Every read (`show`, `list`) is open to agents.

## Naming a person

Wherever a command takes `<actor>`, any of these work:

| Form | Example |
|---|---|
| handle | `@mira` |
| actor id | `act_7a670ae8548ed05bf57ad29ad0759504` |
| identity key | `human:mira@agency.example` |
| an identity contact as `<type>:<value>` | `email:mira.patel@example.com`, `github:mirap` |
| a bare email of an identity contact | `mira.patel@example.com` |

A reference that matches more than one actor answers `E_AMBIGUOUS_ID` and lists the candidates. The same forms work in `lore task add … --assign`, so `--assign github:mirap` assigns Mira.

## Step 1 — Fill in a profile

A profile is a handful of optional fields on the person's actor. Fill them in once, when someone joins; `lore actor show` and `lore tui --kind actor` show them from then on.

| Field | Flag on `lore actor edit` | What it holds |
|---|---|---|
| title | `--title` | the profile headline ("Backend engineer"); the company role of a stint is on the [company period](#step-4--record-the-company-period) |
| timezone | `--timezone` | an IANA name such as `Europe/Istanbul`; anything else is `E_INVALID_INPUT` |
| location | `--location` | free text ("Ankara, TR") |
| photo URL | `--photo-url` | an `http` or `https` URL |
| bio | `--bio` | free text (markdown) |
| birthday | `--birthday` | `YYYY-MM-DD`, or `--MM-DD` without a year |
| display name | `--display-name` | the name shown everywhere, also in client repos |

`--clear` empties the fields it names (comma-separated, with the field names `photo_url`, `bio`, and so on). A timezone that is not an IANA name is refused:

<!-- example: profiles#profile-fields -->
```console
# tomas, in acme-hub
$ lore actor edit @mira --title 'Backend engineer' --timezone Europe/Istanbul --location 'Ankara, TR'
lore: pushed store agency/org: commit 601d285 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_7a67…9504.json (store agency/org)
✓ act_7a67…9504 Mira Patel edited (title, timezone, location) in agency org/
$ lore actor edit @mira --bio 'Payments and billing.' --photo-url https://example.com/mira.png
lore: pushed store agency/org: commit 86dbae4 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_7a67…9504.json (store agency/org)
✓ act_7a67…9504 Mira Patel edited (photo_url, bio) in agency org/
$ lore actor edit @mira --clear photo_url,bio
lore: pushed store agency/org: commit 9b40ce7 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_7a67…9504.json (store agency/org)
✓ act_7a67…9504 Mira Patel edited (photo_url, bio) in agency org/
$ lore actor edit @mira --timezone 'Istanbul time'
ERROR: [E_INVALID_INPUT] actor edit --timezone: "Istanbul time" is not an IANA zone name
  hint: give an IANA zone name such as Europe/Istanbul or America/New_York
(exit 20)
```
<!-- /example -->

## Step 2 — Add contacts

A person can have any number of contacts. Each one has:

| Part | Meaning |
|---|---|
| type | a [contact type](#step-3--extend-the-contact-types): `email`, `github`, `phone`, `linkedin`, … |
| value | as you typed it (`Mira.Patel@Example.com`, `@mirap`); lore also keeps a normalised copy for matching (see [Identity matching](#identity-matching)) |
| label | free text; the usual ones are `personal`, `work` and `client-<name>` |
| primary | at most one primary contact per person and type; marking one primary clears the others. The first contact of a type becomes primary unless you mark another one |
| identity | "use this to recognise the person": allowed only on identity types (`email`, `github`, `gitlab`, or an added type with `--identity`) |
| note | free text |

Tomas adds Mira's two emails and two GitHub accounts as identity contacts, and her work phone. Adding the same contact again changes nothing, and `contact list --type phone` is the org's phone list:

<!-- example: profiles#contacts only=1,2,3,4,5,6,7 -->
```console
# tomas, in acme-hub
$ lore actor contact add @mira email mira@agency.example --label work --primary --identity
lore: pushed store agency/org: commit 05513aa to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_contacts/acn_2ad3…f891.json (store agency/org)
✓ acn_2ad3…f891 email mira@agency.example added for Mira Patel (@mira) [primary, identity] in agency org/
$ lore actor contact add @mira email mira.patel@example.com --label personal --identity
lore: pushed store agency/org: commit dfd8dc8 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_contacts/acn_162d…9666.json (store agency/org)
✓ acn_162d…9666 email mira.patel@example.com added for Mira Patel (@mira) [identity] in agency org/
$ lore actor contact add @mira github mira-agency --label work --primary --identity
lore: pushed store agency/org: commit cf563f3 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_contacts/acn_54fe…28f2.json (store agency/org)
✓ acn_54fe…28f2 github mira-agency added for Mira Patel (@mira) [primary, identity] in agency org/
$ lore actor contact add @mira github mirap --label personal --identity
lore: pushed store agency/org: commit df3a55c to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_contacts/acn_05e9…4603.json (store agency/org)
✓ acn_05e9…4603 github mirap added for Mira Patel (@mira) [identity] in agency org/
$ lore actor contact add @mira phone '+90 532 000 0000' --label work --primary
lore: pushed store agency/org: commit 6d00426 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_contacts/acn_c0f3…b5b8.json (store agency/org)
✓ acn_c0f3…b5b8 phone +90 532 000 0000 added for Mira Patel (@mira) [primary] in agency org/
$ lore actor contact add @mira github mirap --label personal --identity
· acn_05e9…4603 github mirap already there [identity] in agency org/
$ lore actor contact list --type phone
acn_c0f3…b5b8 Mira Patel (@mira)       phone     +90 532 000 0000             work       primary           tel:+905320000000
```
<!-- /example -->

| Command | What it does |
|---|---|
| `lore actor contact add <actor> <type> <value> [--label l] [--primary] [--identity] [--note t] [--merge] [--yes]` | add a contact; adding the same one again prints "already there" and changes nothing |
| `lore actor contact edit <acn_id> [--value v] [--label l] [--primary] [--no-primary] [--identity] [--no-identity] [--note t]` | change one contact |
| `lore actor contact remove <acn_id> [--reason t]` | remove one contact (it goes to the trash and can be restored); rows the person wrote keep their name |
| `lore actor contact list [<actor>] [--type t] [--identity] [--json]` | one person's contacts, or everyone's (a phone list for the org) |

What can go wrong: an unknown or retired type, a value that fails the type's check, or `--identity` on a type that is not an identity type is `E_CONTACT_TYPE`; an identity contact that already belongs to another person (or an email that already has its own actor, a duplicate made before the contact was known) is `E_CONTACT_TAKEN`, see [Duplicates](#duplicates-and---merge). Any identity contact names the person, so `github:mirap` finds Mira:

<!-- example: profiles#contacts only=8,9,10 -->
```console
# tomas, in acme-hub
$ lore actor contact add @mira phone 12 --label work
ERROR: [E_CONTACT_TYPE] contact value: a phone number holds at least 7 digits (+90 532 000 0000)
  hint: lore contact-type list shows each type's kind
(exit 20)
$ lore actor contact add @tomas github mirap --identity
ERROR: [E_CONTACT_TAKEN] identity contact github:mirap already belongs to Mira Patel (@mira) act_7a67…9504 (acn_05e9…4603)
  hint: lore actor show github:mirap to see who has it; if it is on the wrong person, lore actor contact remove <acn_id> there
(exit 23)
$ lore actor show github:mirap
act_7a67…9504
  kind:        human
  display:     Mira Patel
  stable_key:  human:mira@agency.example
  title:       Backend engineer
  timezone:    Europe/Istanbul
  location:    Ankara, TR
  contacts:    email    mira@agency.example  work  primary identity
               email    mira.patel@example.com  personal  identity
               github   mira-agency  work  primary identity
               github   mirap  personal  identity
               phone    +90 532 000 0000  work  primary
  source:      agency/org (Mira Patel)
  copies:      agency/org   source
created 2026-11-02T10:00:00Z by mira · updated 2026-11-02T10:00:00Z by tomas
```
<!-- /example -->

A contact is changed or removed by its `acn_` id. A removed contact goes to the trash and can be restored; rows the person wrote keep their name. `--identity` on `contact list` keeps only the contacts lore recognises the person by:

<!-- example: profiles#contact-edit-and-remove -->
```console
# tomas, in acme-hub
$ lore actor contact add @mira linkedin mira-patel --label work
lore: pushed store agency/org: commit 34ee210 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_contacts/acn_76bd…3f10.json (store agency/org)
✓ acn_76bd…3f10 linkedin mira-patel added for Mira Patel (@mira) [primary] in agency org/
$ lore actor contact edit acn_76bd…3f10 --note 'old profile, not updated'
lore: pushed store agency/org: commit 7898daa to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_contacts/acn_76bd…3f10.json (store agency/org)
✓ acn_76bd…3f10 linkedin mira-patel edited for Mira Patel (@mira) [primary] in agency org/
$ lore actor contact remove acn_76bd…3f10 --reason 'account closed'
lore: pushed store agency/org: commit ccfb0bb to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_contacts/acn_76bd…3f10.json (store agency/org)
✓ acn_76bd…3f10 linkedin mira-patel removed from Mira Patel (@mira) in agency org/ (restorable from the trash)
$ lore actor contact list @mira --identity
acn_2ad3…f891 Mira Patel (@mira)       email     mira@agency.example          work       primary, identity mailto:mira@agency.example
acn_162d…9666 Mira Patel (@mira)       email     mira.patel@example.com       personal   identity          mailto:mira.patel@example.com
acn_54fe…28f2 Mira Patel (@mira)       github    mira-agency                  work       primary, identity https://github.com/mira-agency
acn_05e9…4603 Mira Patel (@mira)       github    mirap                        personal   identity          https://github.com/mirap
```
<!-- /example -->

## Step 3 — Extend the contact types

The built-in contact types:

| Name | Kind | Identity | Link shown by `actor show` |
|---|---|---|---|
| `github` | handle | yes | `https://github.com/{value}` |
| `gitlab` | handle | yes | `https://gitlab.com/{value}` |
| `email` | email | yes | `mailto:{value}` |
| `phone` | phone | no | `tel:{value}` |
| `linkedin` | handle | no | `https://www.linkedin.com/in/{value}` |
| `x` | handle | no | `https://x.com/{value}` |
| `slack` | text | no | |
| `discord` | text | no | |
| `website` | url | no | `{value}` |

The kind decides how a value is checked and normalised: `email`, `handle`, `phone`, `url` or `text`. The list is yours to extend: an added type joins the list, and editing a built-in one saves your change over it for the whole org.

| Command | What it does |
|---|---|
| `lore contact-type list [--retired] [--json]` | the effective list, each marked `built-in`, `changed` or `added` |
| `lore contact-type add <name> --kind email\|handle\|phone\|url\|text [--label t] [--identity] [--url-template t]` | add a type; a name already in the list is `E_NAME_TAKEN` |
| `lore contact-type edit <name> [--label t] [--url-template t] [--retire] [--unretire]` | change a type; a retired type is out of `contact add`, and existing contacts of it still show |

Tomas adds Bitbucket as an identity type and relabels GitHub; the list marks each type `built-in`, `changed` or `added`:

<!-- example: profiles#contact-types -->
```console
# tomas, in acme-hub
$ lore contact-type add bitbucket --kind handle --identity --url-template 'https://bitbucket.org/{value}'
lore: pushed store agency/org: commit 9d655a5 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/contact_types/ctp_d431…313a.json (store agency/org)
✓ contact type bitbucket added in agency org/
$ lore contact-type edit github --label GitHub
lore: pushed store agency/org: commit 2fc021b to branch main
lore: wrote ~/.lore/stores/agency/notes/org/contact_types/ctp_b4f4…a5b0.json (store agency/org)
✓ contact type github edited in agency org/
$ lore contact-type list
github       handle  identity  changed   GitHub       https://github.com/{value}
gitlab       handle  identity  built-in  GitLab       https://gitlab.com/{value}
email        email   identity  built-in  Email        mailto:{value}
phone        phone   -         built-in  Phone        tel:{value}
linkedin     handle  -         built-in  LinkedIn     https://www.linkedin.com/in/{value}
x            handle  -         built-in  X            https://x.com/{value}
slack        text    -         built-in  Slack
discord      text    -         built-in  Discord
website      url     -         built-in  Website      {value}
bitbucket    handle  identity  added     -            https://bitbucket.org/{value}
$ lore contact-type add email --kind email
ERROR: [E_NAME_TAKEN] contact type email is already in the list (built-in)
  hint: lore contact-type edit email
(exit 23)
```
<!-- /example -->

A type's name is never renamed, because contacts store it. The kind and identity flag of `github`, `gitlab` and `email` cannot be changed (`E_INVALID_INPUT`): identity matching depends on them.

## Step 4 — Record the company period

A company period is one stint at the company: its type (`employee`, `contractor`, `intern` or `client_contact`), the first day, the last day (empty while it runs), the company role for that stint (free text, "Senior engineer"), and, once it ends, the reason and a note. Leaving and rejoining makes a second period on the same actor, so the person keeps one id for life.

| Command | What it does |
|---|---|
| `lore actor period list <actor> [--json]` | every stint, oldest first |
| `lore actor period add <actor> --type t --started-on d [--ended-on d] [--org-role t]` | record a stint by hand (a past one, or the first one of a new person) |
| `lore actor period edit <apd_id> [--type t] [--started-on d] [--ended-on d] [--org-role t] [--end-reason t] [--note t]` | fix a stint, for example a mistyped date |

<!-- example: profiles#periods -->
```console
# tomas, in acme-hub
$ lore actor period add @mira --type employee --started-on 2026-01-12 --org-role 'Senior engineer'
lore: pushed store agency/org: commit 51886a5 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_periods/apd_28f8…03df.json (store agency/org)
✓ period apd_28f8…03df of Mira Patel (@mira) added: 2026-01-12 → now  employee  Senior engineer
  it replaces the stint read from the roster (active/left_at)
$ lore actor period list @mira
act_7a67…9504  Mira Patel (@mira)  active
  apd_28f8…03df  2026-01-12 → now  employee  Senior engineer
$ lore actor period add @mira --type contractor --started-on 2026-02-01
ERROR: [E_PERIOD_OVERLAP] period 2026-01-12 → now is still open while 2026-02-01 → now starts after it
  hint: lore actor period list <actor> shows the stints; pick a date after the last end
(exit 23)
```
<!-- /example -->

The first `period add` replaces the stint lore had read from the roster (see [Existing actors](#existing-actors)). Stints never overlap, a person has at most one open stint, and a stint never ends before it starts. A date that breaks one of these is `E_PERIOD_OVERLAP`.

## Step 5 — Add people to projects, with roles

A membership says that a person works on a project, since when, and in which roles. A person can hold several roles on one project, and different roles on different projects.

### Roles

The built-in roles are `lead`, `dev`, `designer`, `qa`, `pm`, `reviewer` and `client`. Add your own, or change a built-in one, the same way as contact types:

| Command | What it does |
|---|---|
| `lore role list [--retired] [--json]` | the effective list |
| `lore role add <name> [--label t] [--description t]` | add a role; a name already in the list is `E_NAME_TAKEN` |
| `lore role edit <name> [--label t] [--description t] [--retire] [--unretire]` | change a role; a retired role is out of `--role`, and memberships that have it keep it |

Tomas adds a `tech_writer` role, gives it a description, and retires `qa`, which his team does not use:

<!-- example: profiles#roles -->
```console
# tomas, in acme-hub
$ lore role add tech_writer --label 'Technical writer'
lore: pushed store agency/org: commit 22efc3b to branch main
lore: wrote ~/.lore/stores/agency/notes/org/roles/rol_4c59…6c38.json (store agency/org)
✓ role tech_writer added in agency org/
$ lore role list
lead  Lead — leads the project
dev  Developer — writes the code
designer  Designer — designs the product
qa  QA — tests the product
pm  Project manager — runs the project
reviewer  Reviewer — reviews changes
client  Client — the client's own person on the project
tech_writer  Technical writer
$ lore project member add acme-hub @mira --role wizard
ERROR: [E_ROLE_UNKNOWN] not a live role: wizard (unknown)
  hint: lore role list; lore role add <name>
(exit 20)
$ lore role edit tech_writer --description 'writes the user docs'
lore: pushed store agency/org: commit 8c373dc to branch main
lore: wrote ~/.lore/stores/agency/notes/org/roles/rol_4c59…6c38.json (store agency/org)
✓ role tech_writer updated in agency org/
$ lore role edit qa --retire
lore: pushed store agency/org: commit 6f11034 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/roles/rol_3b62…e913.json (store agency/org)
✓ role qa updated in agency org/
```
<!-- /example -->

A retired role (here `qa`) is out of `--role` from then on; memberships that have it keep it. A `--role` name that is not in the list is `E_ROLE_UNKNOWN`. Roles only describe what someone does for now: they do not grant or refuse anything.

### Memberships

| Command | What it does |
|---|---|
| `lore project member add <project> <actor> --role a,b [--on d] [--note t]` | add someone to a project (`--on` defaults to today); on a current member, the new roles are added: "already a member; roles now dev, reviewer" |
| `lore project member edit <project> <actor> --role a,b` | set the current membership's roles |
| `lore project member remove <project> <actor> [--on d] [--reason t]` | end the membership on a date (default today); it is kept, not deleted |
| `lore project member list [<project>] [--actor a] [--role r] [--history] [--json]` | current members; `--history` adds ended memberships |

`<project>` is a project name or a `prj_` id, looked up in the stores the repo you are in reads. Mira joins `acme-hub` on her first day and becomes lead of `northwind-app` on 2026-03-01 (run from inside `northwind-app`, where that project is known). `lore actor show` now prints the whole profile; a project whose row lives only in another repo (here `northwind-app`, shown from inside `acme-hub`) is shown by its `prj_` id:

<!-- example: profiles#members -->
```console
# tomas, in acme-hub
$ lore project member add acme-hub @mira --role dev,reviewer
lore: pushed store agency/org: commit 468e9a8 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/project_members/pmb_f147…5139.json (store agency/org)
✓ @mira added to acme-hub as dev, reviewer from 2026-01-12 (pmb_f147…5139)

# tomas, in northwind-app
$ lore project member add northwind-app @mira --role lead
lore: pushed store agency/org: commit 9ea0ca8 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/project_members/pmb_2537…6bbb.json (store agency/org)
✓ @mira added to northwind-app as lead from 2026-03-01 (pmb_2537…6bbb)

# tomas, in acme-hub
$ lore project member add acme-hub @tomas --role lead
lore: pushed store agency/org: commit aba91f6 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/project_members/pmb_b69e…5ed7.json (store agency/org)
✓ @tomas added to acme-hub as lead from 2026-03-01 (pmb_b69e…5ed7)
$ lore project member list acme-hub
@mira  dev, reviewer  since 2026-01-12
@tomas  lead  since 2026-03-01
$ lore actor show @mira
act_7a67…9504
  kind:        human
  display:     Mira Patel
  stable_key:  human:mira@agency.example
  title:       Backend engineer
  timezone:    Europe/Istanbul
  location:    Ankara, TR
  contacts:    email    mira@agency.example  work  primary identity
               email    mira.patel@example.com  personal  identity
               github   mira-agency  work  primary identity
               github   mirap  personal  identity
               phone    +90 532 000 0000  work  primary
  periods:     2026-01-12 → now  employee  Senior engineer
  projects:    acme-hub  dev, reviewer  since 2026-01-12
               prj_0aae…a61f  lead  since 2026-03-01
  source:      agency/org (Mira Patel)
  copies:      agency/org   source
created 2026-11-02T10:00:00Z by mira · updated 2026-11-02T10:00:00Z by tomas
```
<!-- /example -->

`project member edit` sets the current roles; `remove` ends the membership on a date and keeps it in `--history`. Removing someone who is not a member is `E_NOT_FOUND`:

<!-- example: profiles#member-edit-and-remove -->
```console
# tomas, in acme-hub
$ lore project member edit acme-hub @tomas --role lead,pm
lore: pushed store agency/org: commit a8de760 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/project_members/pmb_b69e…5ed7.json (store agency/org)
✓ @tomas on acme-hub: roles now lead, pm

# tomas, in northwind-app
$ lore project member add northwind-app @tomas --role reviewer
lore: pushed store agency/org: commit 9169078 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/project_members/pmb_6a86…ed8c.json (store agency/org)
✓ @tomas added to northwind-app as reviewer from 2026-03-01 (pmb_6a86…ed8c)
$ lore project member remove northwind-app @tomas --reason 'Mira reviews her own project now'
lore: pushed store agency/org: commit b733d8a to branch main
lore: wrote ~/.lore/stores/agency/notes/org/project_members/pmb_6a86…ed8c.json (store agency/org)
✓ @tomas removed from northwind-app on 2026-03-01 (Mira reviews her own project now; history kept)
$ lore project member remove northwind-app @tomas
ERROR: [E_NOT_FOUND] @tomas is not a member of northwind-app
  hint: lore project member list northwind-app --history
(exit 27)
$ lore project member list northwind-app --history
@mira  lead  since 2026-03-01
@tomas  reviewer  2026-03-01 → 2026-03-01 (Mira reviews her own project now)
```
<!-- /example -->

Adding someone again on the day an ended membership began reopens that membership with the new roles instead of making a second one. Removing someone and adding them again later starts a new membership, so `--history` shows both stints. A stint's `ended_on` is its last day: a re-add starts the day after at the earliest (`E_PERIOD_OVERLAP` otherwise, naming that day); re-adding on the day the stint began reopens it. A new membership runs on with no end, so it may not share a day with any ended one, including one that starts later (added with a future `--on` and removed before it began): adding from a day before such a stint is `E_PERIOD_OVERLAP` too, and the hint names that stint and the day after it. Accepting an [invite](invites.md) is checked the same way: on the old stint's last day, or while an ended stint is still ahead, it is refused (`E_PERIOD_OVERLAP`, the hint names the day to accept it again), and nothing is written. A left or archived person cannot be added (`E_STATUS_CHANGE`, hint `lore actor rejoin`).

Teams (`@frontend` and so on) are org-wide groups; adding someone to a project does not change any team. When a member of one of the org's teams has no membership on the project, `project member list` ends with a warning, and `project member remove` prints the same warning when the person stays in an org team:

```text
W_TEAM_NOT_MEMBER @frontend: @carol is in the org team but not a member of acme-hub (lore project member add acme-hub @carol --role dev)
```

With `--json`, the keys `actor show` printed before team profiles are unchanged; `status`, `leave_from`, `leave_until`, `profile`, `contacts`, `periods` and `memberships` are added. A status line appears once the person is not `active`.

## Step 6 — Leave and come back

A person is in one of four statuses:

| Status | In pickers and `--assign @h` | In `lore actor list` | In `lore actor list --archived` | Still recognised by identity matching |
|---|---|---|---|---|
| `active` | yes | yes | yes | yes |
| `on_leave` | yes, with "on leave until <date>" | yes, marked | yes | yes |
| `left` | no | yes, marked | yes | yes: their old rows keep their name |
| `archived` | no | no | yes | yes |

`lore actor status <actor> <status> [--from d] [--until d] [--note t]` changes only the status. `--note` says why (parental leave); it stays until the person is active again, and `actor show` and `actor list` show it. It allows these moves:

| From \ to | active | on_leave | left | archived |
|---|---|---|---|---|
| active | nothing to do | yes (`--from` defaults to today, `--until` optional) | use `lore actor offboard` | use `lore actor offboard --archive` |
| on_leave | yes (clears the leave dates) | updates the dates | use `lore actor offboard` | use `lore actor offboard --archive` |
| left | use `lore actor rejoin` | no | nothing to do | yes |
| archived | use `lore actor rejoin` | no | yes (unarchive) | nothing to do |

Every "use …" and "no" cell answers `E_STATUS_CHANGE` with the command to run instead.

In May, Tomas records Mira's June leave; in July she is back. Leaving the company is not a status change, it is [offboarding](#step-7--offboard-someone-who-leaves):

<!-- example: profiles#status -->
```console
# tomas, in acme-hub
$ lore actor status @mira on_leave --from 2026-06-01 --until 2026-06-30
lore: pushed store agency/org: commit 1833373 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_7a67…9504.json (store agency/org)
✓ act_7a67…9504 Mira Patel (@mira): active → on_leave (2026-06-01 → 2026-06-30)
$ lore actor list
act_4f89…b64b [human] Tomas Weber                              agency/org
act_7a67…9504 [human] Mira Patel  (on_leave until 2026-06-30)  agency/org
act_77cb…76c4 [human] carol                                    repo
3 actors across 2 stores
$ lore actor status @mira active
lore: pushed store agency/org: commit 59e9d31 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_7a67…9504.json (store agency/org)
✓ act_7a67…9504 Mira Patel (@mira): on_leave → active
$ lore actor status @mira left
ERROR: [E_STATUS_CHANGE] Mira Patel (@mira) is active; leaving ends their stint and project memberships
  hint: lore actor offboard @mira
(exit 23)
```
<!-- /example -->

A leave that has passed its end date does not end on its own: `actor show` and `actor list` say "leave ended 2026-06-30; still on_leave" until someone runs `lore actor status @mira active`.

`lore actor deactivate @mira` still works as before: it marks Mira as left, but ends no stint or project. It prints a hint to use `lore actor offboard @mira` for that.

## Step 7 — Offboard someone who leaves

`lore actor offboard <actor> [--on d] [--reason t] [--note t] [--archive] [--yes] [--json]` is the one command for someone leaving the company. It first shows everything it will do, and writes nothing until it is confirmed. On 2026-09-30 Mira still leads one open task; without a terminal, the preview comes back as `E_NEEDS_CONFIRM`, and `--yes` applies it. After that she is out of `--assign`, and in October Tomas archives her:

<!-- example: profiles#offboard -->
```console
# tomas, in acme-hub
$ lore task add 'move the invoice export to the new API' --to agency --assign @mira
lore: pushed store agency: commit 7540e63 to branch main
lore: wrote 2 row file(s) in ~/.lore/personal/agency (store me)
lore: wrote 3 row file(s) in ~/.lore/stores/agency/notes/acme-hub (store agency)
✓ tsk_d191…45c2 — move the invoice export to the new API [medium] → [agency/acme-hub]
  created tasklist tlt_b4d3…11aa "default" for tasks added without --tasklist
$ lore actor offboard @mira --reason 'moved abroad'
ERROR: [E_NEEDS_CONFIRM] offboard Mira Patel (@mira) (act_7a67…9504) on 2026-09-30:
  period     2026-01-12 → 2026-09-30  employee  Senior engineer  reason: moved abroad
  projects   acme-hub (dev, reviewer) ends 2026-09-30
             prj_0aae…a61f (lead) ends 2026-09-30
  open tasks tsk_d191…45c2  "move the invoice export to the new API"  agency  lead  reassign: lore task edit tsk_d191…45c2 --assign @<someone>
  agents     (none)
  status     active → left
  hint: ask the person, then rerun with --yes
(exit 26)
$ lore actor offboard @mira --reason 'moved abroad' --yes
lore: pushed store agency/org: commit ef62c83 to branch main
lore: wrote 4 row file(s) in ~/.lore/stores/agency/notes/org (store agency/org)
offboard Mira Patel (@mira) (act_7a67…9504) on 2026-09-30:
  period     2026-01-12 → 2026-09-30  employee  Senior engineer  reason: moved abroad
  projects   acme-hub (dev, reviewer) ends 2026-09-30
             prj_0aae…a61f (lead) ends 2026-09-30
  open tasks tsk_d191…45c2  "move the invoice export to the new API"  agency  lead  reassign: lore task edit tsk_d191…45c2 --assign @<someone>
  agents     (none)
  status     active → left
✓ act_7a67…9504 Mira Patel (@mira) offboarded on 2026-09-30 (2 memberships ended, 0 agents inactive, 1 open tasks listed above)
$ lore actor list
act_4f89…b64b [human] Tomas Weber         me, agency, agency/org
act_7a67…9504 [human] Mira Patel  (left)  agency/org
act_77cb…76c4 [human] carol               repo
3 actors across 4 stores
$ lore task add 'review the refund flow' --to agency --assign @mira
ERROR: [E_NOT_FOUND] no person or team with handle @mira
  hint: they run `lore actor register` in a repo of the org, then `lore sync` here
(exit 27)
$ lore actor status @mira archived
lore: pushed store agency/org: commit dd85e7d to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_7a67…9504.json (store agency/org)
✓ act_7a67…9504 Mira Patel (@mira): left → archived
$ lore actor list --archived
act_4f89…b64b [human] Tomas Weber             me, agency, agency/org
act_7a67…9504 [human] Mira Patel  (archived)  agency/org
act_77cb…76c4 [human] carol                   repo
3 actors across 4 stores
```
<!-- /example -->

What it does:

- ends the current company period on `--on` (default today; a future date is `E_INVALID_INPUT`, a date before the stint started is `E_PERIOD_OVERLAP`), with the reason and note;
- ends every current project membership on the same date; a membership that has not started yet (added with a future `--on`) shows as "not started yet (starts <date>): ends unstarted" and goes to the trash (reason "offboarded before it started", `lore trash restore <id>` brings it back);
- lists every open task where the person is an assignee or the lead (a task they only hold a claim on is not listed yet), with a `reassign:` line for each. **No task is changed:** reassign them yourself;
- marks every agent the person owns as inactive;
- takes the person off every team that lists them (the preview names them: `teams      @web: taken off`);
- sets the status to `left`, or `archived` with `--archive`.

On a terminal, lore asks before writing. Without a terminal (a script, an agent), it answers `E_NEEDS_CONFIRM` with the preview; rerun with `--yes` to apply. With `--json` and no `--yes`, the preview is in the error's message.

After offboarding, `@mira` is out of `--assign` and pickers, still in `lore actor list` marked `left`, and every old row still shows her name. On the person's own machine, once `lore sync` brings the change, every write to the org's stores and repos is refused with `E_REFUSED` ("you are marked as left in agency since 2026-09-30; writes to its stores and repos are refused (your personal store still works)"); reads and their personal store keep working. Removed by mistake? `lore member add <their email>` brings them back, the same day too: a stint that ended today is reopened rather than a new one started (see [Adding your staff](members.md#step-3--see-and-change-people)). Offboarding someone who already left answers `E_STATUS_CHANGE` "already left on 2026-09-30". If the command stopped half-way (a crash, a lost connection), run it again: it finishes what is left. Offboarding is for people; an agent follows its owner (`E_INVALID_INPUT` on an agent).

## Step 8 — Rejoin

`lore actor rejoin <actor> --type employee|contractor|intern|client_contact [--on d] [--org-role t] [--note t] [--yes] [--json]` brings back someone who left or was archived. It works like offboarding: a preview, then a confirmation on a terminal or `--yes`. On 2026-11-02 Mira comes back as a contractor; her project memberships are listed with the line that adds each one back, and Tomas adds her to `acme-hub` again:

<!-- example: profiles#rejoin -->
```console
# tomas, in acme-hub
$ lore actor rejoin @mira --type contractor --org-role 'Contract engineer' --yes
lore: pushed store agency/org: commit d18179e to branch main
lore: wrote ~/.lore/stores/agency/notes/org/actor_periods/apd_9a34…b418.json (store agency/org)
lore: wrote ~/.lore/stores/agency/notes/org/actors/act_7a67…9504.json (store agency/org)
rejoin Mira Patel (@mira) (act_7a67…9504) on 2026-11-02:
  period     2026-11-02 → now  contractor  Contract engineer
  projects   acme-hub (dev, reviewer): lore project member add acme-hub act_7a67…9504 --role dev,reviewer
             prj_0aae…a61f (lead): lore project member add prj_0aae…a61f act_7a67…9504 --role lead
             memberships are not restored (TP-16)
  status     archived → active
✓ act_7a67…9504 Mira Patel (@mira) rejoined on 2026-11-02 as contractor
$ lore project member add acme-hub @mira --role reviewer
lore: pushed store agency/org: commit f4b8da2 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/project_members/pmb_bfed…eeea.json (store agency/org)
✓ @mira added to acme-hub as reviewer from 2026-11-02 (pmb_bfed…eeea)
$ lore project member list acme-hub --history
@mira  dev, reviewer  2026-01-12 → 2026-09-30 (offboarded)
@mira  reviewer  since 2026-11-02
@tomas  lead, pm  since 2026-03-01
$ lore actor period list @mira
act_7a67…9504  Mira Patel (@mira)  active
  apd_28f8…03df  2026-01-12 → 2026-09-30  employee  Senior engineer  reason: moved abroad
  apd_9a34…b418  2026-11-02 → now  contractor  Contract engineer
```
<!-- /example -->

- It starts a new company period on `--on` (default today), which must be after the last stint ended (`E_PERIOD_OVERLAP` otherwise) and not in the future (`E_INVALID_INPUT`: use today or a past day; run it on their first day back), and sets the status back to `active`. On the day the last stint ended (a removal undone the same day) it reopens that stint instead of starting a new one. The new stint and the status are one store commit.
- Project memberships are **not** restored. The preview lists the ones that ended when the person left, each with the `lore project member add` line to add it back.
- A membership the offboard moved to the trash because it had not started yet ("offboarded before it started") is listed too, under `trashed`, with its start date and the `lore trash restore <id>` line that brings it back; the rejoin does not restore it. `--json` lists them in `trashed_memberships` (with `starts_on` and `not_started`).
- Agents the person owns stay inactive; the preview gives `lore actor status <agent> active` for each.
- Rejoining someone who is active is `E_STATUS_CHANGE`.
- Only rejoin brings someone back. When a person who left (or was archived) runs `lore actor register` or `lore agent start` themselves, lore answers `E_STATUS_CHANGE` naming their status and the `lore actor rejoin` line for an admin, and writes nothing. (A v0.1.17 `lore actor deactivate`, which sets no status, is still undone by registering again; it is the only way back on a roster without an org.)

The actor id never changes, so the person's old commits, tasks and time entries still show under their name, and `lore actor period list` and `lore actor show` list both stints.

## Step 9 — Create or rename an org

Profiles live in an org's store, so a team without an org starts with `lore org create`. It needs a lore store to hold the org folder (`lore store init`), writes the org's suggested entry into that store, adds the entry to your `~/.lore/config.yaml` with your identity, and puts you on the roster. `lore org rename` changes only the entry's name on this machine: the org's id and every row stay.

<!-- example: profiles#org-create-and-rename -->
```console
# ana, in handbook
$ lore store init acme git@github.com:acme/lore-notes --yes
lore store init: set user.name and user.email in the store clone ~/.lore/stores/acme/lore-notes (.git/config) to ana <ana@acme.example>: your lore identity (`lore whoami`), the one lore signs this store's commits with, so your own git commits there carry it (your global git config is unchanged)
lore store init: set up git for lore's row files in this clone: merge drivers lore and lore-keep-ours in ~/work/handbook/.git/config; hook blocks in ~/work/handbook/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push) (`lore where` lists everything lore keeps here)
✓ created store acme (sto_376c…8e1a) at git@github.com:acme/lore-notes; recorded in .lore/stores.yaml; commit it to share
$ lore org create acme --store acme --own 'github.com/acme/*' --key human:ana@acme.example --name 'Ana Lima' --handle ana --yes
lore: added .lore/lore.db, .lore/lore.db-shm, .lore/lore.db-wal, .lore/lore.db.broken.*, .lore/state/, .lore/backups/, .lore/lock to this clone's .git/info/exclude (never committed) so lore's local files stay out of git status; this repo holds no lore data, so lore changes none of its tracked files (`lore where` lists everything lore keeps here)
lore: pushed store github.com/acme/lore-notes: commit 70f4f88 to branch main
lore: pushed store acme/org: commit d33587e to branch main
lore: pushed store acme/org: commit 34010c1 to branch main
lore: wrote ~/.lore/stores/acme/lore-notes/org/projects/prj_67e2…291e.json (store acme/org)
lore: wrote ~/.lore/stores/acme/lore-notes/org/actors/act_8567…366f.json (store acme/org)
This writes org/lore-org.yaml into the store (no identity) and adds to ~/.lore/config.yaml:
    name: acme
    identity:
        key: human:ana@acme.example
        name: Ana Lima
        handle: ana
    stores:
        - url: git@github.com:acme/lore-notes
          org_folder: org
    own:
        - github.com/acme/*
✓ wrote org/lore-org.yaml (pushed)
✓ added org entry acme
✓ act_8567…366f Ana Lima (@ana) registered in acme org/
  teammates join with: lore org import git@github.com:acme/lore-notes --key human:<their email> --name "Their Name"
  tie a client repo to it (written only into the store): lore org link acme --project <folder>
$ lore org rename acme acme-studio
✓ org entry acme is now acme-studio (this machine)
  id prj_67e2…291e recorded (the org folder's project id; it never changes)
  rows are unchanged; store rows that name the old label (routing.client_repos) and other machines keep it until edited there
  written to ~/.lore/config.yaml
$ lore whoami
org        acme-studio  (matched by: own pattern github.com/acme/* of acme-studio)
identity   Ana Lima <human:ana@acme.example>   act_8567…366f  @ana  (from: org entry (config.yaml orgs[acme-studio].identity))
repo       own repo  (no client guards)
machine    mch_017941fe
agent      none  (no coding agent detected)
project    (no .lore/data/_meta.json here)
remotes    github.com/acme/handbook
stores     git@github.com:acme/lore-notes on branch main  (org folder org)
links      known  no project id here yet, so no store link can name this repo
personal   not set
```
<!-- /example -->

Teammates then join with `lore org import` (see [Identity and stores](identity-and-stores.md)) or from an [invite](invites.md).

## Step 10 — Label your machines

Every machine that writes to a store keeps a small machine profile there (OS, architecture, lore version, last write), so a team can tell which laptop or CI runner wrote what; ordinary writes refresh it, the `lore migrate` commands do not. `lore machine label` gives this machine a name everyone sees, `lore machine list` lists the machines of the attached stores (`--store`, `--org` narrow it), and `lore machine show <id>` prints one in full; a unique prefix of 8 hex characters is enough. An empty label clears it. Below, only the id, label and host columns are shown, because the rest describes the build machine:

<!-- example: profiles#machines -->
```console
# tomas, in acme-hub
$ lore machine label 'office desktop'
lore: wrote ~/.lore/personal/agency/machines/mch_71f0…c07d.json (store me)
✓ machine mch_017941fe labelled "office desktop"
$ lore machine list | cut -c1-38
ID            LABEL           HOST    
mch_017941fe  office desktop  devbox  
$ lore machine show mch_017941fe | grep -e '^mch_' -e 'label:' -e 'host:' -e 'runtime:' -e 'last_write:'
mch_017941fe53b8eca065d80ff150a20e9fa70f8cec380c9b853a906dd8c07a7eff
  label:     office desktop
  host:      devbox
  runtime:   bare
  last_write: 2026-11-02T10:00:00.000000001Z
$ lore machine label ''
lore: wrote ~/.lore/personal/agency/machines/mch_71f0…c07d.json (store me)
✓ machine mch_017941fe label cleared
```
<!-- /example -->

## Try it

The steps above are one year of Mira's time at the company, run in order: she joins on 2026-01-12 (profile, contacts, period, projects), takes June off, leaves on 2026-09-30, is archived in October and comes back as a contractor on 2026-11-02. At the end, from inside `acme-hub`, her profile lists both stints and her new `acme-hub` membership, and her memberships keep their history:

<!-- example: profiles#whole-year -->
```console
# tomas, in acme-hub
$ lore actor show @mira
act_7a67…9504
  kind:        human
  display:     Mira Patel
  stable_key:  human:mira@agency.example
  title:       Backend engineer
  timezone:    Europe/Istanbul
  location:    Ankara, TR
  contacts:    email    mira@agency.example  work  primary identity
               email    mira.patel@example.com  personal  identity
               github   mira-agency  work  primary identity
               github   mirap  personal  identity
               phone    +90 532 000 0000  work  primary
  periods:     2026-01-12 → 2026-09-30  employee  Senior engineer
               2026-11-02 → now  contractor  Contract engineer
  projects:    acme-hub  reviewer  since 2026-11-02
               acme-hub  dev, reviewer  2026-01-12 → 2026-09-30
               prj_0aae…a61f  lead  2026-03-01 → 2026-09-30
  source:      agency/org (Mira Patel)
  copies:      agency/org   source
created 2026-11-02T10:00:00Z by mira · updated 2026-11-02T10:00:00Z by tomas
$ lore project member list --actor @mira --history
acme-hub  @mira  dev, reviewer  2026-01-12 → 2026-09-30 (offboarded)
acme-hub  @mira  reviewer  since 2026-11-02
prj_0aae…a61f  @mira  lead  2026-03-01 → 2026-09-30 (offboarded)
```
<!-- /example -->

To run it yourself, start from an org you can throw away (Step 9 makes one) and replay the commands of Steps 1 to 8 with your own people; every write goes to the org store and is pushed at once.

## Identity matching

A person may commit from several emails and use several GitHub accounts. Mark each of them as an identity contact (`--identity`), and lore treats all of them as the same actor:

- **Writing rows.** When your git email or org identity is one of a person's identity contacts, lore writes your rows as that person, never as a second actor. `lore whoami` says so with a line like "identity contact: mira.patel@example.com is an identity contact of Mira Patel (act_7a67…9504)".
- **Naming people.** `email:<address>`, `github:<login>`, `gitlab:<login>` (or `<type>:<value>` for any identity type) and a bare email work wherever a command takes a person: `lore actor show mira.patel@example.com`, `lore task add … --assign github:mirap`.
- **Registering.** `lore actor register` by someone whose identity matches an existing person's identity contact refreshes that person ("already registered as Mira Patel via identity contact") instead of adding a second one.

Values are compared after normalising them, so small differences in how they are typed do not matter:

| Kind | Rule | Example |
|---|---|---|
| email | trimmed and lower-cased | `Mira.Patel@Example.com` → `mira.patel@example.com` |
| handle | a leading `@`, a known profile URL (`https://github.com/`, `https://gitlab.com/`, `https://x.com/`, `https://www.linkedin.com/in/`) and a trailing `/` are dropped; lower-cased | `https://github.com/MiraP/` → `mirap` |
| phone | only a leading `+` and the digits are kept; at least 7 digits | `+90 (532) 000-0000` → `+905320000000` |
| url | must be `http` or `https`; scheme and host lower-cased, a trailing `/` dropped | |
| text | trimmed | |

Dots and `+tags` in an address are not folded: to git they are different addresses, so add each one you use.

Rules lore keeps:

- Only contacts marked `--identity` are used; a contact without it never matches.
- Contacts are read only from the org's stores, never from a client repo.
- Left and archived people still match, so a commit by someone who has left keeps their name.
- If one value is an identity contact of two people (for example after two clones each added it), lore matches neither and never guesses. `lore doctor` reports "identity contacts held by two actors", naming the two people and the fix.

GitHub logins resolve wherever you name a person. Lore does not import pull requests or commits from GitHub yet, so nothing else reads a login today.

### Duplicates and `--merge`

If Mira once committed with `mira.patel@example.com` before anyone added that email to her profile, the roster already has a second actor for that email. Adding the email as her identity contact then answers `E_CONTACT_TAKEN` with the hint `--merge`:

```console
$ lore actor contact add @mira email mira.patel@example.com --label personal --identity --merge
$ lore actor contact add @mira email mira.patel@example.com --label personal --identity --merge --yes
```

`--merge` shows how many rows the other actor has, by kind, and asks (or answers `E_NEEDS_CONFIRM` without a terminal). Once confirmed, that actor is marked as merged into Mira and inactive. No row is rewritten: lists and reports follow the merge to Mira.

When the value belongs to a different person's identity contact, `--merge` is not the fix: run `lore actor show email:mira.patel@example.com` to see who holds it, and remove the wrong contact with `lore actor contact remove <acn_id>`.

## Client repos

A client repo keeps seeing each person as `{id, display_name, kind}` and nothing else, exactly as before:

- the new profile fields (title, timezone, location, photo, bio, birthday, status, leave dates) are never written into a client repo;
- contacts, company periods, project memberships, roles and contact types are never written into a client repo; lore refuses such a write with `E_LEAK_BLOCKED`, whatever command tries it (including `lore move`);
- the commit check blocks a copied row file. If someone copies a contact file into the client repo's `.lore/data/actor_contacts/`, the commit is blocked with: "actor_contacts row acn_01jd9a00c001 in client-repo file .lore/data/actor_contacts/acn_01jd9a00c001.json: team profile rows live only in the org store; remove the file". An actor file with any profile field is blocked the same way.

## Existing actors

Nothing on the roster is lost or has to be retyped:

- An actor added before team profiles shows one company period built from the roster: type `employee`, from the day the actor was added, ending on the day they were deactivated if they were. `actor show` marks it "(from the roster: active/left_at)".
- The first offboard, rejoin, `period add` or `period edit` for that person saves that period as a real one first.
- `lore migrate run` saves it for every person at once. Running it again, or on a second clone, writes nothing new.
- A person deactivated with `lore actor deactivate` reads as `left`; everyone else reads as `active`.

## Pitfalls

- **The org store holds contacts in plain text.** Phones, personal emails and leave reasons are readable by everyone who can read that store.
- **Offboarding never reassigns tasks.** It lists them with a `reassign:` line each; reassigning is a decision for a person.
- **Rejoining does not restore projects.** Add the person to each project again.
- **A passed leave does not end itself.** Run `lore actor status <actor> active` when the person is back.
- **Mark every email and GitHub account you use as an identity contact.** One that is not marked makes a second actor the first time you commit from it; fix it with `--merge`.
- **No org, no profiles.** In a repo without an org, profile writes are refused; create or join an org first ([Step 9](#step-9--create-or-rename-an-org)).
- **A project is found only where it is known.** `project member add northwind-app …` from inside another client repo is `E_NOT_FOUND`; run it in that project's repo, or name the project by its `prj_` id.
- **An offboarded person cannot register themselves back.** `lore actor register` and `lore agent start` from their own clone are `E_STATUS_CHANGE` until an admin runs `lore actor rejoin`.
- **An offboarded person is not a valid `--assign`.** `--assign @mira` answers `E_NOT_FOUND` until she rejoins; the old rows keep her name.

## Related

- [Identity and stores](identity-and-stores.md): registering on the roster, `lore org import`, identity per org.
- [Team invites](invites.md): inviting someone with roles and projects that become memberships when they join.
- [Guided setup](guided-setup.md) and [Setups from the command line](setup-cli.md).
- [Commands for teams](commands.md) and [Errors](../reference/errors.md#team-profiles) (`E_CONTACT_TAKEN`, `E_CONTACT_TYPE`, `E_PERIOD_OVERLAP`, `E_ROLE_UNKNOWN`, `E_NAME_TAKEN`, `E_STATUS_CHANGE`).
- [Client repos and leaks](use-cases/leak-cleanup.md): what a client repo is allowed to hold.
