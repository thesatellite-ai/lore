# Which org and identity setup

**In short:** your identity is your email; lore writes it on every note and task you add. An org is one company you work for, written down once in your own `~/.lore/config.yaml`: your email there, its private notes repos, and which repos are its own and which belong to its clients. With no config at all, lore uses the name and email from your git settings, and that is fine for one person.

## The setups in one table

| Setup | What you write | What lore does | Choose it when |
|---|---|---|---|
| **No config** | nothing | uses your git name and email; every repo is "no org" | you work alone, or in a team with no client and no private notes |
| **A fallback identity** | `lore identity set` (saved in `identity:` of `~/.lore/config.yaml`) | uses that email everywhere outside an org | your git email is not the one you want on your notes |
| **One org** | one entry under `orgs:` with `identity`, `stores`, `own:` and `repos:` | in matching repos: your company email, its roster (the list of people and agents) and its store (its private notes repo) | you work for one company |
| **Joining an org someone set up** | `lore org import` | copies the suggested entry from the store, adds your identity, puts you on the roster | your company already uses lore |
| **Two or more orgs** | one entry per company, each with its own `identity` | picks the org from the repo you are in; orgs never mix | you work for two companies, with a different email at each |
| **A different email inside one org** | `lore identity set --org <entry>` | saves that identity in the org's entry | you already have the entry and only the email is wrong |

## Own repo or client repo

Each org entry sorts repos by their address:

| Role | Set by | What changes |
|---|---|---|
| **Own repo** | `own:` patterns, or `lore org link agency --project tools --own` | no leak guards (checks that stop private notes reaching a client); the company's notes may live in the repo |
| **Client repo** | `repos:` patterns, or `lore org link agency --project client` (no `--own`) | leak guards on: private kinds go to the store by default, and commits and pull request text are checked for private notes |
| **No org** | nothing matched | no guards; only your personal `everywhere/` notes load |

## How lore picks the org in a repo

The first rule that answers wins:

1. `LORE_ORG=<entry>`, for one shell or one command.
2. `lore org use <entry>`, for this repo on this machine (saved in your config, not committed).
3. A committed `.lore/org.yaml`, written by `lore org set` (only in the org's own repos).
4. A link in one org's store (made once with `lore org link`).
5. An `own:` or `repos:` pattern that matches the repo's address, for example `github.com/client/*`.
6. The active setup (see [Setups from the command line](../team/setup-cli.md)), when it names an org: used only when rules 1 to 5 found none. `lore whoami --explain` shows it as `setup`.
7. Nothing matched: no org.

Two entries that answer at the same rule stop with `E_ORG_AMBIGUOUS`; lore never guesses. `lore whoami` prints the result and the rule that decided it, and `lore whoami --explain` prints every rule with hit or miss.

## If this, then that

- **If you work alone,** write no config. Check `lore identity show` once; set a fallback with `lore identity set` only if the email is wrong.
- **If your company already uses lore,** run `lore org import` with your own email instead of writing the entry by hand.
- **If you set lore up for your company,** write one org entry, create the store with `lore store init`, register yourself with `lore actor register`, and commit a suggested `lore-org.yaml` into the store's company folder so the next person can import it.
- **If a teammate's repo should name its org for everyone,** run `lore org set` in it. It is refused in a client repo, because it would tell the client the agency's org name.
- **If one repo matches two entries,** pick one with `lore org use <entry>` for this machine, or tighten the patterns.
- **If you work for two companies,** give each entry its own `identity` with that company's email. Lore never borrows one org's identity in another org's repo, and a repo that matches no org never uses an org's identity.
- **If you keep several setups on one machine** (your own projects and a team's), make one setup each with `lore setup plan`, `check` and `apply`; a setup that names an org decides it in its folders when nothing above it does. lore EE adds a browser page for the same steps.
- **If you hire a contractor for one client,** see [How to lay out your stores](store-layout.md): the org setup is the same, the store layout is what limits access.

## Pitfalls

- **Your email is your identity key,** not your display name. Two people with the same name are still two people; one person with two emails is two identities.
- **`@handles` need the roster.** `--assign @bob` works only after Bob (an agency developer) ran `lore actor register` (or `lore org import`).
- **A repo address is read exactly as git config stores it.** `url.insteadOf` rewrites (git settings that swap one address for another) are ignored, so write patterns for the address the remote really has.
- **A staging entry never matches by itself.** An entry whose store branch is not the store's default branch is chosen only with `lore org use` or `LORE_ORG`.
- **Check the config before you trust it.** `lore config check` names the line of every mistake; an invalid config blocks writes until it is fixed.

## Guides and examples for each setup

| Setup | Guide | Example |
|---|---|---|
| No config, fallback identity | [Your identity, joining an org, and store housekeeping](../team/identity-and-stores.md) | [`people`](../../tests/team/docs/transcripts/people.txt) |
| One org | [Getting started](../team/getting-started.md), [Agency with a client repo](../team/use-cases/agency-client-repo.md) | [`getting-started`](../../tests/team/docs/transcripts/getting-started.txt), [`agency`](../../tests/team/docs/transcripts/agency.txt) |
| Joining an org | [Your identity, joining an org, and store housekeeping](../team/identity-and-stores.md) | [`people`](../../tests/team/docs/transcripts/people.txt) |
| Two orgs | [One person in two companies](../team/use-cases/two-companies.md) | [`two-orgs`](../../tests/team/docs/transcripts/two-orgs.txt) |
| How the org is picked | [Concepts: orgs, identity and whoami](../team/concepts.md#orgs-identity-and-whoami), [How it works: org resolution](../team/how-it-works.md#org-resolution) | [`two-orgs`](../../tests/team/docs/transcripts/two-orgs.txt) |
| Setups (several identities on one machine) | [Setups from the command line](../team/setup-cli.md), [Team invites](../team/invites.md) | [`setup-cli`](../../tests/team/docs/transcripts/setup-cli.txt), [`invites`](../../tests/team/docs/transcripts/invites.txt) |
| Every config key | [Config files](../reference/config.md) | [`reference`](../../tests/docs/sections/reference.sh) ([output](../../tests/docs/transcripts/reference.txt)) |

## Related

- [How to lay out your stores](store-layout.md)
- [When to use agents, claims and gates](agents-claims-gates.md)
- [All decision guides](README.md)
