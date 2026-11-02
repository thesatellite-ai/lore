# Personal notes

## The situation

Alice keeps notes only she should see: "I always forget the staging VPN profile", "I prefer tabs over spaces". Some are about one company and must never show up in another company's repos; some apply everywhere. She works on a laptop and on a server and wants the same notes on both.

## The config

`personal.store` in `~/.lore/config.yaml`: `local` keeps the notes in `~/.lore/personal` on this machine; a git URL of your own private repo syncs them between machines.

```yaml
personal:
  store: local                                  # or: git@github.com:alice/lore-me
```

## The commands

`--to me` files a note by where you are when you write it: under the org's folder in an org's repo, under `everywhere/` elsewhere. A note that names a company you know, filed under `everywhere/` on purpose, is questioned first:

<!-- example: personal#local-personal-store -->
```console
# alice, in acme-app
$ lore memory add --body 'I always forget the staging VPN profile' --to me
✓ mem_7a67…9504 mem_7a67…9504 [me]
  filed: --to me → personal northwind/
  scope: [master]

# alice, in scratch
$ lore memory add --body 'I prefer tabs over spaces' --to me
✓ mem_2ad3…f891 mem_2ad3…f891 [me]
  filed: --to me → personal everywhere/
  scope: [master]

# alice, in acme-app
$ lore memory add --body 'northwind invoices go to the finance inbox' --to me --everywhere
note mentions [northwind], a known org or repo. Save it under everywhere/ anyway? [y/N] n
ERROR: [E_INVALID_INPUT] not saved
(exit 1)
$ lore memory list
mem_2ad3…f891  I prefer tabs over spaces  [me]
mem_7a67…9504  I always forget the staging VPN profile  [me]

# alice, in scratch
$ lore memory list
mem_2ad3…f891  I prefer tabs over spaces  [me]

# alice, in ~
$ find ~/.lore/personal -name '*.json' -path '*memories*' | sed 's#.*/personal/##'
everywhere/.lore/data/memories/mem_2ad3…f891.json
northwind/.lore/data/memories/mem_7a67…9504.json
```
<!-- /example -->

The `[me]` label marks personal rows. In the Northwind repo Alice sees both notes; in her scratch repo only the `everywhere/` one.

To sync between machines, make a private repo of your own a store and point `personal.store` at it on every machine. A note written on the laptop shows up on the server on its next command:

<!-- example: personal#git-personal-store -->
```console
# alice, in ~
$ lore store init git@github.com:alice/lore-me
✓ wrote lore-store.yaml to github.com/alice/lore-me (new branch main, first commit) and pushed

# alice, in acme-app
$ lore memory add --body 'the Acme invoice template lives in the shared drive' --to me
✓ mem_162d…9666 mem_162d…9666 [me]
  filed: --to me → personal northwind/
  scope: [master]
lore: [me] 4 file(s) pushed

# alice-devbox, in acme-app
$ lore memory list
mem_162d…9666  the Acme invoice template lives in the shared drive  [me]
```
<!-- /example -->

## What happened

```text
 personal store (~/.lore/personal, or github.com/alice/lore-me)
   everywhere/.lore/data/memories/…   loads in every repo
   northwind/.lore/data/memories/…    loads only where the org is northwind
   betalabs/.lore/data/memories/…     loads only where the org is betalabs
```

## Gotchas

- **Filing is by place, not by text.** Lore never guesses the company from what you wrote; it files the note under the org of the repo you are in. `--everywhere` overrides that, after a warning when the text names a known org or repo (refused without a terminal unless `--yes`).
- **Never in the checkout.** Personal notes are rendered into `~/.lore/render/…`, outside the repo, like internal notes; nothing personal is written to a committed file.
- **A personal store repo is a lore store**: create it with `lore store init` on a new, empty, private repo.
- Personal notes are for you. To give one to your team, use `lore share <id> --to northwind`; see [Sharing an internal note with the client](sharing-a-note.md) for how sharing works.
