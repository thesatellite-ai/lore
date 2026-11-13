# How to lay out your stores

**In short:** a store is a separate git repo that holds only notes, never code, like a locked filing cabinet next to the client's repo. It lets the agency keep notes the client must never read. You can keep everything in one store, give each client its own store, or mix the two. Pick by one question: who must never see which client's notes? GitHub decides who can open a repo, and it cannot hide one folder inside a repo.

## The one rule behind every layout

Anything in the client's repo, on any branch or hidden list (a list on GitHub that is not a branch), the client can download. So the agency's private notes about the client live in a separate repo, `github.com/agency/notes`, that only the agency can open. Inside a store, each folder is one project (`client/`, `client2/`), and one folder is the company folder. It holds the roster (the list of people and agents in the org), the teams and the company-wide rules. The config key `org_folder` names it; `org_folder: "."` means the store's root is the company folder.

## The layouts in one table

| Layout | What it looks like | Who sees what | Choose it when |
|---|---|---|---|
| **No store** | notes only in each repo's `.lore/data` | everyone who can read the repo | you work alone or only in your own repos, and nothing is private |
| **One store for everything** | `agency/notes` with `org/`, `client/`, `client2/` folders | everyone who can open `agency/notes` sees every client | a small agency where every developer works on every client |
| **One store per client** | a company store `agency/notes` (`org_folder: "."`) plus `agency/notes-client`, `agency/notes-client2` | each person sees the company store and only the client stores GitHub lets them open | contractors, confidentiality agreements (NDAs), or teams that must not see each other's clients |
| **Mixed** | one main store for the company folder and most clients, plus a separate store for the client that needs it | most people see the main store; only some see the separate one | one client is special (an NDA, a contractor) and the rest are not |
| **A staging branch** | a second branch of the same store, for example `staging` | the same people as the store | you want test notes and staging-only tasks that never mix with the real ones |
| **Personal store** | `personal: {store: local}` or your own private repo | only you | notes for you alone, like "I prefer tabs over spaces" |

All of these are plain config in `~/.lore/config.yaml`; the commands are the same in every layout. A layout can change later: add a store to the `stores:` list, create it with `lore store init --name <url>`, and link the client's repo to it with `lore org link --org agency --project client --store <url>`.

## If this, then that

- **If you are one person with your own repos,** start with no store. Add one the day a client repo or a private note appears.
- **If every developer at the agency works on every client,** use one store for everything. It is one repo to create and one access list to keep.
- **If a contractor must see only one client,** give that client its own store and give the contractor GitHub access to the company store and that one store only.
- **If only one client is special,** use the mixed layout: move that client to its own store and keep the rest together.
- **If one client has several repos that make one product** (client 2's `app`, `api` and `infra`), link all of them to one project folder (`client2/`), so they share one mission and task list.
- **If you need a place to try things out,** use a staging branch of the same store, chosen with `lore org use` for one repo or `LORE_ORG` for one shell. A staging entry never matches a repo by itself.
- **If a note is only for you,** write it with `--to me`. With `personal.store` set to a store address, such a note loads in every repo you open. The older value `personal.store: local` (`~/.lore/personal`) files it by company instead: under the company's folder in that company's repos, under `everywhere/` elsewhere ([Personal notes](../team/use-cases/personal-notes.md)).
- **If a store's notes may appear in committed files** (a render, a client repo), mark it `lore store visibility --name <name> --visibility shareable`; every store starts `internal`.

## Pitfalls

- **GitHub permissions are the access control.** Leaving a store out of someone's config is a convenience. Removing their access to the store repo is what keeps it out of reach.
- **Folder names are unique across all of an org's stores,** and exactly one store sets `org_folder`. A folder made in the wrong store is removed with `lore store folder remove --store <store> --folder <folder>` (an empty one at once; one with rows only with `--purge --confirm`, rows to the trash first), which frees the name for another store.
- **A store is never a code repo.** `lore store init` writes a `lore-store.yaml` marker, and lore refuses to use a repo that holds code as a store.
- **Lore never moves rows between store branches.** Each store branch is a separate lore; staging notes stay on staging.
- **Copies already fetched stay fetched.** When someone loses access, what they already downloaded stays on their machine, like any git clone. Plan the layout before you write private notes, not after.
- **A link lives only in the store.** `lore org link` writes nothing into the client's repo, which needs no `lore init`: a repo with no lore data is linked by its remote, and lore never writes into it (no `.lore/`, ignore lines or hooks). Everyone at the agency finds the link on their next command.

## Guides and examples for each layout

| Layout | Guide | Example |
|---|---|---|
| No store | [Solo developer](../team/use-cases/solo.md), [Small team in one repo](../team/use-cases/small-team.md) | [`solo`](../../tests/team/docs/transcripts/solo.txt), [`small-team`](../../tests/team/docs/transcripts/small-team.txt) |
| One store for everything | [Agency with a client repo](../team/use-cases/agency-client-repo.md) | [`agency`](../../tests/team/docs/transcripts/agency.txt) |
| One store per client, mixed | [Contractor limited to one client](../team/use-cases/contractor.md) | [`contractor`](../../tests/team/docs/transcripts/contractor.txt) |
| Several repos, one project | [Several repos as one product](../team/use-cases/group-project.md) | [`group`](../../tests/team/docs/transcripts/group.txt) |
| Staging branch | [Same store, separate staging](../team/use-cases/staging.md) | [`staging`](../../tests/team/docs/transcripts/staging.txt) |
| Personal store | [Personal notes](../team/use-cases/personal-notes.md) | [`personal`](../../tests/team/docs/transcripts/personal.txt) |
| Looking after stores | [Your identity, joining an org, and store housekeeping](../team/identity-and-stores.md), [Your stores: list, move, retire](../sync/stores.md) | [`people`](../../tests/team/docs/transcripts/people.txt), [`stores`](../../tests/docs/sections/stores.sh) ([output](../../tests/docs/transcripts/stores.txt)) |

## Related

- [Concepts: stores and links](../team/concepts.md#stores-and-links)
- [Which storage backend and cache](backend.md): folder store or git store, and where each keeps its files.
- [Which org and identity setup](org-and-identity.md)
- [Keep a note with the code, or share it at once](with-code-or-live.md)
- [All decision guides](README.md)
