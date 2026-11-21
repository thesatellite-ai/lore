# Teams

**In short:** a team is an `@handle` on the org's roster that names a group of people. `lore team add --handle @web --members @tomas,@mira` makes one; from then on `@web` works wherever a person's handle does, such as `lore task add --title … --assign @web`. The team commands run inside a repo of the org, in a clone with no lore project yet (a read there reads the org store; nothing is created in the clone), or outside any repo (the active setup's org store). From a folder no org applies to (the home folder, a folder outside every setup), name the org: `lore team list --store <org>`, `lore team show --handle @web --store <org>`; every read of the roster takes `--store <org>` (`member list`, `member show`, `team invite list`, `actor list` …). With no flag there, the read is refused with `E_NOT_A_STORE`, and the hint names `--store <org>` and `--setup <setup>` with the orgs and setups this machine has.

You want this page when you assign work to a group rather than a person, or keep track of who is on which team. Every output below is a real run (`tests/team/docs/run-examples.sh teams`).

## Before you start

- The org's store holds its roster (the `org` folder by default), and the people you add are on it: an admin added them with [`lore member add`](members.md) (which can put them on teams at once), they ran `lore actor register` in a repo of the org, or they joined from an [invite](invites.md). `lore actor list` shows them; `lore actor list --with teams` adds the teams each person is on, and `lore actor show` names them on a `teams:` line.
- In an org store only an admin of the org changes the teams (`team add`, `team rename`, `team remove`, `team member add`, `team member remove`, and the team contact commands): who is on `@web` decides who adds rules and notes in `<org>/web` (see [Members](members.md)), so a member who could put themselves on a team could add anywhere. A member who runs one is refused with `E_REFUSED` "only an admin of the org can change the team @web …", naming the admins and the exact line an admin runs (`lore team member add --handle @web --members @mo`), with nothing written; every member still reads the teams (`team list`, `team show`). A person taking up their own invite joins the teams it names. While the roster names no admin (an org from before org roles) anyone on it may change the teams. A contractor (a person whose open stint is a `contractor` one) writes only to their projects' folders, so their team commands (and company rules, `--to <org>/org`) are refused with one `E_REFUSED` line naming the stint. A coding agent runs the team commands as its person, like every other roster command (`agents.may_manage_people`, see [Agents](agents.md)).

## Step 1 — Make a team

Tomas makes `@web` from his plain `work` folder (no repo there), lists the teams, and looks at one from a client repo:

<!-- example: teams#add -->
```console
# tomas, in ~
$ lore team add --handle @web --name 'Web' --members @tomas,@mira
lore: committed locally to store agency/org; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/org/teams/tem_7a67…9504.json (store agency/org)
✓ tem_7a67…9504 @web (2 member(s)) added on the roster of agency (agency/org)
$ lore team list
@web  Web  2 member(s)
1 team

# tomas, in acme-hub
$ lore team show --handle @web
tem_7a67…9504  @web  Web
roster: agency (agency/org)
members (2):
  act_2ad3…f891  @tomas  Tomas Weber
  act_162d…9666  @mira  Mira Patel
```
<!-- /example -->

`--members` takes `@handle`s, `act_` ids or emails, comma-separated or repeated. A handle is one lowercase word (letters, digits, `_` and `-`). Accents are dropped, never the rest of the word: `lore member add --handle chloé` stores `@chloe`, and `--assign @chloé` or `@Chloe` finds the same person. A handle that is not on the roster but close to one (a roster written by an older lore holds `@chlo` for chloé) is answered with that handle and the `lore member edit --who @chlo --handle chloe` that renames it; `member add` for someone already on the roster with another `--handle` says the handle is unchanged and names the same command.

## Step 2 — Assign work to the team

`@web` resolves to the team on every command that takes a person's handle:

<!-- example: teams#assign -->
```console
# tomas, in acme-hub
$ lore task add --title 'ship the checkout page' --to agency --assign @web
lore: committed locally to store agency/acme-hub; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/acme-hub/task_lists/tlt_54fe…28f2.json (store agency/acme-hub)
lore: wrote ~/.lore/stores/agency/notes/acme-hub/tasks/tsk_05e9…4603.json (store agency/acme-hub)
lore: wrote ~/.lore/stores/agency/notes/acme-hub/_meta.json (store agency/acme-hub)
✓ tsk_05e9…4603 — ship the checkout page [medium] → [agency/acme-hub]
  created tasklist tlt_54fe…28f2 "default" for tasks added without --tasklist
```
<!-- /example -->

Every member of `@web` then finds the task in `lore task list --mine` and in `lore inbox` under assignments, marked `(team @web)`, until someone claims it. `lore task show --id <task>` prints a `team:     @web` line, and its `--json` carries `assigned_team` (the `tem_` id) and `assigned_team_handle`.

A team's name is internal to the org, so a task written into a client's own repo cannot be assigned to a team; write it to the org's store (`--to agency`) as above.

## Step 3 — Change who is on it

`team member add` and `team member remove` change the members and say who was already in (or not in); `team add` on an existing team changes its display name and keeps its members unless `--members` is given, which replaces them. Someone invited who has not joined yet is named by email: `lore team member add --handle @web --members mira@agency.example` records the team on her pending invite and says so, and she joins it when she accepts (`lore team invite --email <email> --team @web` does the same up front). `team list` counts them as pending, and `team show` lists them apart from the members, with their invite and its last day:

<!-- example: teams#members -->
```console
# tomas, in ~
$ lore team member remove --handle @web --members @mira
lore: committed locally to store agency/org; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/org/teams/tem_7a67…9504.json (store agency/org)
✓ @web: 1 member(s) now; @mira taken off the team
$ lore team member add --handle @web --members @mira,@tomas
lore: committed locally to store agency/org; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/org/teams/tem_7a67…9504.json (store agency/org)
✓ @web: 2 member(s) now; @mira added to the team; @tomas already a member
$ lore team add --handle @web --name 'Web platform'
lore: committed locally to store agency/org; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/org/teams/tem_7a67…9504.json (store agency/org)
✓ tem_7a67…9504 @web (2 member(s)) updated on the roster of agency (agency/org)
$ lore team list --json
{
  "teams": [
    {
      "id": "tem_7a67…9504",
      "handle": "web",
      "display_name": "Web platform",
      "active": true,
      "members": [
        {
          "id": "act_2ad3…f891",
          "handle": "tomas",
          "display_name": "Tomas Weber"
        },
        {
          "id": "act_162d…9666",
          "handle": "mira",
          "display_name": "Mira Patel"
        }
      ],
      "created_by": "act_2ad3…f891",
      "updated_by": "act_2ad3…f891"
    }
  ],
  "where": "agency (agency/org)"
}
```
<!-- /example -->

To change the handle itself, `lore team rename --handle @web --to @frontend` keeps the team (same `tem_` id and members): tasks assigned to it show `@frontend` from then on, and a pending invite that named `@web` names `@frontend`. The new handle may not be a person's or another team's, removed ones included. The old handle names nobody afterwards: `--assign @web` is refused, and the hint lists the teams there are now, so the new handle is in it.

## Step 3b — Give the team a contact

A team keeps contacts the way a person does: its chat channel, its mailing list, its page. `lore team contact add --handle @web --type slack --value '#web-team' --label announcements` adds one; the type is checked like a person's contact (`lore contact-type list`: `slack`, `discord`, `email`, `website`, … and any type the org added), and an email or URL is normalised the same way. The first contact of a type is its primary; `--primary` makes another one primary. `lore team show --handle @web` prints the contacts under `contacts (n):` with their `tcn_` ids, `lore team list` adds the primary of each type to the team's line (`@web  Web  2 member(s)  slack #web-team`), and both carry them in `--json` as `contacts` (`type`, `value`, `value_norm`, `label`, `primary`, `url`). `lore team contact list` lists every team's contacts (`--handle @web` for one team, `--type slack` for one type, `--store <org>` from a folder no org applies to); `lore team contact edit --id <tcn_id>` changes `--value`, `--label`, `--note` or `--primary` / `--no-primary`, and `lore team contact remove --id <tcn_id>` moves one to the trash. A team contact is never an identity: no git email or login resolves to a team. Like the team itself, only an admin of the org changes its contacts, and a removed team takes none until `team add` brings it back.

## What lore refuses

A team cannot take a person's handle (an `@mention` would mean two things), only people on the roster can join (someone who left must be brought back with `lore actor rejoin` first), and a team that does not exist is named with the command that makes it:

<!-- example: teams#refusals -->
```console
# tomas, in ~
$ lore team add --handle @mira
ERROR: [E_INVALID_INPUT] handle @mira is taken by Mira Patel on the roster of agency (agency/org); a team and a person cannot share one
  hint: choose another team handle (lore team add --handle @mira-team …)
(exit 20)
$ lore team add --handle @ops --members @nobody
ERROR: [E_NOT_FOUND] no one is @nobody on the roster of agency (agency/org)
  hint: check the handle with lore actor list; a new person runs lore actor register (or joins from an invite) first
(exit 27)
$ lore team show --handle @ops
ERROR: [E_NOT_FOUND] no team @ops on your copy of the roster of agency (agency/org)
  hint: a teammate may have made it since your last sync: lore sync fetches the roster, then run this again; lore team list shows the teams you have
(exit 27)
```
<!-- /example -->

## Step 4 — Remove a team

`team remove` takes the team off the roster but keeps its row, so tasks that named `@web` still read it; `team list --all` shows removed teams, and `team add` brings one back with its members. Once the removal has reached a clone (`lore sync`), `--assign @web` there is refused with `team @web was removed from the roster`, naming `lore team list` and any active team with the same members:

<!-- example: teams#remove -->
```console
# tomas, in ~
$ lore team remove --handle @web
lore: committed locally to store agency/org; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/org/teams/tem_7a67…9504.json (store agency/org)
✓ tem_7a67…9504 @web removed from the roster of agency (agency/org) (kept for history: tasks that named it still read; lore team add --handle @web brings it back)
$ lore team list --all
@web  Web platform  2 member(s)  [removed]
1 team
$ lore team add --handle @web
lore: committed locally to store agency/org; lore sync sends it
lore: wrote ~/.lore/stores/agency/notes/org/teams/tem_7a67…9504.json (store agency/org)
✓ tem_7a67…9504 @web (2 member(s)) updated on the roster of agency (agency/org)
```
<!-- /example -->

## See also

- [Team invites](invites.md): bringing someone new onto the roster.
- [Team profiles](profiles.md): people's profiles, contacts and project roles.
- [Commands reference](commands.md).
