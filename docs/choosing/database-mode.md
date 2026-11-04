# Where your notes live: one repo, one database, or a store

**In short:** most people never choose: `lore init` puts the notes inside the repo, and they travel with your commits (Mode A). Pick something else only when one product is split over several repos. Working alone, you can keep the notes they share in one folder store on your machine, attached in each repo. (Older lore had a shared database file for this, Mode B; it is retired, and `lore migrate run` converts such a repo.) Working as a team, you link the repos to one project in your company's private notes repo (a store).

## The options in one table

| Option | Where the notes are | Who sees them | Travels with git | Set up with |
|---|---|---|---|---|
| **Mode A** (the default) | `.lore/lore.db` in the repo, plus one small file per note in `.lore/data`, committed with your code | everyone who can read the repo | yes, with every commit, branch and pull request | `lore init` |
| **Folder store** (replaces Mode B) | one folder on your machine, for example `~/stores/client2`, attached in each repo; notes written `--to` it, everything else stays in the repo | only you, on this machine | no: the store folder is not committed anywhere | `lore store init <name> file://<path>` once, `lore store add <name> <url>` in each other repo |
| **Store group** | one internal project in your company's store (a separate notes-only repo), linked from each repo | everyone at your company who can open the store | yes, through the store repo, which lore pushes by itself | `lore store init`, then `lore org link` in each repo |

A few words first:

- A **database** here is one file lore reads fast. Think of it as the index card box on your desk.
- The **`.lore/data` folder** holds the same notes as small text files that git can merge, like the pages of a shared binder.
- A **pointer** is a tiny file that says "my notes are over there", like a sticky note on an empty drawer.
- A **store** is a separate git repo that holds only notes, never code. The agency's store is `github.com/agency/notes`, and only the agency can open it.

## If this, then that

- **If you work in one repo, alone or with a team,** keep Mode A. Do nothing; `lore init` already set it up.
- **If one repo holds several apps (a monorepo),** stay on Mode A and run `lore init` in each app folder that wants its own notes. Each folder becomes its own project with its own notes, and all of them share the repo's one hidden list of live rows (notes every teammate sees at once, whatever branch they are on).
- **If one product is split over several repos and you work alone,** use a folder store for the notes they share, and write those notes `--to` it.
- **If you cannot or must not commit notes to a repo** (a repo you do not own, a repo that refuses extra files), write them `--to` a folder store instead.
- **If a repo still has a v0.1.17 shared-database pointer** (`.lore/lore.yaml`), run `lore migrate run` in it; every other command stops and says so.
- **If one product is split over several repos and a team works on it,** use a store group: one project in the store, linked from every repo, so every repo shows the same mission (a shared goal) and the same tasks.
- **If the repos belong to a client,** use a store group even when you work alone, so the agency's notes about the client never land in the client's repo.
- **If you only want to label notes by part of the code** ("Buttons are blue" is for the shop, not the help pages), stay on Mode A and add repos inside the project with `lore repo add`.

## Pitfalls

- **A folder store is not backed up by git.** It lives only on your machine; back the folder up yourself, or use a git store.
- **Commit `.lore/stores.yaml` only when everyone uses the same path.** It records the store's address on your machine. If teammates keep it elsewhere, leave the file out of git.
- **A folder store is not a team feature.** Teammates cannot see it. For shared notes across repos, use a store group.

## Guides and examples for each option

| Option | Guide | Example |
|---|---|---|
| Mode A | [How notes travel with git](../sync/how-notes-travel-with-git.md) | [`sync`](../../tests/docs/sections/sync.sh) ([output](../../tests/docs/transcripts/sync.txt)) |
| Mode A, parts of the code | [Projects and repos](../sync/multi-repo-projects.md) | [`projects`](../../tests/docs/sections/projects.sh) ([output](../../tests/docs/transcripts/projects.txt)) |
| Folder store (replaces Mode B) | [One store for several repos](../sync/shared-database-mode-b.md) | [`projects`](../../tests/docs/sections/projects.sh) ([output](../../tests/docs/transcripts/projects.txt)) |
| Store group | [Several repos as one product](../team/use-cases/group-project.md) | [`group`](../../tests/team/docs/transcripts/group.txt) |
| Monorepo with several projects | [Concepts: branch data, live rows and signals](../team/concepts.md#branch-data-live-rows-and-signals) | [`internals`](../../tests/team/docs/transcripts/internals.txt) |

## Related

- [Keep notes with the code or share them at once](with-code-or-live.md)
- [How to lay out your stores](store-layout.md)
- [All decision guides](README.md)
