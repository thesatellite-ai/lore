package host

// live_rewritten.go — the refusal of a write while the remote live ref was
// deleted or rewritten. It names the backup ref lore made of this clone's
// live rows (refs/lore-backup/live/<stamp>), which the person needs for
// the repair: the surface prints an error's message and hint, not its
// wrapped cause, so the name must be in the text (v0.1.17 printed it).

import (
	"strings"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
)

// backupKeptAs is the part of live.Ref's rewritten error naming the backup
// ("… (local copy kept as <ref>)", adapters/live ref.go rewritten).
const backupKeptAs = "local copy kept as "

// rewrittenError is code's refusal for blocked (a live.ErrRemoteRewritten
// chain), naming the backup ref when the cause carries it, else the
// backup namespace.
func rewrittenError(code errs.Code, blocked error) error {
	backup := backupRefOf(blocked)
	where := "kept as " + backup
	if backup == "" {
		where = "kept under " + live.RefBackupPrefix
	}
	return errs.Wrap(code, "the remote live ref was deleted or rewritten; lore stopped writing to it (your local copy is "+where+")", blocked).
		WithHint("run `lore live restore` to put your live rows back on the remote")
}

// backupRefOf is the backup ref named in err's text ("" when none).
func backupRefOf(err error) string {
	if err == nil {
		return ""
	}
	_, rest, ok := strings.Cut(err.Error(), backupKeptAs)
	if !ok {
		return ""
	}
	ref, _, _ := strings.Cut(rest, ")")
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, live.RefBackupPrefix) {
		return ""
	}
	return ref
}
