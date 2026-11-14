package wire

// trackedwiring.go — which clone of a migrated project adds lore's wiring
// to the tracked .gitignore and .gitattributes (stories A-1).
//
// lore migrate run changes no tracked file but .lore/data (M-6), so the
// .gitignore lines and the .gitattributes block a project on the current
// format needs are added by the first write after the migration. When that
// first write ran in every teammate's clone, each of them added the same
// lines to the same tracked files without committing them; once the
// migrator committed theirs, every teammate's `git pull` aborted ("Your
// local changes ... would be overwritten by merge"), even though the bytes
// were the same. So in a migrated project one clone owns the tracked
// wiring:
//
//	_meta.json records no migration (a project lore 0.1.18 made, or one
//	   whose migration recorded nobody) ─▶ every write may add it, as before
//	migrated ─▶ this clone ran lore migrate run (git config --local
//	   lore.migratedhere true) ─▶ its writes add it; the migrator commits it
//	         ─▶ any other clone ─▶ the same rules go to the clone's own
//	            .git/info/exclude and .git/info/attributes (never committed);
//	            the tracked lines reach it with the migrator's commit
//
// An install step (lore init, lore doctor --fix, lore fix) is the person
// asking for the tracked wiring: it writes it in any clone.

import (
	"context"
	"strings"
)

// MigratedHereGitKey is the clone-local git config key lore migrate run
// sets in the clone it ran in. It is never committed or pushed, so it
// names exactly one clone: the one whose next commit is expected to carry
// the tracked wiring.
const MigratedHereGitKey = "lore.migratedhere"

// migratedHereOn is the key's value in the migrator's clone.
const migratedHereOn = "true"

// markMigratedHere records in the git config of the clone holding top that
// lore migrate run raised the project's format here. Best effort: without
// the mark the migrator's writes keep the rules local like a teammate's,
// and `lore doctor --fix` still adds the tracked lines.
func markMigratedHere(ctx context.Context, top string) {
	_, _ = gitRun{dir: top, args: []string{"config", "--local", MigratedHereGitKey, migratedHereOn}}.output(ctx) // best effort, see above
}

// TrackedWiringHere reports whether an ordinary write in the clone of the
// project at root may add lore's lines to the tracked .gitignore and
// .gitattributes (see the file comment). A _meta.json that cannot be read
// answers false: the local files change nothing a teammate pulls.
func TrackedWiringHere(ctx context.Context, root string) bool {
	pf, err := ProjectFormatAt(root)
	if err != nil {
		return false
	}
	if pf.MigratedBy == "" && pf.MigratedAt == "" {
		return true
	}
	out, err := gitRun{dir: root, args: []string{"config", "--local", "--get", MigratedHereGitKey}}.output(ctx)
	return err == nil && strings.TrimSpace(string(out)) == migratedHereOn
}
