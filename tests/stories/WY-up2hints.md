# WY — every printed next step works where it is printed (owner use pass 2)

**Who.** Ada, the admin of the org `acme` (made-up names), on one laptop: the home folder, the org's own repo `~/code/platform` (linked), an own repo `~/code/web` no folder is linked to, a client repo `~/code/shop` (the org's `repos:` pattern) no folder is linked to, and the store's clone.

**Why.** The owner's second use pass found hints that did not work or looped: whoami in an unlinked client repo said `lore fix here installs it` while `lore fix` refused there; a read in an unlinked own repo suggested `--project org` although the resource naming the repo says `project = <its folder>`; a mistyped id in the home folder bounced between "add --setup" and "--store does not apply" (no `--store` was given); a row deleted from its folder got the branch hint in the home folder and "it is in <folder>" elsewhere; a client repo's read refusal never named `--store`.

**Test.** `TestStoryWYUp2Hints` (tests/stories/WY_up2hints_test.go). After each answer, every concrete `lore …` command it printed (`hint: lore …`, a command in backticks, `link it: …`, `restore: …`) runs in the same folder and must not be refused as a wrong hint, refuse `E_NOT_A_STORE`, or answer the first hint again.

## Steps

1. Day 1: Ada starts `acme` from `~/code/platform`, declares `github.com/acme-co/*` own and `github.com/client-co/*` client, clones `web` and `shop` unlinked, and writes a memory to `acme/sync` from the home folder.
2. D-591: `lore trash list` in `shop` is refused naming `--store acme/<folder>` for reads beside `--to` for writes; `lore search --store acme/sync` there finds the memory.
3. D-587: `lore whoami` in `shop` says the guards are off and names `lore org link --org acme --project <folder>` (never `lore fix`); after that link, whoami says guards on.
4. D-588: a resource kept in `acme/org` with `project = sync` names `web`'s remote: `memory list` and `search` in `web` read `acme/sync` and suggest `lore org link --org acme --project sync --own`, never `--project org`.
5. D-589: `memory edit`, `memory show` and `trash restore` of a mistyped id in the home folder (and with `--setup`) say the id is in no store of any setup, list the folders looked in, and name no flag the command was not given.
6. D-590: a rule deleted from `acme/sync` answers "is in the trash" from the home folder, the store clone's `org` folder and the linked repo; once purged (a tombstone), `rule show` from each of them says `deleted from acme/sync on <date>` with `lore trash list --store acme/sync`, never "it is in" and never a branch hint.
