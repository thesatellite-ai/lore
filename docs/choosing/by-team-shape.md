# Which options for which kind of team

**In short:** find the row that looks like your team, take its settings, and change nothing else. A person alone in their own repos needs no config at all. Each step up (teammates, a client, a contractor, agents, CI) adds one or two settings, and every setting on this page can be added later without moving a single note.

## The settings per team shape

| Team shape | Where notes live | Org entry | Live rows | Agents and gates | Version pin | Start with |
|---|---|---|---|---|---|---|
| **One person, own repos** | the repo (`lore init`); a personal store for notes about you | none | defaults (tasks live; with no remote they stay on your machine) | not needed | optional | [Solo developer](../team/use-cases/solo.md) |
| **Small team, one repo** | the repo | optional; only if you want `@handles` and a roster | defaults: tasks reach teammates at once on any branch | not needed | yes, `lore_version_pin` in `.lore/data/_meta.json` | [Small team in one repo](../team/use-cases/small-team.md) |
| **One repo, several apps (monorepo)** | the repo; `lore init` in each app folder that wants its own notes | as for the team | defaults | as for the team | yes | [Projects and repos](../sync/multi-repo-projects.md) |
| **One product over several repos** | one project in a git store, linked from each repo with `lore org link` (alone: a folder store is enough) | one, with the store | defaults | tasks carry `--repo` | yes | [Several repos as one product](../team/use-cases/group-project.md) |
| **Agency with client repos** | the client's repo for what the client may read; everything else in the agency's git store | one, with `repos:` patterns or links for the client repos | defaults, but write private notes `--to <org>`: a live row in a client repo is readable by the client | as for the team | yes | [Agency with a client repo](../team/use-cases/agency-client-repo.md) |
| **Agency with a contractor** | as the agency, plus one store per client the contractor may see | the contractor imports the agency's entry with `lore org import` | defaults | as for the team | yes | [Contractor limited to one client](../team/use-cases/contractor.md) |
| **One person, two companies** | each company's store; a personal store for yourself | one per company, each with its own `identity` | defaults | as each company does | each repo's pin | [One person in two companies](../team/use-cases/two-companies.md) |
| **Several agents on one goal** | as the team; claims live on the repo's (or the linked store's) hidden signals ref | one, so agents are on the roster | defaults; claims and gates need the remote | `lore agent start`, `lore task next --mission`, gates on risky steps, holds for heavy jobs | yes | [Agents on one mission](../team/use-cases/agents-mission.md) |
| **CI** | reads the repo's committed notes | none | none: `LORE_READ_ONLY=1` | none | `LORE_VERSION_PIN` or the committed pin | [Use lore in CI](../operations/ci.md) |

## If this, then that

- **If you are not sure which row you are,** start with the one-person row. Adding a store, an org entry or a pin later never rewrites the notes you already have.
- **If a client can read the repo,** treat it as the agency row even when you work alone: your notes about the client go to a store the client cannot open.
- **If people join and leave,** keep the org's roster in its store and bring people in with an invite (`lore team invite <email>`, then `lore setup join` on their side), and take their GitHub access away when they leave.
- **If each person uses several setups on one machine** (own projects and a team's), make one setup each with `lore setup plan`, `check` and `apply` ([Setups from the command line](../team/setup-cli.md)); lore picks the setup from the folder you are in. The prebuilt binaries add a browser page for the same steps.
- **If two people may run different lore versions,** pin one version in `.lore/data/_meta.json`; `lore upgrade` then installs that version and `lore doctor` says when someone is on another.
- **If agents run unattended,** give each its own label with `lore agent start`, put a gate on every step a person must approve, and stop an agent when `lore task next` exits with status 10 (nothing ready) instead of letting it loop.
- **If CI only checks the notes,** set `LORE_READ_ONLY=1` and act on the exit code of `lore doctor`. If CI must merge pull requests whose notes conflict on GitHub, add the action with `lore sync install-action`.

## Pitfalls

- **GitHub access is the only wall.** An org entry, a store list or a setup decides what lore loads on one machine; who can open the store repo decides who can read it.
- **Hidden is not private.** Live rows and claims ride hidden refs of the repo's remote; anyone who can clone the repo can fetch them.
- **A pin below the project's `min_lore_version` leaves everyone read-only;** `lore doctor` reports it as broken.
- **Read-only CI still builds a cache.** The first command in a fresh clone builds `.lore/lore-cache.db` from the files; nothing in `.lore/data` changes.

## Guides and examples for each team shape

| Team shape | Guide | Example |
|---|---|---|
| One person | [Solo developer](../team/use-cases/solo.md), [Personal notes](../team/use-cases/personal-notes.md) | [`solo`](../../tests/team/docs/transcripts/solo.txt), [`personal`](../../tests/team/docs/transcripts/personal.txt) |
| Small team | [Small team in one repo](../team/use-cases/small-team.md), [Getting started](../team/getting-started.md) | [`small-team`](../../tests/team/docs/transcripts/small-team.txt), [`getting-started`](../../tests/team/docs/transcripts/getting-started.txt) |
| Monorepo | [Projects and repos](../sync/multi-repo-projects.md) | [`projects`](../../tests/docs/sections/projects.sh) ([output](../../tests/docs/transcripts/projects.txt)) |
| Several repos | [Several repos as one product](../team/use-cases/group-project.md), [One store for several repos](../sync/shared-database-mode-b.md) | [`group`](../../tests/team/docs/transcripts/group.txt), [`projects`](../../tests/docs/sections/projects.sh) |
| Agency | [Agency with a client repo](../team/use-cases/agency-client-repo.md), [Give one note to the client](../team/use-cases/sharing-a-note.md), [Clean up a leak](../team/use-cases/leak-cleanup.md) | [`agency`](../../tests/team/docs/transcripts/agency.txt), [`leak-cleanup`](../../tests/team/docs/transcripts/leak-cleanup.txt) |
| Contractor | [Contractor limited to one client](../team/use-cases/contractor.md) | [`contractor`](../../tests/team/docs/transcripts/contractor.txt) |
| Two companies | [One person in two companies](../team/use-cases/two-companies.md) | [`two-orgs`](../../tests/team/docs/transcripts/two-orgs.txt) |
| Agents | [Agents](../team/agents.md), [Agents on one mission](../team/use-cases/agents-mission.md) | [`agents`](../../tests/team/docs/transcripts/agents.txt) |
| CI | [Use lore in CI](../operations/ci.md), [Upgrade and version pins](../operations/upgrade.md), [GitHub workflow](../sync/github-workflow.md) | [`ops`](../../tests/docs/sections/ops.sh) ([output](../../tests/docs/transcripts/ops.txt)) |
| People joining and setups | [Team invites](../team/invites.md), [Setups from the command line](../team/setup-cli.md) | [`invites`](../../tests/team/docs/transcripts/invites.txt), [`setup-cli`](../../tests/team/docs/transcripts/setup-cli.txt) |

## Related

- [Which command for which job](workflow-methods.md)
- [Which storage backend and cache](backend.md)
- [All decision guides](README.md)
