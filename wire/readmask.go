package wire

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
)

// MaskedReads lists the rows this process masked on read because their
// stored text matched a secret rule (devco:R-14, engine readmask.go): rows
// that reached a store without lore's write scan. Nil when the App is not
// open.
func (a *App) MaskedReads(ctx context.Context) []engine.MaskedRead {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	st := a.st
	a.mu.Unlock()
	if st == nil || st.eng == nil {
		return nil
	}
	return st.eng.MaskedReads()
}

// WasMasked reports whether a read in this process masked row id; render
// keeps such a row out of every file an agent reads.
func (a *App) WasMasked(ctx context.Context, id ids.AnyID) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	st := a.st
	a.mu.Unlock()
	return st != nil && st.eng != nil && st.eng.WasMasked(id)
}

// MaskedReadLines is one warning line per masked row: the row, its store,
// its author, the fields and the rules (never the text), and what to do.
// "" when no row was masked.
func (a *App) MaskedReadLines(ctx context.Context) string {
	ms := a.MaskedReads(ctx)
	if len(ms) == 0 {
		return ""
	}
	name := a.ActorNamer(ctx)
	var b strings.Builder
	for _, m := range ms {
		by := "an unknown author"
		if !m.Author.IsZero() {
			by = m.Author.String()
			if name != nil {
				by = name(by)
			}
		}
		where := ""
		if m.Source != "" {
			where = " in " + string(m.Source)
		}
		verb := " matches"
		if len(m.Fields) > 1 {
			verb = " match"
		}
		b.WriteString("⚠ " + m.ID.String() + where + " (by " + by + "): " + strings.Join(m.Fields, ", ") +
			verb + " a secret rule (" + strings.Join(m.Rules, ", ") + "); shown redacted and left out of render. " +
			"Run `lore check leaks --rows` for the allow line, or move the value to your vault: lore edit --id " + m.ID.String() + "\n")
	}
	return b.String()
}
