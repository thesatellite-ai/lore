// render_meta.go — metadata values on rendered rows (METADATA 3.14, MQ13,
// A-CTX-13).
//
// MQ13: agents see metadata values too, so every rendered row carries its
// meta as one "meta: name=value, …" line. Which entries show depends on
// where the text goes:
//
//	committed render (.lore/LORE.md and friends, may reach a client)
//	    ─► only keys that are neither internal nor sensitive; a key whose
//	       row is not found counts as internal (R2 item 4, fail closed)
//	private render (~/.lore/render/…, an internal context)
//	    ─► internal values too; sensitive values never (RQ1, OQ2)
//
// Values print in the same form `lore show` uses (meta.Show).
package main

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// renderMetaPrefix starts the metadata line of a rendered row.
const renderMetaPrefix = "meta: "

// renderMetaKeys caches the key rows a render looks up (key id → flags);
// one per render pass. found is false for a key id with no readable row.
type renderMetaKeys struct {
	keys map[ids.AnyID]renderMetaKey
}

type renderMetaKey struct {
	found, internal, sensitive bool
}

// newRenderMetaKeys starts an empty cache.
func newRenderMetaKeys() *renderMetaKeys {
	return &renderMetaKeys{keys: map[ids.AnyID]renderMetaKey{}}
}

// key reads one key row through the core (cached).
func (c *renderMetaKeys) key(ctx context.Context, kid ids.AnyID) renderMetaKey {
	if k, ok := c.keys[kid]; ok {
		return k
	}
	var k renderMetaKey
	if coreApp != nil {
		if _, r, err := coreApp.Get(ctx, engine.Ref{Table: schema.TableMetaKeys, ID: kid}); err == nil && !r.Deleted {
			k = renderMetaKey{found: true, internal: rowBool(r, metaKeyInternalField), sensitive: rowBool(r, metaKeySensitiveField)}
		}
	}
	c.keys[kid] = k
	return k
}

// line is r's metadata line ("" when nothing may show). internal is true
// only for the private render.
func (c *renderMetaKeys) line(ctx context.Context, r row.Row, internal bool) string {
	var parts []string
	for kid, j := range r.Meta {
		en, err := meta.DecodeEntry(j)
		if err != nil || en.Name == "" || en.Sensitive {
			continue
		}
		k := c.key(ctx, kid.Any())
		if k.sensitive || (!internal && (!k.found || k.internal)) {
			continue
		}
		parts = append(parts, en.Name+"="+oneLine(meta.Show(en)))
	}
	if len(parts) == 0 {
		return ""
	}
	slices.Sort(parts)
	return renderMetaPrefix + strings.Join(parts, ", ")
}

// withMetaLine appends r's metadata line to body on its own line.
func (c *renderMetaKeys) withMetaLine(ctx context.Context, body string, r row.Row, internal bool) string {
	l := c.line(ctx, r, internal)
	if l == "" {
		return body
	}
	if body == "" {
		return l
	}
	return body + "\n" + l
}
