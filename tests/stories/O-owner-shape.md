# O — the owner-shaped machine

**Who.** Ada (made-up names) runs the org `acme` as its admin and keeps her own projects in a second org `kite`, on one laptop. Mo joins `acme` as a member from a laptop shaped the same way.

**Why.** Store and setup bugs kept showing up on the owner's own machine after every story passed, because the stories ran on a plain one-identity machine that talks to github.com directly. This story runs on a machine laid out like the owner's, so those bugs show up in the gate first.

**Fixture.** `tests/stories/ownershape.go`; every store or setup story can use it (`s.OwnerShaped(OwnerShape{…})`):

- `~/.gitconfig` holds the personal identity; `[includeIf "gitdir:~/code/<org>/"]` gives every repo under the org folder the org identity (`OwnerMachine.GitUser` reads what git resolves in a folder).
- `~/.ssh/config` has `Host github-<org>` / `HostName github.com` with the org account's key, and `url."git@github-<org>:<owner>/".insteadOf` rewrites the org's GitHub owners (ssh and https forms) to it.
- The fake ssh carries git over the alias (`git-upload-pack`, `git-receive-pack`) to the same bare repos a plain url reaches, so a push through the alias lands on the fake GitHub.
- `s.MoveRemote` is a GitHub transfer: the bare repo moves to another owner (optionally leaving a stale copy).
- `GitignoredLore` and `ClaudeLinkOut` make a repo that ignores `.lore/` and one whose `CLAUDE.md` is a link out of the repo.

**Test.** `TestStoryOOwnerShape` (tests/stories/O_owner_shape_test.go), about 60 s.

## Steps

1. Ada clones her org repo into `~/code/acme/platform` and a personal one into `~/code/kite/kite-app`; git resolves the org identity in the first and the personal one in the second.
2. She saves the admin setup `acme-admin` (org identity, the org account through the alias, team store `acme-co/lore-team`) and a second admin setup `kite-home` (personal identity, plain github.com) for the org `kite`; she adds the admins-only store `lore-ops` (`--audience admins`) and the client pattern `github.com/acme-co/client-*`.
3. She adds a folder `web` with a memory and a task, links `web`, links the client repo `client-shop` (matched by the prefix pattern), inits and renders the repo that ignores `.lore/` and the one whose `CLAUDE.md` links to `~/notes/CLAUDE.md`, adds a folder and a row to `lore-ops`, and syncs.
4. She reads from every place (home, each repo, the store clone): nothing is pushed.
5. GitHub moves `lore-team` and `lore-ops` to `acme-labs`; she runs `lore store set-url` for each from `$HOME`, writes a row and runs `lore store push --store acme` from `$HOME`.
6. Mo joins with the invite as `acme-member` (org identity, alias), reads in his clone and from `$HOME`, writes, syncs.
7. A second admins-only store nobody wrote to (`lore-vault`) moves; Ada runs set-url for it in her org repo.

## What must hold

- `setup list` and `doctor --all` treat the includeIf org identity as right: no `identity.git_email` warning and never a `--global` git command.
- Every org store's clone has the org identity as its git user, and every commit on it is authored so (the `kite` store: the personal identity); set-url says the git user it actually set.
- A client repo and the repo that ignores `.lore/` keep their tracked files and `git status`; the file `CLAUDE.md` links to is never written, and the link stays a link.
- Reads push nothing; only sync and store push move a remote.
- Each store set-url moves its clone to the new owner's folder; `store push` takes the org's name from `$HOME` and its "sent N" is the commits that reached the remote.
- A member's machine never clones the admins-only stores.
- Every note names the store it wrote to.
- Findings another branch fixes are logged as waiting with their id until the fix lands (`oWaiting`).

## Findings it guards

- `devco:D-531` (REGRESSIONS.md, `TestStoryOAliasWhoami`): `whoami` in a repo cloned as `git@<alias>:<owner>/<repo>.git` showed the remote as `<alias>/<owner>/<repo>`, matched no org, fell back to the personal identity and, with `--setup`, offered the alias pattern for the shared own list; it now shows `github.com/<owner>/<repo>`, the org match and the org identity, and never names the alias.
- `devco:D-530` (REGRESSIONS.md): `setup list` warned `identity.git_email` for a personal global email with an includeIf org identity and advised `git config --global user.email <org email>`; `setup list` and `doctor --all` from `$HOME` now show no such warning and never advise `--global`.
- `devco:D-532` (REGRESSIONS.md, `TestStoryOAliasWhoami`): `lore org link --remote git@<alias>:<owner>/<repo>.git` (the alias spelling the clone's origin holds) was refused as not a remote of this repo; it now links the repo as `github.com/<owner>/<repo>`. Branch notes of an alias clone are keyed like a github.com clone's, and a link row or branch notes an older lore keyed by the alias spelling are still found (unit tests).
- `devco:D-533` (REGRESSIONS.md, `TestStoryOIdentityByRemote`): the org identity given by `[includeIf "hasconfig:remote.*.url:git@<alias>:<owner>/**"]` blocks with a relative path, and a setup with no folders: `setup list` and `doctor --all` from `$HOME` never warn `identity.git_email` nor advise `--global` (the gitdir: shape above did not catch the old check).
- `devco:D-542` (REGRESSIONS.md): `store folder add lore-ops servers` said "committed locally to store acme"; the note now names the store written.
- `devco:D-516`, `devco:D-517` (REGRESSIONS.md): `store push --store acme` from `$HOME` refused, and push printed "sent 0" after 2 commits; the story pushes by the org's name and checks the count.
- `devco:D-514`, `devco:D-515` (REGRESSIONS.md): set-url set the moved clone's git user to a non-org identity and left the old clone beside the new one (for both stores); the story checks the moved clone's place and git user.
- `devco:D-561`, `devco:D-562`, `devco:D-564`, `devco:D-565`, `devco:D-567`, `devco:D-569` (REGRESSIONS.md): a home-folder sync claimed a push that sent nothing and never said which setup it covered; an admin's set-url rewrote the shared org file unasked (now `E_NEEDS_CONFIRM` until `--yes`, a local path called out), miscounted unpushed commits, left a net-zero pair of commits and an empty `stores/_local/`; whoami showed `_local/<path>`; one place had two labels; commit subjects had two forms. The story's set-url steps pass `--yes`; the owner-shaped home sync and set-url round trip run in TestJourneyHomeSyncAndStoreMoveSayWhatHappened.
- Waiting: `devco:D-540` (set-url cannot name the org's second store from `$HOME`; store show prints no sto_ id), `devco:D-541` (set-url on an empty admins-only store hints `--name acme` with that store's url).
- `devco:D-571`..`devco:D-574` (REGRESSIONS.md): owner use pass polish: store show of a named store prints its clone and folders, lists align and count, the private budget warning names its fix commands once per change, and context --for-file matches whole paths.
