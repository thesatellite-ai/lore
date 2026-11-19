package decision

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/content"
	kn "github.com/thesatellite-ai/lore/ops/internal/knowledge"
)

// shortRefRE is a team's own decision number as people write it in a
// title: D5, ADR-12, dec7 (letters, an optional hyphen, digits).
var shortRefRE = regexp.MustCompile(`^[A-Za-z]{1,8}-?[0-9]{1,6}$`)

// resolveRef returns the dec_ id that raw names. raw is a dec_ id, or a
// short ref such as D5 that opens one decision title ("D5: Which repos
// are client repos"), matched without case across every repo and the
// archived rows (devco D-449: `decision show --id D5` said "not a
// decision id" while D5 is how the team names it). A ref that opens no
// title, or several, is refused with the ids to pick from.
func (o ops) resolveRef(ctx context.Context, raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if _, err := ids.ParseAny(s); err == nil || !shortRefRE.MatchString(s) {
		return raw, nil
	}
	items, _, err := kn.List(ctx, o.env, noun, kn.Scope{All: true}, true, "", nil)
	if err != nil {
		return "", err
	}
	var hits []string
	for _, it := range items {
		if titleOpensWith(content.Field(it.Row, kn.FieldTitle), s) {
			hits = append(hits, it.Row.ID.String())
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", errs.New(errs.NotFound, "decision: no decision title starts with "+s).
			WithHint("lore decision list --json shows the ids; lore decision show --id dec_<id>")
	default:
		return "", errs.New(errs.AmbiguousID, "decision: "+s+" opens "+strconv.Itoa(len(hits))+" decision titles: "+strings.Join(hits, ", ")).
			WithHint("lore decision show --id <one of those dec_ ids>")
	}
}

// titleOpensWith: title starts with ref (any case) and the next character
// ends the ref (end, space or punctuation), so D5 never matches "D51".
func titleOpensWith(title, ref string) bool {
	t := strings.TrimSpace(title)
	if len(t) < len(ref) || !strings.EqualFold(t[:len(ref)], ref) {
		return false
	}
	if len(t) == len(ref) {
		return true
	}
	switch c := t[len(ref)]; {
	case c >= '0' && c <= '9', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '-', c == '_':
		return false
	default:
		return true
	}
}
