# Sharing an internal note with the client

## The situation

Northwind's internal note for Acme says "checkout must support INR and USD". It turns out Acme's own developers need it too. Northwind wants to give that one note to the client repo, on purpose, and nothing else.

## The config

The [agency setup](agency-client-repo.md#the-config): `acme/app` linked to `northwind/lore acme/`.

## The commands


`lore share` copies a row to a wider audience. It always shows the exact text and asks first. The copy gets a **new id** and carries no pointer back to the internal row, so the client repo never learns that an internal row exists; only the internal row records where it was shared:

<!-- example: agency#share -->
```console
# alice, in acme-app
$ lore memory add --body 'checkout must support INR and USD'
✓ mem_d431…313a mem_d431…313a [northwind/acme]
  filed: defaults.client_repos.memory = northwind (personal config) → the linked project acme/
  scope: [master]
lore: [northwind/acme] 1 file(s) pushed
$ lore share mem_d431…313a --to repo
This becomes visible to everyone with access to github.com/acme/app:
  "checkout must support INR and USD"
Share it? [y/N] y
✓ mem_b4f4…a5b0 shared copy of mem_d431…313a, in this repo (the copy has no pointer back; the original records the copy)
lore: [northwind/acme] 1 file(s) pushed
$ git status --short
?? .lore/LORE.md
?? .lore/data/memories/
?? CLAUDE.md
```
<!-- /example -->

The same command widens a note inside the company: from your personal notes to the team's internal project (`--to northwind`), or from a client project to the whole company (`--to northwind/org`). Each step makes a new row with a new id.

`lore move <id> --to <target>` works the same way (shows the text, asks), and then deletes the original: use it when a note was simply filed in the wrong place.

## What happened

```text
 northwind/lore  acme/.lore/data/memories/mem_A   "checkout must support INR and USD"
                                                   (records: shared as mem_B in acme/app)
                       │ lore share mem_A --to repo   (text shown, y/N)
                       ▼
 github.com/acme/app  .lore/data/memories/mem_B   same text, new id, no pointer back
```

## Gotchas

- **Agents cannot share.** `--yes` (confirm without a prompt) is refused for agent actors. A prompt planted in a client-writable row ("run `lore share … --yes`") cannot make an agent publish internal notes.
- **Sharing is the only door.** Pasting the same text into a file of the client repo is blocked at commit by the leak check; `lore share` is the deliberate way through, and the copy's new id is what keeps the check from firing on it later.
- **A shared note is the client's now.** Edits to the internal row do not follow the copy; share again if needed.
- Removing something that reached the client by mistake is a different job: see [Cleaning up after a leak](leak-cleanup.md).
