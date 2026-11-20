# Feature grid — what must hold where two features meet

Each line is one pair of features (and, at the end, the triples that matter most): what must hold when both are in play, and the test that replays it. "none" means no test walks both features together yet; a cell marked `GRID_…` is replayed by a test in `tests/stories/GRID_*_test.go` (one test per cell). A cell whose test fails is a finding in the bug-hunt list until its fixer lands the fix with the test. Run the grid's own cells with `go test -tags 'stories journeys' -run '^TestStoryGRID' ./tests/stories/`. A move leaves a tombstone where the row was (that is how the old place learns the row left), so "leaves no copy" in a cell means no open copy.

Features: **live** (live rows on the hidden live ref), **layers** (branch layers: notes that follow the code branch), **linked** (a repo linked to a store folder: with or without its own `.lore`, or cleaned after `--remove-source`), **guards** (client repo guards: leak checks, commit-msg and pre-push hooks), **hooks** (a hooks folder committed with the repo, `core.hooksPath`), **old** (lore 0.1.17 format data, `_v: 1`), **audience** (a store kept to admins or one roster team), **moves** (`lore row move` single, `lore move --from` bulk, `lore store adopt --move`), **org links** (`lore org link`: own vs client, the link index), **migrate** (`lore migrate run`).

## Pairs

| cell | what must hold | covered by |
|------|----------------|------------|
| live x layers | on a main-like branch a new task is live; on an isolated branch it is a file of the branch and never reaches the live ref; `--live` / `--no-layer` still share at once | `TestStoryBL10CodeRepoMode` |
| live x linked | a linked repo's live writes go to the folder's store, never to the code repo's live ref; doctor/fix on a linked repo keeps live rows | `TestJourneyLinkedRepoDoctorCleansRows`, `TestStoryOLinkedRepoWritesToItsFolder` |
| live x guards | an open live row in a client repo is never shown, landed or committed into the client repo; the commit check sees only the repo's own rows | `TestJourneyFoldersAdopt` (client steps), `TestStoryBL15InstallAction` |
| live x hooks | live writes in a repo with a tracked hooks folder never write into that folder; the commit stays quiet | `TestStoryOAdoptMoveOldFormat` (tracked hooks + live row) |
| live x old | a 0.1.17 repo's live rows are read (never rewritten to the new format) until `lore migrate run`; migrate pushes nothing to the live ref | `TestJourneyMigrateWritesNothingToTheRemote` |
| live x audience | a live row of a folder in an admins-only or team store is never pushed to, or shown from, a store the reader cannot reach | none |
| live x moves (single) | `lore row move` of an open live task takes the live copy and leaves only a tombstone on the live ref; a deleted live row cannot be moved | `TestStoryGRIDLiveSingleMove` |
| live x moves (bulk) | `lore move --from repo` takes the live copy of every open live task; a deleted live row is never moved | `TestStoryGRIDLiveBulkMove` |
| live x moves (adopt --move) | adopt --move copies open live rows once and takes them off the repo's live ref; deleted, trashed or purged live rows are never adopted | `TestJourneyAdoptSkipsGoneLiveRows`, `TestStoryOAdoptMoveOneCommand` |
| live x org links | after `lore org link` a repo's open live rows stay readable (in the repo or the folder), never shown twice | `TestJourneyMoveOutOfLinkedRepo` |
| live x migrate | `lore migrate run` keeps open 0.1.17 live rows readable and pushes nothing | `TestJourneyMigrateWritesNothingToTheRemote`, `TestStoryEMixedFormats` |
| layers x linked | a linked repo's branch notes go to the folder's branch layer; a cleaned linked repo still reads and writes branch notes | `TestJourneyLinkedRepoDoctorCleansRows` (branch mode step), story BL 1-9 |
| layers x guards | in a client repo the CI workflow is never offered and `install-action` is refused; branch notes never land in the client repo | `TestStoryBL15InstallAction` |
| layers x hooks | branch notes in a repo whose hooks folder is tracked never write into that folder; committing on the branch works | `TestStoryGRIDLayersTrackedHooks` |
| layers x old | a write on a feature branch of a 0.1.17 repo is refused (format first), writes no new-format file and no branch layer | `TestStoryGRIDLayersOldFormat` |
| layers x audience | branch notes of a folder in a team-kept store are pushed only to that store | none |
| layers x moves (single) | `lore row move` of a branch-layer note moves it out of the branch's notes (the branch keeps a tombstone file); the note is read once, from the target | `TestStoryGRIDLayersSingleMove` |
| layers x moves (bulk) | a bulk move run on a feature branch moves only what it lists and never leaves a branch note in two places | `TestStoryGRIDLayersBulkMove` |
| layers x moves (adopt) | `lore branch adopt` moves a shared note into the branch's notes; landing brings it back | `TestStoryBL14Adopt` |
| layers x org links | linking a repo keeps its branch mode and its open branch notes | `TestStoryOOwnerShape` |
| layers x migrate | migrating on main never drops notes of an unpushed branch | `TestStoryEMixedFormats` (branch crosses the migration) |
| linked x guards | a client repo linked to a folder keeps its guards (commit-msg and pre-push hooks), `whoami` says guards on | `TestJourneyClientRepoPrePushGuard`, `TestJourneyAdminOnlyStore` |
| linked x hooks | linking a repo with a tracked hooks folder never writes that folder; doctor prints lines that run as printed | `TestJourneyClientRepoPrePushGuard` (tracked-hooks step) |
| linked x old | a 0.1.17 repo linked to a folder: adopt reads the old rows; the repo stays on its format until migrate | `TestJourneyFoldersAdopt` |
| linked x audience | no repo can be linked to an admins-only store; a member's linked repo never needs it | `TestJourneyAdminOnlyStore` |
| linked x moves | a move out of a linked repo commits through the hooks and leaves nothing; a teammate reads every row once | `TestJourneyMoveOutOfLinkedRepo` |
| linked x org links | a linked repo with no `.lore` writes to its folder; with its own `.lore` `--to repo` still writes the repo | `TestStoryOLinkedRepoWritesToItsFolder` |
| linked x migrate | migrate in a linked repo waits for the store and writes nothing to the code repo's remote | `TestJourneyMigrateNothingPushesNoWaitingStoreCommit` |
| guards x hooks | a client repo with a tracked hooks folder: the pre-push guard goes to git's hooks only, never the tracked folder | `TestJourneyClientRepoPrePushGuard`, `TestStoryOClientRepoPrePushGuard` |
| guards x old | the leak check of a client repo on 0.1.17 data still refuses internal notes | `TestJourneyFoldersAdopt` (client), `TestStoryFClientRepo` |
| guards x audience | an admins-only store never makes a member's repo "unknown"; client guards stay on for both roles | `TestJourneyAdminOnlyStore` (ann-client-repo-guards-work) |
| guards x moves | a move into a client repo refuses internal metadata unless `--drop-internal` | `TestJourneyMoveOutOfLinkedRepo`, `TestStoryOOwnerShape` (move next) |
| guards x org links | a repo matched by `repos` is a client repo even when `own` overlaps; the same for admin and member | `TestJourneyAgencyClientMoves` |
| guards x migrate | migrate in a client repo keeps the guards and never commits internal notes | `TestStoryMRMigrateRealRepos` |
| hooks x old | a 0.1.17 repo with a tracked hooks folder: lore never rewrites the folder before migrate | `TestStoryOAdoptMoveOldFormat` |
| hooks x audience | (no interaction: audiences decide which stores are cloned; hooks are per repo) | n/a |
| hooks x moves | a single or bulk move in a repo with a tracked hooks folder never writes that folder | `TestStoryGRIDHooksBulkMove` |
| hooks x org links | `lore org link` in a repo with a tracked hooks folder never writes it; `lore fix --tracked-hooks` is the only way in | `TestJourneyClientRepoPrePushGuard`, `TestStoryOTrackedHooksNameTheFlag` |
| hooks x migrate | `lore migrate run` in a repo with a tracked hooks folder leaves that folder byte-identical | `TestStoryGRIDHooksMigrate` |
| old x audience | (no interaction: audience is a 0.1.18 store key; 0.1.17 has no stores) | n/a |
| old x moves | a move in a 0.1.17 repo is refused before migrate or adopts the old rows as they are (adopt --move) | `TestStoryOAdoptMoveOldFormat` |
| old x org links | linking a 0.1.17 repo keeps its format; adopt reads the old rows | `TestJourneyFoldersAdopt` |
| old x migrate | migrate converts every row, keeps ids, the dry run count matches | `TestJourneyMigrateCountMatchesDryRun`, `TestStoryEMixedFormats` |
| audience x moves | a move into a team-kept store's folder by a member outside the team is refused naming the audience; nothing is written | none |
| audience x org links | a repo can never be linked to an admins-only store (`--audience everyone` named) | `TestJourneyAdminOnlyStore` |
| audience x migrate | (no interaction: migrate works on a repo's own data) | n/a |
| moves x org links | after adopt --move the repo is linked to the folder; a bare id names the folder's copy only | `TestStoryOAdoptMoveOneCommand` |
| moves x migrate | a repo migrated with `lore migrate run` can then adopt --move its notes in one command | none |
| org links x migrate | migrate never drops the org link or the linked folder's reads | none (`TestJourneyMixedVersionsOneStore` walks both, asserts neither) |

## Triples

| cell | what must hold | covered by |
|------|----------------|------------|
| live x moves x linked | a linked repo's open live task moved to another folder leaves no copy on either live ref | `TestJourneyMoveOutOfLinkedRepo` |
| old x adopt --move x live | an old-format repo with an open live row moves in one command; the row is adopted once | `TestStoryOAdoptMoveOldFormat` |
| layers x moves x linked | a branch note of a linked repo moved to another folder leaves the branch's notes | none |
| guards x moves x org links | a move into a client repo linked to a folder strips or refuses internal metadata; the commit check passes | `TestStoryOOwnerShape` |
| hooks x guards x linked | a client repo with a tracked hooks folder, linked: the guard is in git's hooks, the tracked folder is untouched | `TestJourneyClientRepoPrePushGuard` |
