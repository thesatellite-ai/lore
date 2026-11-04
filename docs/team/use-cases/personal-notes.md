# Personal notes

**In short:** some notes are for you alone, like "I prefer tabs over spaces". `--to me` keeps them on your machine, or in your own private repo. Lore shows notes about one company only in that company's repos.

## The situation

Alice (an agency developer) keeps notes only she should see: "I always forget the staging VPN profile", "I prefer tabs over spaces". Some are about one company and must never show up in another company's repos. Some apply everywhere. She works on a laptop and on a server, and wants the same notes on both.

## The config

`personal.store` in `~/.lore/config.yaml`. `local` keeps the notes in `~/.lore/personal` on this machine. A git URL of your own private repo syncs them between machines.

```yaml
personal:
  store: local                                  # or: git@github.com:alice/lore-me
```

## The commands

`--to me` files a note by where you are when you write it. In a repo of an org (one company's setup), it goes under that org's folder. Anywhere else, it goes under `everywhere/`:

<!-- example: personal#local-personal-store -->
```console
# alice, in website
$ lore memory add --body 'I always forget the staging VPN profile' --to me
✓ mem_7a67…9504 [me]
  filed: --to me → personal agency/
  scope: [master]

# alice, in scratch
$ lore memory add --body 'I prefer tabs over spaces' --to me
✓ mem_2ad3…f891 [me]
  filed: --to me → personal everywhere/
  scope: [master]

# alice, in website
$ lore memory list
mem_7a67…9504  I always forget the staging VPN profile  [me]
mem_2ad3…f891  I prefer tabs over spaces  [me]

# alice, in scratch
$ lore memory list
mem_2ad3…f891  I prefer tabs over spaces  [me]

# alice, in ~
$ find ~/.lore/personal -name '*.json' -path '*memories*' | sed 's#.*/personal/##'
everywhere/.lore/data/memories/mem_2ad3…f891.json
agency/.lore/data/memories/mem_7a67…9504.json
```
<!-- /example -->

The `[me]` label marks personal rows (your own notes). In the agency repo Alice sees both notes. In her scratch repo she sees only the `everywhere/` one.

To sync between machines, make a private repo of your own into a store (a lore-only notes repo). Point `personal.store` at it on every machine. A note written on the laptop shows up on the server on its next command:

<!-- example: personal#git-personal-store -->
```console
# alice, in ~
$ lore store init git@github.com:alice/lore-me
✓ wrote lore-store.yaml to github.com/alice/lore-me (new branch main, first commit) and pushed

# alice, in website
$ lore memory add --body 'the client invoice template lives in the shared drive' --to me
✓ mem_162d…9666 [me]
  filed: --to me → personal agency/
  scope: [master]
lore: [me] 4 file(s) pushed

# alice-devbox, in website
$ lore memory list
mem_162d…9666  the client invoice template lives in the shared drive  [me]
```
<!-- /example -->

## What happened

```text
 personal store (~/.lore/personal, or github.com/alice/lore-me)
   everywhere/.lore/data/memories/…      loads in every repo
   agency/.lore/data/memories/…          loads only where the org is agency
   client3/.lore/data/memories/…         loads only where the org is client3
```

## Pitfalls

- **Filing is by place, not by text.** Lore never guesses the company from what you wrote. It files the note under the org of the repo you are in. To file a note for every repo, write it from a repo that belongs to no org.
- **Never in the checkout.** Personal notes are rendered into `~/.lore/render/…`, outside the repo, like internal notes. Nothing personal is written to a committed file.
- **A personal store repo is a lore store**: create it with `lore store init` on a new, empty, private repo.
- Personal notes are for you. To give one to your team, use `lore share <id> --to agency`; see [Sharing an internal note with the client](sharing-a-note.md) for how sharing works.
