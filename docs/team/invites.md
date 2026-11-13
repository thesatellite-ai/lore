# Team invites

**Your own staff are not invited:** add them with [`lore member add`](members.md) and they join with `lore setup join --invite "<the org's store url>"` (the command `member add` prints). An invite is for an outsider who needs a one-time line, such as a contractor joining with a personal email.

**In short:** an admin runs `lore team invite --email <email>`; lore writes an invite row in the team's store and prints one line to send to the person. They paste it into `lore setup join`, and lore puts them on the roster with the roles and projects the invite names. An invite works once and expires after 14 days.

You want this page when you run a team (an `admin` setup) and someone new should join it: the invite tells their lore where the team's store is, and their stint, roles and projects are set up when they accept. Every output below is a real run (`tests/team/docs/run-examples.sh invites`) against a fake GitHub; invite lines are cut after 24 characters in the examples, as they hold a random id.

## Before you start

- An **admin setup** for the org is active (see [Guided setup](guided-setup.md)): every `lore team invite` command refuses with `E_REFUSED` when no setup is active or the active one is a `solo` or `member` setup. Run with `--setup <an admin setup>` from anywhere.
- The org's store holds its roster (the `org` folder by default).
- `gh`, logged in, only if you want lore to check or grant GitHub access.

## Step 1 — Invite someone

Tomas invites Mira as a developer and reviewer on `acme-hub`, and Ana as a contractor with a 30-day invite:

<!-- example: invites#invite -->
```console
# tomas, in acme-hub
$ lore team invite --email mira@agency.example --role dev,reviewer --project acme-hub
lore: pushed store agency/org: commit 601d285 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/invites/inv_7a67…9504.json (store agency/org)
✓ invite inv_7a67…9504 written to store agency (expires 2026-11-16)
Send this to mira@agency.example (it holds no secret):
  lore setup join lore-invite:v1:eyJ2IjoxLCJpZCI6Imludl8w…
$ lore team invite --email ana@agency.example --type contractor --expires 30d
lore: pushed store agency/org: commit 86dbae4 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/invites/inv_2ad3…f891.json (store agency/org)
✓ invite inv_2ad3…f891 written to store agency (expires 2026-12-02)
Send this to ana@agency.example (it holds no secret):
  lore setup join lore-invite:v1:eyJ2IjoxLCJpZCI6Imludl8w…
```
<!-- /example -->

The line after "Send this to" is what the person pastes. With `--github <user>`, lore also checks (through `gh`) whether that GitHub user can push to the store and, when not, prints the command that grants it, for example `gh api -X PUT repos/agency/notes/collaborators/mira-dev -f permission=push` (see [GitHub access](#github-access-and---grant)).

| Flag | Meaning |
|---|---|
| `--github <user>` | the GitHub user you expect to accept; lore checks whether they can push to the store |
| `--role <r>` | a role name, applied on each `--project` (repeat it, or comma-separate) |
| `--project <p>` | a project to join, by name or `prj_` id (repeat it); needs at least one `--role` |
| `--team @t` | a team to join on accept (repeat it); the team must exist and be active |
| `--type <t>` | `employee` (default), `contractor`, `intern` or `client_contact`: the kind of stint opened on join |
| `--expires <n>d` | lifetime in days, 1 to 90; default `14d` |
| `--grant` | also give `--github` push access to the store on GitHub with `gh` (asks first) |
| `--yes` | answer the confirmation without a terminal |

### What is refused

A second invite for an email that still has a pending one is refused (resend or revoke the first); `--role` without `--project` (and the other way round) is `E_INVALID_INPUT`, because a role is always held on a project; and an invite from a setup that is not an `admin` setup of the org is `E_REFUSED`:

<!-- example: invites#invite-refusals -->
```console
# tomas, in acme-hub
$ lore team invite --email mira@agency.example
ERROR: [E_INVALID_INPUT] invite inv_7a67…9504 for mira@agency.example is still pending (until 2026-11-16); one invite per person
  hint: lore team invite resend mira@agency.example prints it again; lore team invite revoke mira@agency.example ends it
(exit 20)
$ lore team invite --email bo@agency.example --role dev
ERROR: [E_INVALID_INPUT] --role is applied on each --project, and no --project is given
  hint: --project <project>
(exit 20)
$ lore --setup personal team invite --email bo@agency.example
ERROR: [E_REFUSED] setup personal is a solo setup; team invites are written from an admin setup of the org
  hint: run with --setup <an admin setup>, or ask the org admin to invite
(exit 23)
```
<!-- /example -->

### GitHub access and `--grant`

The invite line grants nothing: whether the person can read and write the team's store is decided by GitHub. When the store is on github.com and you give `--github`, lore checks access with `gh` and, when the person has none yet (or it could not check), prints the exact command that grants it. With `--grant`, lore asks first, then runs that command (`gh api -X PUT repos/<owner>/<repo>/collaborators/<user> -f permission=push`) for you; it needs `--github` and a store on github.com. Nothing is written before you confirm.

## Step 2 — Send the invite line

```text
lore-invite:v1:<base64url of a small JSON object>
```

Send it in chat or email; the person runs `lore setup join --invite <line>` (see [Step 4](#step-4--what-happens-when-they-join)). The JSON names the invite id, the org, the store url, the org folder, the invited email, who invited them and the last day. It holds no secret, so it is safe in chat or email. Everything that matters (roles, projects, kind of stint, status) is read from the invite row inside the store, so changing the line cannot give anyone more.

## Single use, 14 days

- An invite is **accepted once**. After that, the same line answers `E_INVITE_USED` for anyone else; running the join again as the same person is harmless.
- It **expires** after its last day (14 days by default, `--expires` up to 90). An expired invite answers `E_INVITE_EXPIRED`; send a new one.
- The **email must match**: the person joining must give the invite's email (as their main address or one of their other addresses), or the join is refused with `E_INVITE_EMAIL`.
- A **revoked** invite answers `E_INVITE_REVOKED`.

## Step 3 — List, resend and revoke

<!-- example: invites#list -->
```console
# tomas, in acme-hub
$ lore team invite list
invites:
  ID                                    EMAIL                GITHUB  ROLES          PROJECTS                              TEAMS  PERIOD_TYPE  STATUS   EXPIRES_ON  ACCEPTED_BY  CREATED_BY                            UPDATED_BY
  inv_7a67…9504  mira@agency.example          dev, reviewer  prj_162d…9666         employee     pending  2026-11-16               act_54fe…28f2  act_54fe…28f2
  inv_2ad3…f891  ana@agency.example                                                                       contractor   pending  2026-12-02               act_54fe…28f2  act_54fe…28f2
$ lore team invite list --status pending --json
{
  "invites": [
    {
      "id": "inv_7a67…9504",
      "email": "mira@agency.example",
      "roles": [
        "dev",
        "reviewer"
      ],
      "projects": [
        "prj_162d…9666"
      ],
      "period_type": "employee",
      "status": "pending",
      "expires_on": "2026-11-16",
      "created_by": "act_54fe…28f2",
      "updated_by": "act_54fe…28f2"
    },
    {
      "id": "inv_2ad3…f891",
      "email": "ana@agency.example",
      "period_type": "contractor",
      "status": "pending",
      "expires_on": "2026-12-02",
      "created_by": "act_54fe…28f2",
      "updated_by": "act_54fe…28f2"
    }
  ]
}
```
<!-- /example -->

`--status pending|accepted|revoked|expired` filters the list; a pending invite past its last day shows as `expired`. `--json` prints the same rows as `{"invites":[…]}`, as above; projects are shown by `prj_` id.

`resend` prints the same line again and changes nothing. It takes an `inv_` id or the invited email (with an email, the pending invite is picked). A used, revoked or expired invite answers its `E_INVITE_*` code, with a hint to write a new one.

<!-- example: invites#resend-and-revoke -->
```console
# tomas, in acme-hub
$ lore team invite resend --ref mira@agency.example
id: inv_7a67…9504
text: lore-invite:v1:eyJ2IjoxLCJpZCI6Imludl8w…
$ lore team invite revoke --ref ana@agency.example
ERROR: [E_NEEDS_CONFIRM] Revoke invite inv_2ad3…f891 for ana@agency.example?
  hint: nothing written; confirm with --yes (or "yes": true over lore api)
(exit 26)
$ lore team invite revoke --ref ana@agency.example --yes
lore: pushed store agency/org: commit 9b40ce7 to branch main
lore: wrote ~/.lore/stores/agency/notes/org/invites/inv_2ad3…f891.json (store agency/org)
id: inv_2ad3…f891
status: revoked
$ lore team invite resend --ref ana@agency.example
ERROR: [E_INVITE_REVOKED] invite inv_2ad3…f891 was revoked
  hint: lore team invite ana@agency.example
(exit 23)
$ lore team invite list
invites:
  ID                                    EMAIL                GITHUB  ROLES          PROJECTS                              TEAMS  PERIOD_TYPE  STATUS   EXPIRES_ON  ACCEPTED_BY  CREATED_BY                            UPDATED_BY
  inv_7a67…9504  mira@agency.example          dev, reviewer  prj_162d…9666         employee     pending  2026-11-16               act_54fe…28f2  act_54fe…28f2
  inv_2ad3…f891  ana@agency.example                                                                       contractor   revoked  2026-12-02               act_54fe…28f2  act_54fe…28f2
```
<!-- /example -->

`revoke` marks a pending invite revoked, after a confirmation (`E_NEEDS_CONFIRM` without `--yes` and without a terminal). Revoking an already revoked invite changes nothing. An accepted invite cannot be revoked: the person is on the team, so use `lore actor offboard` instead.

## Step 4 — What happens when they join

When Mira runs `lore setup join --invite <line>` and finishes the join step (see [Guided setup](guided-setup.md#step-6--join-a-team-from-an-invite)), lore, in her name:

1. imports the org entry from the store (unless her machine already has one for that store; an entry of the same name for another store is refused with `E_SETUP_ORG_MISMATCH`);
2. records her email addresses and GitHub user as identity contacts on the roster;
3. opens a stint of the invite's type, starting today;
4. adds a membership with the invite's roles on each of its projects;
5. adds her to each team the invite names (`--team`, or `lore team member add --handle @web --members mira@agency.example` while the invite was pending); a team removed since is skipped;
6. marks the invite `accepted` by her;
7. writes her `member` setup to `~/.lore/config.yaml`.

Someone who left the team and is invited again keeps their old roster entry and gets a new stint (see [Team profiles](profiles.md)).

## Try it

After Step 1 and Step 3, `lore team invite list` shows one invite still pending and one revoked; that list is the whole state of the org's invites:

<!-- example: invites#resend-and-revoke only=5 -->
```console
# tomas, in acme-hub
$ lore team invite list
invites:
  ID                                    EMAIL                GITHUB  ROLES          PROJECTS                              TEAMS  PERIOD_TYPE  STATUS   EXPIRES_ON  ACCEPTED_BY  CREATED_BY                            UPDATED_BY
  inv_7a67…9504  mira@agency.example          dev, reviewer  prj_162d…9666         employee     pending  2026-11-16               act_54fe…28f2  act_54fe…28f2
  inv_2ad3…f891  ana@agency.example                                                                       contractor   revoked  2026-12-02               act_54fe…28f2  act_54fe…28f2
```
<!-- /example -->

Run the same from a folder covered by your own admin setup, with `person@example.com` as the email, then revoke it with `--yes`.

## Pitfalls

- **Invites are written from an admin setup.** If `lore team invite` says no setup is active, add `--setup <your admin setup>`.
- **The line is not access.** If the person cannot read the store, the join stops at the `store.read` check; grant access on GitHub (or with `--grant`) and they run the join again.
- **Use the email the person will join with.** Their join is refused if neither their main address nor one of their other addresses is the invite's.

## Related

- [Guided setup](guided-setup.md): the join step on the setup page.
- [Setups from the command line](setup-cli.md): `lore setup join` and the admin setup invites are written from.
- [Team profiles](profiles.md): the stint, roles and memberships an accepted invite creates.
- [Setup and invite errors](setup-errors.md): every `E_INVITE_*` code with its fix.
