# WR — the leftovers of story WQ

**Who.** Ada (made-up names) is the admin of the org `acme`; her team store is `github.com/acme-co/lore-team` and her repo `~/code/platform` is in the setup's folders. A second repo `~/code/linked` is linked to the org folder itself.

**Why.** Story WQ found two things it did not fix: a repo linked with `--project org` made a coding agent's write in another repo end E_INTERNAL from the cache refresh, and success lines that name a roster still said "in acme org/" beside the refusals' "the roster of acme (acme/org)".

**Test.** `TestStoryWRUp3Polish3` (tests/stories/WR_up3polish3_test.go), about 15 s.

## Steps

1. Ada starts the team in `~/code/platform`.
2. From the home folder: a resource kind `server` with a field `url` and a resource `web1` in `acme/org`. In `~/code/linked`: `lore org link --org acme --project org --yes`. Ada's coding agent (a Claude Code session) runs `lore memory add --to acme/org` in `platform`, a second one in `linked`, then Ada herself; `lore memory list --store acme` in `linked` lists all of them.
3. `lore actor register --setup acme-admin` from the home folder; in `platform`: `lore actor contact add --actor <me> --type github --value ada-second --yes`, `lore actor edit --actor <me> --timezone Europe/Berlin --yes`, `lore role add --name design_lead --yes`, `lore contact-type add --name codeberg --kind handle --yes`; a task added and claimed with `LORE_ACTOR=agent:wr-nobody` (an agent actor no roster row names).

## What must hold

- No write ends E_INTERNAL: a second source at the same cache file shares its handle, and a cache handle whose typed tables another handle or process changed rebuilds its list inside the write.
- Every success line names the roster `in the roster of acme (acme/org)`, never `in acme org/`; the unregistered agent's claim is refused as `not on the roster of acme (acme/org)`.

## Findings it guards

- `devco:D-667`, `devco:D-668` (REGRESSIONS.md, story WQ leftovers).
