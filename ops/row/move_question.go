package row

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/store"
)

// Classer is the optional Env upgrade the move question reads the
// target's F15 class through (wire.App.StoreClass); without it the
// question names the target only.
type Classer interface {
	StoreClass(ctx context.Context, name store.Source) (direction.Class, error)
}

// moveSensitiveMask replaces a sensitive metadata value in the question.
const moveSensitiveMask = "•••"

// moveQuestion is row.move's confirm question (CLI_REFERENCE 2.7): the
// preview a person at a terminal and an agent reading E_NEEDS_CONFIRM both
// see: where the row goes and who will see it, the row (title, id kept,
// where it is now), every metadata `name: value` that travels (a
// sensitive value masked), and, for a client repo, what stays back. A bad
// id or a missing row is reported here, before anything is asked.
func (o ops) moveQuestion(ctx context.Context, in MoveIn) (string, error) {
	id, err := ids.ParseAny(strings.TrimSpace(in.ID))
	if err != nil {
		return "", lookup.BadID(strings.TrimSpace(in.ID)+" is not a row id", err)
	}
	to := strings.TrimSpace(in.To)
	if to == "" {
		return "", nil // the move itself refuses a missing --to
	}
	src, r, err := o.env.Get(ctx, engine.Ref{ID: id})
	if err != nil {
		return "", err
	}
	def, _ := o.env.TableSchema(ctx, r.Table)
	class := direction.Class("")
	if c, ok := o.env.(Classer); ok {
		if cl, cerr := c.StoreClass(ctx, store.Source(to)); cerr == nil {
			class = cl
		}
	}
	var b strings.Builder
	b.WriteString("This becomes visible to everyone with access to " + to)
	if class != "" {
		b.WriteString(" (" + string(class) + ")")
	}
	b.WriteString(":\n  " + strconv.Quote(opstrash.Title(r, def, true)) + " (" + id.String() + ", id kept), now in " + string(src) + "\n")
	var lines, keys []string
	for kid, j := range r.Meta {
		en, derr := meta.DecodeEntry(j)
		if derr != nil {
			continue
		}
		v := string(en.V)
		if en.Sensitive {
			v = moveSensitiveMask
		}
		lines = append(lines, "    "+en.Name+": "+v)
		keys = append(keys, o.keyLine(ctx, kid.String(), en.Name, to))
	}
	if len(lines) > 0 {
		slices.Sort(lines)
		slices.Sort(keys)
		b.WriteString("  metadata that travels:\n" + strings.Join(lines, "\n") + "\n")
		b.WriteString("  keys in " + to + ":\n" + strings.Join(keys, "\n") + "\n")
	}
	if class == direction.Client {
		b.WriteString("  kept back: internal metadata, internal and sensitive resource fields, time entries and claim state (listed after the move)\n")
		if in.DropInternal {
			b.WriteString("  --drop-internal: internal metadata values are removed\n")
		}
	}
	if in.WithChildren {
		b.WriteString("  --with-children: its sub-tasks move with it, each keeping its id\n")
	}
	b.WriteString("Move it?")
	return b.String(), nil
}

// keyLine is one "keys in <target>" line of the move question: the key
// row is reused when it already lives in the target; otherwise it stays
// where it is and the moved value keeps pointing at it (a move creates no
// key row: METADATA 3.11 closed by OV17-3, G-ENGINE-5), which a client
// repo refuses at the write (E_META_FOREIGN_KEY, FA-META-7).
func (o ops) keyLine(ctx context.Context, keyID, name, to string) string {
	id, err := ids.ParseAny(keyID)
	if err != nil {
		return "    " + name + "  key " + keyID + " (unreadable id)"
	}
	src, k, err := o.env.Get(ctx, engine.Ref{ID: id})
	if err != nil {
		return "    " + name + "  key " + keyID + " not found in any attached store (lore meta rehome fixes it)"
	}
	typ := ""
	if v, ok := k.Fields[metaKeyTypeField]; ok && v != nil {
		typ = " (" + v.String() + ")"
	}
	if string(src) == to {
		return "    " + name + "  reused key " + keyID + typ
	}
	return "    " + name + "  key " + keyID + typ + " stays in " + string(src) + " (no new key is created)"
}

// metaKeyTypeField is the meta_keys field holding a key's type.
const metaKeyTypeField = "type"
