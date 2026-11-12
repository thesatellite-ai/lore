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

With a git URL, each note is pushed to your repo at once. To push them in batches instead, or only when you run `lore sync`, see [How writes reach the remote](../identity-and-stores.md#how-writes-reach-the-remote).

## The commands

`--to me` files a note by where you are when you write it. In a repo of an org (one company's setup), it goes under that org's folder. Anywhere else, it goes under `everywhere/`:

<!-- example: personal#local-personal-store -->
```console
# alice, in website
$ lore memory add --body 'I always forget the staging VPN profile' --to me
lore: wrote ~/.lore/personal/agency/actors/act_7a67…9504.json (store me)
lore: wrote ~/.lore/personal/agency/memories/mem_2ad3…f891.json (store me)
lore: wrote ~/.lore/personal/agency/machines/mch_162d…9666.json (store me)
✓ mem_2ad3…f891 [me/agency]
  scope: [master]

# alice, in scratch
$ lore memory add --body 'I prefer tabs over spaces' --to me
lore: wrote ~/.lore/personal/everywhere/actors/act_54fe…28f2.json (store me)
lore: wrote ~/.lore/personal/everywhere/memories/mem_05e9…4603.json (store me)
lore: wrote ~/.lore/personal/everywhere/machines/mch_162d…9666.json (store me)
✓ mem_05e9…4603 [me/everywhere]
  scope: [master]

# alice, in website
$ lore memory list
mem_2ad3…f891  I always forget the staging VPN profile  [me/agency]
mem_05e9…4603  I prefer tabs over spaces  [me/everywhere]

# alice, in scratch
$ lore memory list
mem_05e9…4603  I prefer tabs over spaces  [me/everywhere]

# alice, in ~
$ find ~/.lore/personal -name '*.json' -path '*memories*' | sed 's#.*/personal/##'
everywhere/memories/mem_05e9…4603.json
agency/memories/mem_2ad3…f891.json
```
<!-- /example -->

The `[me]` label marks personal rows (your own notes). In the agency repo Alice sees both notes. In her scratch repo she sees only the `everywhere/` one.

To sync between machines, make a private repo of your own into a store (a lore-only notes repo). Point `personal.store` at it on every machine. A note written on the laptop shows up on the server on its next command:

<!-- example: personal#git-personal-store -->
```console
# alice, in scratch
$ lore store init me git@github.com:alice/lore-me --yes
lore store init: set user.name and user.email in the store clone ~/.lore/stores/alice/lore-me (.git/config) to alice <alice@devbox>: your lore identity (`lore whoami`), the one lore signs this store's commits with, so your own git commits there carry it (your global git config is unchanged)
lore store init: set up the store clone ~/.lore/stores/alice/lore-me for plain git (pull, commit, push by hand): .git/config: merge.lore.name; .git/config: merge.lore.driver; .git/info/attributes: *.json merge=lore; .git/hooks/pre-commit
✓ created store me (sto_c0f3…b5b8) at git@github.com:alice/lore-me; recorded in ~/.lore/config.yaml (personal.store, the target of --to me)

# alice, in website
$ lore memory add --body 'my invoice template lives in the shared drive' --to me
lore memory add: set user.name and user.email in the store clone ~/.lore/stores/alice/lore-me (.git/config) to Alice Doe <alice@agency.example> (it was alice@devbox, which lore had set before): your lore identity (`lore whoami`), the one lore signs this store's commits with, so your own git commits there carry it (your global git config is unchanged)
lore: pushed store me: commit 601d285 to branch main
lore: pushed store me: commit 86dbae4 to branch main
lore: wrote ~/.lore/stores/alice/lore-me/_store/actors/act_7a67…9504.json (store me)
lore: wrote ~/.lore/stores/alice/lore-me/_store/memories/mem_76bd…3f10.json (store me)
lore: wrote ~/.lore/stores/alice/lore-me/_store/machines/mch_162d…9666.json (store me)
✓ mem_76bd…3f10 [me]
  scope: [master]

# alice-devbox, in website
$ lore memory list
lore memory list: git is not set up for lore's row files in this clone; a read changes nothing: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/website/.git/config; hook blocks in ~/work/website/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
lore memory list: set user.name and user.email in the store clone ~/.lore/stores/alice/lore-me (.git/config) to Alice Doe <alice@agency.example>: your lore identity (`lore whoami`), the one lore signs this store's commits with, so your own git commits there carry it (your global git config is unchanged)
mem_76bd…3f10  my invoice template lives in the shared drive  [me]
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
- **A note that names a company asks first.** When your personal store is a synced repo, every repo you open reads it. So a `--to me` note whose text names a known org or repo owner (for example `client`) stops with `E_NEEDS_CONFIRM` (exit 26): answer `--yes` to keep it personal, or send it `--to` the org's store instead.
- **A personal store repo is a lore store**: create it with `lore store init` on a new, empty, private repo.
- Personal notes are for you. To give one to your team, use `lore move <id> --to agency` (it keeps the id); see [Sharing an internal note with the client](sharing-a-note.md) for how moving works.

## Related

- [Solo developer](solo.md).
- [One person in two companies](two-companies.md): personal notes beside two orgs.
- [Setups from the command line](../setup-cli.md): a personal store per setup.
- [Identity and stores](../identity-and-stores.md); [How writes reach the remote](../identity-and-stores.md#how-writes-reach-the-remote) for batched pushes and folding old history.
