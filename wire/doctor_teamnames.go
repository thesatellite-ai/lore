package wire

// doctor_teamnames.go — how doctor's "Team versions" notes name a writer
// and a save (CLI_REFERENCE 3.7): the person's handle (else display name,
// else the actor id as stamped) and the save's UTC date, not the raw act_
// id and a nanosecond timestamp.

import (
	"context"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// unknownWriter names a save whose row records no writer (rows lore itself
// seeds at init, such as policies and repos, carry no created_by).
const unknownWriter = "(no writer recorded)"

// teamActorNamer returns the writer-name function of one doctor run: each
// actor id is looked up once in the attached stores' actors rows. A store
// that cannot be read, or an id no store holds, shows the id itself.
func (a *App) teamActorNamer(ctx context.Context) func(string) string {
	st, err := a.state(ctx)
	seen := map[string]string{}
	return func(actor string) string {
		if actor == "" {
			return unknownWriter
		}
		if n, ok := seen[actor]; ok {
			return n
		}
		n := actor
		if err == nil {
			if id, perr := ids.Parse[ids.Actor](actor); perr == nil {
				if r, ok, gerr := actorRowElsewhere(ctx, st, "", id); gerr == nil && ok {
					n = actorDisplayName(r, actor)
				}
			}
		}
		seen[actor] = n
		return n
	}
}

// actorDisplayName is the handle of actors row r, else its display name,
// else fallback.
func actorDisplayName(r row.Row, fallback string) string {
	for _, k := range []string{actorFieldHandle, actorFieldDisplay} {
		if v := r.Fields[k]; v != nil {
			if s := v.String(); s != "" {
				return s
			}
		}
	}
	return fallback
}

// saveDate is a `_w` save time (RFC 3339) as its UTC date; text that is
// not a time is shown as it is.
func saveDate(at string) string {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return at
	}
	return t.UTC().Format(time.DateOnly)
}
