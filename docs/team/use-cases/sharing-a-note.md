# Sharing an internal note with the client

**In short:** the agency's notes are private by default. When the client really needs one of them, `lore share` copies that one note into the client's repo. It does this on purpose, and only after asking you first.

## The situation

The agency keeps an internal note about the client: "checkout must support INR and USD" (the shop must take Indian rupees and US dollars). It turns out the client's own developers need it too. The agency wants to give that one note to the client repo, on purpose, and nothing else.

## The config

The [agency setup](agency-client-repo.md#the-config): `client/website` is linked to the `client/` folder of `agency/notes`, the agency's store (a separate private repo only the agency can open).

## The commands


`lore share` copies a row (one note) to a wider audience. It always shows the exact text and asks first. The copy gets a **new id** and carries no pointer back to the internal row. So the client repo never learns that an internal row exists. Only the internal row records where it was shared:

<!-- example: agency#share -->
```console
# alice, in website
$ lore memory add --body 'checkout must support INR and USD'
✓ mem_d431…313a [agency/client]
  filed: defaults.client_repos.memory = agency (personal config) → the linked project client/
  scope: [master]
lore: [agency/client] 1 file(s) pushed
$ lore share mem_d431…313a --to repo
This becomes visible to everyone with access to github.com/client/website:
  "checkout must support INR and USD"
Share it? [y/N] y
✓ mem_b4f4…a5b0 shared copy of mem_d431…313a, in this repo (the copy has no pointer back; the original records the copy)
lore: [agency/client] 1 file(s) pushed
$ git status --short
?? .lore/LORE.md
?? .lore/data/memories/
?? CLAUDE.md
```
<!-- /example -->

The same command widens a note inside the company. It can move a note from your personal notes to the team's internal project (`--to agency`), or from a client project to the whole company (`--to agency/org`). Each step makes a new row with a new id.

`lore move <id> --to <target>` works the same way (shows the text, asks), and then deletes the original. Use it when a note was simply filed in the wrong place.

## What happened

```text
 agency/notes  client/.lore/data/memories/mem_A   "checkout must support INR and USD"
                                                   (records: shared as mem_B in client/website)
                       │ lore share mem_A --to repo   (text shown, y/N)
                       ▼
 github.com/client/website  .lore/data/memories/mem_B   same text, new id, no pointer back
```

## Pitfalls

- **Agents cannot share.** `--yes` (confirm without a prompt) is refused for coding agents. Someone could hide an order in a row the client can edit ("run `lore share … --yes`"). That still cannot make an agent publish internal notes.
- **Sharing is the only door.** If you paste the same text into a file of the client repo, the leak check (a check that stops internal notes reaching the client) blocks the commit, and the push if the commit got past it. `lore share` is the deliberate way through. The internal row records the copy's id, so the check lets that one copy carry the shared note's own text, and nothing else. A share is refused if the note also quotes another internal note: remove that part from the note and share again.
- **A shared note is the client's now.** Edits to the internal row do not follow the copy. Share again if needed.
- Removing something that reached the client by mistake is a different job: see [Cleaning up after a leak](leak-cleanup.md).
