# Adding your staff

**In short:** your own people are added straight to the org's roster with `lore member add <email>` (or many at once with `lore member add --from staff.csv`), with their org role, teams and projects. Nothing is pending and there is no line to send: on their laptop they run `lore setup join <the org's store url>` (the exact command `lore member add` prints), and lore finds them on the roster by their email. Use an [invite](invites.md) only for an outsider, such as a contractor joining with a personal email.

## Before you start

- An **admin setup** for the org is active (see [Guided setup](guided-setup.md)); run with `--setup <an admin setup>` from anywhere. The member commands run outside any repo too, and their reads (`lore member show`, `lore team show`) work in a clone with no lore project yet.
- The teams you name exist (`lore team add @web`, see [Teams](teams.md)).

## Step 1 — Add people

```sh
lore member add mira@agency.example --name 'Mira Dev' --handle mira --github mira-dev --role member --team @web --yes
lore member add --from staff.csv --dry-run   # who would be added
lore member add --from staff.csv --yes       # all of them, one store commit
```

| Flag | Meaning |
|---|---|
| `--name`, `--handle`, `--github` | their display name (default: the email's name part), roster @handle and GitHub login |
| `--role <r>` | an **org-level** role (`member`, `pm`, `admin`, …): no project needed; it is the role on their stint (`lore member edit <who> --role pm` changes it) |
| `--type <t>` | `employee` (default), `contractor`, `intern` or `client_contact` |
| `--team @t` | a team to put them on (repeat it) |
| `--project <p>` with `--project-role <r>` | a project membership with its project roles (repeat both) |
| `--from <file>` | many people from a `.csv` (header `email,name,handle,github,role,type,teams,projects,project_roles`; list cells separated by `;`) or a `.json` list |
| `--from-invites` | every pending invite becomes a member, keeping its teams, projects and roles; the invite is marked accepted |
| `--dry-run` | list who would be added; write nothing |
| `--yes` | answer the confirmation without a terminal |

Every row of one run (the people, their email and GitHub contacts, stints, memberships, and each team once) is written as **one store commit** and one push, so a list of a hundred people takes seconds. One bad line (an address that is not one, a team that does not exist, an email listed twice) refuses the whole file before anything is written, naming the line. A second run adds nobody: someone who is already a member is listed as such. The last line of the output is the command each person runs, with the org's store url filled in:

```text
✓ added mira@agency.example (act_…) Mira Dev, role member, teams @web
  4 roster rows in agency org/, one store commit; each person joins on their machine with: lore setup join git@github.com:agency/agency-team.git (matched by their email, no invite)
```

## Step 2 — They join

On their own machine, each person runs the command `lore member add` printed: `lore setup join <the org's store url>`. A new member's machine has no entry for the org yet, so the org's name alone is refused there with `E_JOIN_ORG_UNKNOWN` ("no org named … on this machine yet; join with the org's store url the admin gave you"); the name works only on a machine that already has the org. lore takes the email of their identity step (their `git config user.email`), finds them on the roster, links this machine to their roster entry and sets up the org's stores. Their role, teams and projects are already there.

Someone not on the roster is refused with the reason and the command to ask for: `<email> is not a member of <org>; ask an admin to run: lore member add <email>`. `lore setup check <setup> --step join` (and the setup page, which checks first) asks the roster the same way, so its `invite.found` check fails with that same message before anything is applied.

## Step 3 — See and change people

- `lore member list [--all]`: everyone on the roster with email, name, org role, @handle and teams (`--json`).
- `lore member show <email|@handle|act_…>`: one person.
- `lore member edit <who> [--name] [--handle] [--role]`.
- `lore member remove <who> [--reason] --yes`: they leave the org (`lore actor offboard`: their stint and memberships end, they are taken off every team, the row stays for history; all of it is one store commit and one push, so a teammate never fetches half a removal). From then on their own lore refuses writes to the org's stores and repos, from any folder (a repo, or inside the store's clone; a hand edit in the clone is held, not committed) (`E_REFUSED` "you are marked as left in <org> since <day>", after their next `lore sync`); their personal store keeps working.
- `lore member add <email>` for someone who left brings them back: active again, with their roster name; a stint that ended today is reopened, so a removal made by mistake is undone at once (`lore actor rejoin <who> --type <t>` does the same on the same day). After their next `lore sync`, their writes go through again and their `lore doctor` shows the open stint at once. Teams and projects are not given back on their own: add `--team` / `--project` to the same command.
- `lore team member add @web <email|@handle>` works for every member at once.

## Pending invites

Invites written before `member add` existed still work. `lore doctor` lists the org's pending invites with one hint: `lore member add --from-invites` turns them into members now (an outsider can keep the invite).

## Who wrote it

Rows are written by the person running the command. When a coding agent runs `lore member add` for its person, the rows are the person's (created_by is the person) and the agent's session is on the audit trail, as for every other roster write; an agent needs the org's `agents.may_manage_people` policy to change people.

## Related

- [Team invites](invites.md): a one-time line for an outsider.
- [Teams](teams.md): `@web` and its members.
- [Team profiles](profiles.md): stints, roles and project memberships.
