# Decision guides

**In short:** if you work alone in one repo, you do not have to choose anything: lore just works. These short pages help when your setup grows: several repos, a client, private notes, or a few agents. Each page gives you a table of the options, plain "if this, then that" advice, the traps to avoid, and a guide and a real example for every option.

| Question | Page |
|---|---|
| Where do my notes live: in each repo, in one database on my machine, or in a store? | [Where your notes live](database-mode.md) |
| Should a note travel with my branch, or reach everyone at once? | [Keep a note with the code, or share it at once](with-code-or-live.md) |
| One private notes repo for everything, one per client, or a mix? | [How to lay out your stores](store-layout.md) |
| Do I need an org entry, and how does lore pick my identity? | [Which org and identity setup](org-and-identity.md) |
| Which file does my coding tool read, and how do I fill it? | [Which file your coding tool reads](render-targets.md) |
| When do I need agent identities, claims, gates and resource holds? | [When to use agents, claims and gates](agents-claims-gates.md) |
| Folder store or git store, and where does the cache live? | [Which storage backend and cache](backend.md) |
| Rule, memory or decision? Task, mission, plan or reminder? Peek, promote or live? Command line, local API or terminal browser? | [Which command for which job](workflow-methods.md) |
| I am solo, a small team, an agency, a contractor, running agents or CI: which settings? | [Which options for which kind of team](by-team-shape.md) |

## A good default for most people

- One repo, no config: run `lore init`. Notes travel with the code, tasks are live (every teammate sees them whatever branch they are on, once you run `lore sync` or `git push`), and `lore render` points the file your coding tool reads (`AGENTS.md`, `CLAUDE.md` and the like) at them. You do not need to change anything on this list.
- An agency with clients: one org entry (a few lines in your config that say which company you work for), one store for everything (a separate private repo that only the agency can open, like a locked filing cabinet next to the client's repo), and each client repo linked to a project in the store. Split the store into one per client the day a contractor or a confidentiality agreement (NDA) appears.
- Several agents: one mission (one shared goal with its tasks). Start each agent with `lore agent start`; it takes its next task with `lore task next`. Put a gate (a step that waits for a person to say yes) on every risky step.

## Every example is a real run

Each "Example" link on these pages points at a script that runs the real lore binary in a throwaway folder, or at the output it printed. The scripts live in [tests/docs](../../tests/docs/README.md) and [tests/team/docs](../../tests/team/docs/run-examples.sh).

## Related

- [Documentation home](../README.md): the learning path and every page.
- [Feature index](../reference/features.md): every feature with its guide and example.
