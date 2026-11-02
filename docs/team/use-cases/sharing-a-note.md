# Sharing an internal note with the client

## The situation

Northwind's internal note for Acme says "checkout must support INR and USD". It turns out Acme's own developers need it too. Northwind wants to give that one note to the client repo, on purpose, and nothing else.

## The config

The [agency setup](agency-client-repo.md#the-config): `acme/app` linked to `northwind/lore acme/`.

## The commands

> **Known bug (BUG-D1):** in the current build `lore share --to repo` from a linked client repo is refused with `E_LEAK_BLOCKED … pointers flow inward only`, as the output below shows. A fix is in progress; this page describes the intended behaviour and the example will show the successful share once the fix lands.

`lore share` copies a row to a wider audience. It always shows the exact text and asks first. The copy gets a **new id** and carries no pointer back to the internal row, so the client repo never learns that an internal row exists; only the internal row records where it was shared:

<!-- example: agency#share -->
```console
# alice, in acme-app
$ lore memory add --body 'checkout must support INR and USD'
✓ mem_01a1…2197 mem_01a1…2197 [northwind/acme]
  filed: defaults.client_repos.memory = northwind (personal config) → the linked project acme/
  scope: [master]
lore: [northwind/acme] 1 file(s) pushed
$ lore share mem_01a1…2197 --to repo
This becomes visible to everyone with access to github.com/acme/app:
  "checkout must support INR and USD"
Share it? [y/N] y
ERROR: [E_LEAK_BLOCKED] xref: pointers flow inward only: a client-readable row may not point at a internal row (the pointer alone reveals it); nothing written
  hint: write the pointer on the internal side instead (e.g. --to <org>), where the client cannot read it
$ git status --short
?? .lore/LORE.md
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
