# Sharing an internal note with the client

**In short:** the agency's notes are private by default. When the client really needs one of them, `lore move` gives that one note to the client's repo. It does this on purpose, and only after showing the text and asking you first.

## The situation

The agency keeps an internal note about the client: "checkout must support INR and USD" (the shop must take Indian rupees and US dollars). It turns out the client's own developers need it too. The agency wants to give that one note to the client repo, on purpose, and nothing else.

## The config

The [agency setup](agency-client-repo.md#the-config): `client/website` is linked to the `client/` folder of `agency/notes`, the agency's store (a separate private repo only the agency can open).

## The commands

A row lives in exactly one place, so there is no copy: `lore move <id> --to repo` moves the note from the store into the client repo with the **same id**. It always shows the exact text and asks first. The store keeps only a tombstone (a deletion marker), so the note is no longer internal anywhere:

<!-- example: agency#move -->
```console
# alice, in website
$ lore memory add --body 'checkout must support INR and USD'
✓ mem_4c59…6c38 [agency/client]
  filed: defaults.client_repos.memory = agency (personal config) → the linked project client/
  scope: [master]
$ lore move mem_4c59…6c38 --to repo
y
This becomes visible to everyone with access to repo (client):
  "checkout must support INR and USD" (mem_4c59…6c38, id kept), now in agency
  kept back: internal metadata, internal and sensitive resource fields, time entries and claim state (listed after the move)
Move it? [y/N] ✓ mem_4c59…6c38 moved to store repo (same id; the old copy is tombstoned)
$ git status --short
?? .lore/LORE.full.md
?? .lore/LORE.md
?? .lore/data/memories/
```
<!-- /example -->

The question names where the row goes and who can read it there, its title and id (kept), the store it is in now, and any metadata that travels with it (sensitive values masked; a key that the target already has is listed as reused, any other key stays where it is, because a move never creates a key). In a client repo it also says what is kept back, and it shows what `--drop-internal` and `--with-children` change. Without a terminal (an agent, a script) the move stops with `E_NEEDS_CONFIRM`, and that error carries the same question, so the agent can show its person exactly what would move; `--yes` answers it. A row whose metadata is quarantined (an entry lore could not read cleanly) is not moved: lore answers `E_META_QUARANTINED` (exit 23) and leaves the row where it is; `lore meta show <id>` names the bad entry, and the move works once it is fixed.

The same command moves a note between other places: from your personal notes to the team's internal project (`--to agency`), from a client project to the whole company (`--to agency/org`), or back from the client repo into the store. The id never changes.

## What happened

```text
 agency/notes  client/.lore/data/memories/mem_A   "checkout must support INR and USD"
                       │ lore move mem_A --to repo   (text shown, y/N)
                       ▼
 github.com/client/website  .lore/data/memories/mem_A   same id; the store keeps a tombstone
```

## Pitfalls

- **There is no share command.** It was removed in 0.1.18 (lore answers "unknown command"): a row has one home and lore never makes a second copy, so `lore move` is the one command.
- **Moving is the only door.** If you paste the same text into a file of the client repo, the leak check (a check that stops internal notes reaching the client) blocks the commit, and the push if the commit got past it. `lore move` is the deliberate way through. A move into a client repo is refused if the note also quotes another internal note: remove that part from the note and move it again.
- **A moved note is the client's now.** Commit `.lore/data/memories/` in the client repo like any other change.
- Removing something that reached the client by mistake is a different job: see [Cleaning up after a leak](leak-cleanup.md).
