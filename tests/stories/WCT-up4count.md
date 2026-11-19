# WCT — what a sync and a write say they moved is what git moved

**Who.** Ada (made-up names) is the admin of the org `acme`; her team store is `github.com/acme-co/lore-team`, her own repo `~/code/platform` and a client's repo `github.com/client-co/shop` (cloned three times: linked in the setup's folders, linked outside them, and not linked). Ben joins from his own laptop with an invite and pushes first; Ada puts him on the team `@platform`, so `acme/platform` is a folder he may add to (a member adds only in the folder of a team they are on).

**Why.** Owner use pass 4: `lore sync` in a linked client repo after a teammate's push printed counts that did not match git (the merge commit the sync made was counted as received and as sent), and `memory add --to <org>/<folder>` from the home folder said "wrote 4 row file(s)" over a commit of 1 file (rows rewritten with the same bytes were counted; a folder's new `_meta.json` was never named).

**Test.** `TestStoryWCTUp4Count` (tests/stories/WCT_up4count_test.go), about 30 s; the set-url move matrix `TestStoryWCTStoreMoveForward` (tests/stories/WCT_storemove_test.go), about 12 s; the WW write matrix's rule (b) checks every write op in every place the same way (`TestStoryWWWrites`).

## Steps

1. Ada starts the team in `~/code/platform`, adds `own: github.com/acme-co/*` and `repos: github.com/client-co/*`, links the client clones to `acme/shop` and `platform` to `acme/platform --own`.
2. Ben joins. In each place (client repo, client repo outside the setup's folders, unlinked client repo, own linked repo, the store clone's `shop/` folder): Ben adds two memories to `acme/platform` and syncs; Ada adds three memories there and three from the home folder (to `acme/org` and `acme/platform`), then runs `lore sync` there.
3. The move matrix: Ada moves the store with `lore store set-url --name acme --url <new repo>` (the old repo still exists, the history copied over) three times, once per explicit push path: `lore sync` in `~/code/platform`, `lore sync --setup acme-admin` from home, `lore store push --store acme`. Ben syncs after each.
4. A day later, from the home folder under a coding agent: `memory add`, `decision add`, `task add` (the folder's first task list) to `acme/shop`, and `memory add` to `acme/org`.

## What must hold

- `sent N` on the store line is the commits `git rev-list` counts on the store remote between before and after the sync, `received N` the commits the remote held that the clone did not (Ben's), in every place, and the `lore: pushed store …` line is printed in every place.
- After each set-url: the old repo is unchanged until an explicit push (set-url and a read push nothing); that push prints one line naming the old repo with git's count there (`forwarded N commits to the old address <old url> (branch main, now at commit …)`), and after a take-in of a teammate's commits one more naming the new repo (`forwarded N commits to the new address <new url> …`) and leaves the old and new repos' `main` equal; the next push says nothing of the old address; Ben's next sync makes his config name the new address and his clone of the new repo equal to its `main`.
- The `lore: wrote …` / `lore: removed …` lines of a write name exactly the files its commits hold: never a row rewritten with the same bytes, and a folder's new `_meta.json` by name.

## Findings it guards

- `devco:D-684`, `devco:D-691` (REGRESSIONS.md, owner use pass 4 weak 2 and weak 8); `devco:D-699` (owner use pass 5 weak 5, the move matrix).
