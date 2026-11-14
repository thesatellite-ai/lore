# Personal notes

**In short:** some notes are for you alone, like "I prefer tabs over spaces". `--to me` keeps them in your personal store: a folder on your machine, or your own private repo that every machine you use attaches with `lore store init --name me --url <url>`.

## The situation

Alice (an agency developer) keeps notes only she should see: "I always forget the staging VPN profile", "I prefer tabs over spaces". Some are about one company and must never show up in another company's repos. Some apply everywhere. She works on a laptop and on a server, and wants the same notes on both.

## The config

`personal.store` in `~/.lore/config.yaml` names your personal store, the target of `--to me`. `lore store init --name me` writes it for you. It holds one of three things:

- **a git URL of your own private repo** (`lore store init --name me --url git@github.com:alice/lore-me`): lore keeps a clone at `~/.lore/stores/alice/lore-me`, and every machine that attaches the same URL gets the same notes. A note is one file, `_store/<kind>/<id>.json` in that repo, labelled `[me]`, and it loads in every repo you open.
- **a folder** (`lore store init --name me` with no URL makes `file://~/.lore/stores/_me`): the same layout (`_store/<kind>/<id>.json`, `[me]`), on this machine only.
- **the lore 0.1.17 value `local`**: the notes stay in `~/.lore/personal` on this machine, filed by company (`<org>/<kind>/<id>.json` or `everywhere/<kind>/<id>.json`, labelled `[me/<org>]`); a note filed under a company loads only in that company's repos. An upgrade from 0.1.17 turns `local` into the folder address `file://~/.lore/personal` (the old file is kept as `config.yaml.migrated`) and copies the notes into its `_store/`.

```yaml
personal:
  store: git@github.com:alice/lore-me          # or file:///home/alice/.lore/stores/_me, or the 0.1.17 value local
```

With a git URL, each note is pushed to your repo at once. To push them in batches instead, or only when you run `lore sync`, see [How writes reach the remote](../identity-and-stores.md#how-writes-reach-the-remote).

## The commands

With the 0.1.17 value `personal.store: local` (the first example), `--to me` files a note by where you are when you write it. In a repo of an org (one company's setup), it goes under that org's folder. Anywhere else, it goes under `everywhere/`:

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
mem_2ad3…f891 [retrieved] I always forget the staging VPN profile [me/agency]
mem_05e9…4603 [retrieved] I prefer tabs over spaces [me/everywhere]

# alice, in scratch
$ lore memory list
mem_05e9…4603 [retrieved] I prefer tabs over spaces [me/everywhere]

# alice, in ~
$ find ~/.lore/personal -name '*.json' -path '*memories*' | sed 's#.*/personal/##'
everywhere/memories/mem_05e9…4603.json
agency/memories/mem_2ad3…f891.json
```
<!-- /example -->

The `[me]` label marks personal rows (your own notes). In the agency repo Alice sees both notes. In her scratch repo she sees only the `everywhere/` one.

To sync between machines, make a new, empty private repo of your own into your personal store with `lore store init --name me --url <url>` (it becomes a lore-only notes repo and is recorded as `personal.store`). From then on a `--to me` note lands in that repo's `_store/` folder with the label `[me]`, not under a company folder, and loads in every repo. Below, a second machine (`alice-devbox`) that already has `personal.store` set to the same URL sees the laptop's note on its first command:

<!-- example: personal#git-personal-store -->
```console
# alice, in scratch
$ lore store init --name me --url git@github.com:alice/lore-me --yes
lore store init: store clone ~/.lore/stores/alice/lore-me: git user set to alice <alice@devbox> (your lore identity)
✓ created store me (sto_c0f3…b5b8) at git@github.com:alice/lore-me; recorded in ~/.lore/config.yaml (personal.store, the target of --to me)

# alice, in website
$ lore memory add --body 'my invoice template lives in the shared drive' --to me
lore memory add: store clone ~/.lore/stores/alice/lore-me: git user set to Alice Doe <alice@agency.example> (your lore identity, was alice@devbox)
lore: committed locally to store me; lore sync sends it
lore: wrote ~/.lore/stores/alice/lore-me/_store/actors/act_7a67…9504.json (store me)
lore: wrote ~/.lore/stores/alice/lore-me/_store/memories/mem_76bd…3f10.json (store me)
lore: wrote ~/.lore/stores/alice/lore-me/_store/machines/mch_162d…9666.json (store me)
✓ mem_76bd…3f10 [me]
  scope: [master]

# alice-devbox, in website
$ lore memory list
lore memory list: git is not set up for lore's row files in this clone: `lore sync` (or the next lore write here) sets up merge drivers lore and lore-keep-ours in ~/work/website/.git/config; hook blocks in ~/work/website/.git/hooks (commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-commit, pre-push)
lore memory list: store clone ~/.lore/stores/alice/lore-me: git user set to Alice Doe <alice@agency.example> (your lore identity)
(no memories)
```
<!-- /example -->

## On your next machine

Install lore, then attach the personal store you already have. `lore store init --name me --url <url>` on an existing store attaches it (it never empties it) and records it as `personal.store`; your notes are there on the next command (lore also prints a line or two about the git settings it made in the store clone and in this repo, left out here):

```console
# alice, on a new laptop, in a clone of website
$ lore store init --name me --url git@github.com:alice/lore-me --yes
✓ git@github.com:alice/lore-me is already a lore store (sto_…); recorded as me in ~/.lore/config.yaml (personal.store, the target of --to me)
$ lore memory list
mem_…  my invoice template lives in the shared drive  [me]
```

`lore config set --key personal.store --value git@github.com:alice/lore-me` does the same: the next command that needs the store clones it. After that, a note written on one machine reaches the other one at its next `lore sync` there.

## What happened

```text
 personal store at a git URL or a folder (~/.lore/stores/alice/lore-me, ~/.lore/stores/_me)
   _store/memories/<id>.json             [me]: loads in every repo

 personal.store: local (lore 0.1.17 value; ~/.lore/personal)
   everywhere/memories/<id>.json         [me/everywhere]: loads in every repo
   agency/memories/<id>.json             [me/agency]: loads only where the org is agency
   client3/memories/<id>.json            [me/client3]: loads only where the org is client3
```

**Upgrading from lore 0.1.17.** 0.1.17 filed `--to me` notes per org in a git personal store too (`acme/.lore/data/…`, `everywhere/.lore/data/…`), and they keep that scope. Until someone runs `lore migrate run`, this lore reads them where 0.1.17 left them (`everywhere/` notes as `[me/everywhere]`) and writes nothing into that store (`--to me` is refused with `store me is on the lore 0.1.17 layout; run lore migrate run …`). `lore migrate run` moves the `everywhere/` ones to `_store/` in one commit, `lore: personal store — lore 0.1.17's everywhere/ notes move to _store/`, pushed like any commit (a row found in two folders becomes one), and says so in one line, `lore: personal store lore-me: 2 row(s) lore 0.1.17 filed under everywhere/ moved to _store/: commit …, pushed; notes it filed under an org's folder stay there and load only in that org's repos`, after the block that says another machine of yours still on lore 0.1.17 must upgrade (0.1.17 refuses the moved store with `E_NOT_A_STORE`). The notes 0.1.17 filed under `acme/` stay in `acme/` (the layout commit moves them to `acme/memories/…`) and load only in acme's repos, labelled `[me/acme]`; `--store me` lists them with the rest. `lore doctor` names a folder of an org this machine has no entry for (its notes load nowhere until that org is set up). A `local` personal store is moved to its root the same way by `lore fix`; its emptied old folder is removed, and a row left in one is named by `lore doctor` (`old_clones`) and moved by `lore fix`. While a git personal store is still on the lore 0.1.17 layout, `lore doctor` names its `everywhere/` notes with `lore migrate run` as the step that moves them; no other command moves them before that.

**Changing the personal store on the setup page.** Saving the personal store step with another store never hides the notes of the one you used: it says how many notes that store holds (`the personal store used before (…) holds 3 note(s) this setup's new store does not have; …`), keeps it readable as store `me-before` (its notes show labelled `[me-before]`), and `lore move --id <id> --to me --yes` moves each note over; once it holds none it is no longer attached. Choosing `local` on a machine whose `personal.store` is lore 0.1.17's `local` reuses `~/.lore/personal` as the local store and moves its notes to its root on the spot. `lore config check` warns when a setup names its own personal store beside 0.1.17's `personal.store: local` (commands read the 0.1.17 one until it is migrated).

## Pitfalls

- **With `local`, filing is by place, not by text.** Lore never guesses the company from what you wrote. It files the note under the org of the repo you are in. To file a note for every repo, write it from a repo that belongs to no org. A personal store at a git URL or a folder has no company folders: every note loads everywhere.
- **Never in the checkout.** Personal notes are rendered into `~/.lore/render/…`, outside the repo, like internal notes. Nothing personal is written to a committed file. On another machine a fresh clone has no such render yet: `lore doctor` lists it as a note (`… is not rendered yet on this machine`), not a warning, and the next `lore render`, `lore fix` or `lore doctor --fix` writes it.
- **A note that names a company asks first.** When your personal store is a synced repo, every repo you open reads it. So a `--to me` note whose text names a known org or repo owner (for example `client`) stops with `E_NEEDS_CONFIRM` (exit 26): answer `--yes` to keep it personal, or send it `--to` the org's store instead.
- **A personal store repo is a lore store**: `lore store init --name me --url <url>` makes a new, empty, private repo into one. Run on a repo that is already a lore store, it attaches it instead (`… is already a lore store …; recorded as me`), which is how every further machine gets it.
- Personal notes are for you. To give one to your team, use `lore move --id <id> --to agency` (it keeps the id); see [Sharing an internal note with the client](sharing-a-note.md) for how moving works.

## Related

- [Solo developer](solo.md).
- [One person in two companies](two-companies.md): personal notes beside two orgs.
- [Setups from the command line](../setup-cli.md): a personal store per setup.
- [Identity and stores](../identity-and-stores.md); [How writes reach the remote](../identity-and-stores.md#how-writes-reach-the-remote) for batched pushes and folding old history.
