package git

// backupprune.go — backups are bounded (plain files sweep 3.2,
// 2026-11-12: "lore backup refs kept with a bound"). A migration's backup
// (refs/lore-backup/migrate/…) and a compaction's backup of the old store
// history (refs/lore/backup/compact-…) are local refs that used to pile up
// forever; after each new one only the newest few of its kind stay (the
// live ref's backups already keep live.BackupsKept). `lore where` lists
// them, `git update-ref -d <ref>` drops one by hand.

import (
	"context"
	"strings"
)

// Backups kept per kind.
const (
	// MigrateBackupsKept: the newest migration backups (a rollback undoes
	// the last migration, so a few are plenty).
	MigrateBackupsKept = 5
	// CompactBackupsKept: the newest compaction backups of a store clone.
	CompactBackupsKept = 3
)

// compactBackupPrefix starts a compaction backup ref.
const compactBackupPrefix = backupRefNS + "compact-"

// pruneRefs deletes every ref under prefix in the repository at dir but
// the newest keep (by the date of the commit they point at). Best effort:
// a ref that cannot be deleted stays, and the next prune tries again.
func pruneRefs(ctx context.Context, g runner, dir, prefix string, keep int) {
	pattern := prefix
	if !strings.HasSuffix(pattern, "/") {
		pattern += "*" // for-each-ref matches whole path segments; a name prefix needs a glob
	}
	out, err := g.run(ctx, call{dir: dir, args: []string{"for-each-ref", "--sort=-committerdate", "--format=%(refname)", pattern}})
	if err != nil {
		return
	}
	refs := strings.Fields(string(out))
	for i := keep; i < len(refs); i++ {
		_, _ = g.run(ctx, call{dir: dir, args: []string{"update-ref", "-d", refs[i]}}) // best effort, see above
	}
}
