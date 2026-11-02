// Package stores reads and writes lore stores: separate lore-only git repos
// that hold internal, group, global and personal projects as folders
// (LORE_TEAM_SPEC §5, F11, F12, F13, ADRs TEAM-003, TEAM-014, TEAM-020).
//
// What lives here, and why each piece is shaped the way it is:
//
//   - One BARE clone per store URL at ~/.lore/clones/<host>/<owner>/<repo>.git
//     (§5.3). Bare, because lore never needs a checkout of a notebook and a
//     checkout is one more place internal text could be copied from. All
//     reads and writes go through gitref (no index, no work tree), so two org
//     entries on one URL (main and staging) share the clone safely.
//   - The store marker check (F12): every use of a store verifies that its
//     branch holds lore-store.yaml and nothing outside the allowed list, so
//     lore never commits into a code repo listed under stores: by mistake.
//   - Store project read/write: a store branch is the durable home of its
//     projects (fixed rule 9), so a write commits on the local store branch
//     at once and pushes it; offline, the commit waits in the clone and is
//     pushed by the next command (`lore store forget` refuses while any wait).
//   - The link index (F11): every `<folder>/.lore/data/_meta.json` "links"
//     entry of every non-staging store, scanned per store commit and cached
//     outside any checkout. It is the real orgresolve.LinkSource.
//
// Mechanism versus decision: this package knows how a store is laid out and
// how to read, write and check it. Which org a repo belongs to, which
// project a row goes to and whether a write may leak are decided elsewhere
// (orgresolve, rowroute, leakcheck) and passed in.
package stores
