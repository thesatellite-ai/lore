package leakcheck

// allowcmd.go — the one spelling of `lore secrets allow` for a row finding
// and the one command that trims actor copies, shared by every hint that
// names them (migrate, the commit and push checks, check leaks, lint).
//
// Why: the migrate warning printed `lore secrets allow <id> --field …`
// while the push refusal printed `lore secrets allow --row-id <id> --rule
// … --field …`; both worked, but two shapes of one fix read as two
// different fixes (user report). Every hint now builds its line here.

// Placeholders of the allow template (AllowRowTemplate).
const (
	allowPlaceRow   = "<row-id>"
	allowPlaceField = "<field>"
	allowPlaceRule  = "<rule>"
	allowPlaceHash  = "<the sha256:… match shown>"
	allowReason     = ` --reason "<why>"`
)

// AllowRowCommand is the `lore secrets allow` command that records one row
// finding as not a secret: row id, field, rule and match hash filled in
// (the sha256:… fingerprint a refusal prints, or the number `lore check
// leaks --rows` prints; the command takes both), the reason left to write.
func AllowRowCommand(rowID, field, rule, matchHash string) string {
	return "lore secrets allow --row-id " + rowID + " --field " + field + " --rule " + rule + " --match-hash " + matchHash + allowReason
}

// AllowRowTemplate is AllowRowCommand with every value a placeholder, for
// a hint that names no single finding.
func AllowRowTemplate() string {
	return AllowRowCommand(allowPlaceRow, allowPlaceField, allowPlaceRule, allowPlaceHash)
}

// ActorCopyFix is the command that trims every actor copy in a repo's
// .lore/data to the {id, display name, kind} projection (the actor
// projection step of `lore migrate run`; it is safe to run again on a repo
// already migrated). The client-repo guard and `lore check leaks --ci`
// name it under an actor finding.
const ActorCopyFix = "lore migrate run"

// ActorCopyExtraKeys lists the top-level keys of the actor row file b that
// a client repo's copy may not hold (the same test as the client-repo
// guard and CheckCI), sorted; nil when the copy is clean or b is not a
// JSON object. The migrate step deletes exactly these keys.
func ActorCopyExtraKeys(b []byte) []string {
	doc, ok := decodeObject(b)
	if !ok {
		return nil
	}
	return actorExtraKeys(doc)
}
