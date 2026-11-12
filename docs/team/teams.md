# Teams

**In short:** a team is an `@handle` on the org's roster that names a group of people. `lore team add @web --members @tomas,@mira` makes one; from then on `@web` works wherever a person's handle does, such as `lore task add … --assign @web`. The team commands run inside a repo of the org or outside any repo (the active setup's org store).

You want this page when you assign work to a group rather than a person, or keep track of who is on which team. Every output below is a real run (`tests/team/docs/run-examples.sh teams`).

## Before you start

- The org's store holds its roster (the `org` folder by default), and the people you add are on it: they ran `lore actor register` in a repo of the org, or joined from an [invite](invites.md). `lore actor list` shows them.
- Anyone on the roster may change the teams. A coding agent runs the team commands as its person, like every other roster command (`agents.may_manage_people`, see [Agents](agents.md)).

## Step 1 — Make a team

Tomas makes `@web` from his plain `work` folder (no repo there), lists the teams, and looks at one from a client repo:

<!-- example: teams#add -->
```console
# tomas, in ~
$ lore team add @web --name 'Web' --members @tomas,@mira
✓ tem_7a67…9504 @web (2 member(s)) added in agency org/
$ lore team list
@web              Web                      2 member(s)

# tomas, in acme-hub
$ lore team show @web
tem_7a67…9504  @web  Web
roster: agency org/
members (2):
  act_2ad3…f891  @tomas  Tomas Weber
  act_162d…9666  @mira  Mira Patel
```
<!-- /example -->

`--members` takes `@handle`s, `act_` ids or emails, comma-separated or repeated. A handle is one lowercase word (letters, digits, `_` and `-`).

## Step 2 — Assign work to the team

`@web` resolves to the team on every command that takes a person's handle:

<!-- example: teams#assign -->
```console
# tomas, in acme-hub
$ lore task add 'ship the checkout page' --to agency --assign @web
✓ tsk_54fe…28f2 — ship the checkout page [medium] → [agency/acme-hub]
```
<!-- /example -->

A team's name is internal to the org, so a task written into a client's own repo cannot be assigned to a team; write it to the org's store (`--to agency`) as above.

## Step 3 — Change who is on it

`team member add` and `team member remove` change the members and say who was already in (or not in); `team add` on an existing team renames it and keeps its members unless `--members` is given, which replaces them. Someone invited who has not joined yet is named by email: `lore team member add @web mira@agency.example` records the team on her pending invite and says so, and she joins it when she accepts (`lore team invite <email> --team @web` does the same up front). `team list` counts them as pending, and `team show` lists them apart from the members, with their invite and its last day:

<!-- example: teams#members -->
```console
# tomas, in ~
$ lore team member remove @web @mira
✓ @web: 1 member(s) now; @mira taken off the team
$ lore team member add @web @mira,@tomas
✓ @web: 2 member(s) now; @mira added to the team; @tomas already a member
$ lore team add @web --name 'Web platform'
✓ tem_7a67…9504 @web (2 member(s)) updated in agency org/
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
  "where": "agency org/"
}
```
<!-- /example -->

## What lore refuses

A team cannot take a person's handle (an `@mention` would mean two things), only people on the roster can join (someone who left must be brought back with `lore actor rejoin` first), and a team that does not exist is named with the command that makes it:

<!-- example: teams#refusals -->
```console
# tomas, in ~
$ lore team add @mira
ERROR: [E_INVALID_INPUT] handle @mira is taken by Mira Patel on the roster in agency org/; a team and a person cannot share one
  hint: choose another team handle (lore team add @mira-team …)
(exit 20)
$ lore team add @ops --members @nobody
ERROR: [E_NOT_FOUND] no one is @nobody on the roster in agency org/
  hint: check the handle with lore actor list; a new person runs lore actor register (or joins from an invite) first
(exit 27)
$ lore team show @ops
ERROR: [E_NOT_FOUND] no team @ops on the roster in agency org/
  hint: lore team list shows the teams; lore team add @ops --members <people> makes it
(exit 27)
```
<!-- /example -->

## Step 4 — Remove a team

`team remove` takes the team off the roster but keeps its row, so tasks that named `@web` still read it; `team list --all` shows removed teams, and `team add` brings one back with its members:

<!-- example: teams#remove -->
```console
# tomas, in ~
$ lore team remove @web
✓ tem_7a67…9504 @web removed from the roster in agency org/ (kept for history: tasks that named it still read; lore team add @web brings it back)
$ lore team list --all
@web              Web platform             2 member(s)  [removed]
$ lore team add @web
✓ tem_7a67…9504 @web (2 member(s)) updated in agency org/
```
<!-- /example -->

## See also

- [Team invites](invites.md): bringing someone new onto the roster.
- [Team profiles](profiles.md): people's profiles, contacts and project roles.
- [Commands reference](commands.md).
