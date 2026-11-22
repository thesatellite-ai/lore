package host

// live_rewritten.go — the refusal of a write while the remote live ref was
// deleted or rewritten. It names the backup ref lore made of this clone's
// live rows (refs/lore-backup/live/<stamp>), which the person needs for
// the repair: the surface prints an error's message and hint, not its
// wrapped cause, so the name must be in the text (v0.1.17 printed it).

import (
	"errors"
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
	if errors.Is(blocked, live.ErrRemoteGoneURLUnknown) {
		return errs.Wrap(code, liveGoneURLUnknownText+"; lore stopped writing live rows to it (your local copy is "+where+")", blocked).
			WithHint(liveGoneHint)
	}
	if errors.Is(blocked, live.ErrRemoteLiveGone) {
		return errs.Wrap(code, liveGoneText+"; lore stopped writing live rows to it (your local copy is "+where+")", blocked).
			WithHint(liveGoneHint)
	}
	return errs.Wrap(code, "the remote live ref was deleted or rewritten; lore stopped writing to it (your local copy is "+where+")", blocked).
		WithHint("run `lore live restore` to put your live rows back on the remote")
}

// liveGoneText, liveGoneURLUnknownText and liveGoneHint: the remote has
// no live ref any more while this clone saw one (#136, devco D-913). It is
// a new repository at the same url (an old repo renamed away, a fresh one
// made under its name: often about to be public) or the ref was deleted
// there; lore cannot tell which, so it pushes nothing on its own. Only
// live writes stop. The hint names the choice that publishes nothing
// first, and says plainly that restore publishes every live row (G-1: a
// repo moved to a new url restores to record it).
const (
	liveGoneText           = "the remote has no live ref any more: it is a new repository at the same url, or the ref was deleted there"
	liveGoneURLUnknownText = "the remote has no live ref: the repo moved to a new remote url, or the ref was deleted there (this clone's last sync did not record the remote url, so lore cannot tell which)"
	liveGoneHint           = "to keep your old live rows off that remote, run `lore live restore --fresh` (pushes nothing: they stay in a local backup ref and the next live write starts a new live ref); to publish them there, run `lore live restore`. Writes of rows that are not live go on meanwhile"
)

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
