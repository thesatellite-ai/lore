package settings

// References inside metadata values (OV13b, later 508): `lore secrets
// list` and the bulk rewrites (move, relabel, rename-entry) read and
// rewrite the ${secret:…} references held in a row's metadata values too,
// so offboarding a vault entry never misses one kept in metadata.
//
//	metaUses(row)  ─▶ every meta entry's string leaves ─▶ refs.Parse ─▶ SecretUse (field meta.<name>)
//	metaRewrite(row) ─▶ rewriteText on each leaf ─▶ engine.MetaWrite JSON terms (key id = new v)
//
// A bare secret_ref value is never a metadata value: metadata has no
// secret_ref type (METADATA RX8; meta add refuses it, G-META-11), a
// ${secret:…} reference in a text value serves it. An unstored
// ${secret:unstored} in a metadata value is numbered after the row's field
// placeholders, so `lore secrets link --at` reaches it (SECRETS 7: --at is
// the position `secrets list --unstored` prints).

import (
	"maps"
	"slices"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// metaFieldPrefix names a metadata value in a SecretUse's Field.
const metaFieldPrefix = "meta."

// metaUses lists the ${secret:…} references in r's metadata values, keys
// in id order.
func metaUses(r row.Row, base SecretUse, unstored int) []SecretUse {
	var out []SecretUse
	for _, kid := range slices.SortedFunc(maps.Keys(r.Meta), func(a, b ids.ID[ids.MetaKey]) int { return compareText(a.String(), b.String()) }) {
		en, err := meta.DecodeEntry(r.Meta[kid])
		if err != nil {
			continue
		}
		for _, s := range jsonStrings(en.V) {
			for _, p := range refs.Parse(s) {
				if p.Kind != refs.PartRef || p.Ref.Scheme != refs.SchemeSecret || p.Ref.Problem != "" {
					continue
				}
				for i, t := range p.Ref.Alternates {
					u := useOf(base, metaFieldPrefix+en.Name, t)
					u.Alt, u.Kind = i, useText
					if t.Fixed == refs.TargetUnstored {
						unstored++
						u.At = unstored
					}
					out = append(out, u)
				}
			}
		}
	}
	return out
}

// metaRewrite applies fix to every metadata value of r; the write is nil
// when nothing changed. Each changed value is set back by its key id as a
// JSON term, so the entry keeps its key, label and scope.
func metaRewrite(r row.Row, fix rewriter) (engine.MetaWrite, int, error) {
	mw := engine.MetaWrite{ID: r.ID}
	total := 0
	for _, kid := range slices.SortedFunc(maps.Keys(r.Meta), func(a, b ids.ID[ids.MetaKey]) int { return compareText(a.String(), b.String()) }) {
		en, err := meta.DecodeEntry(r.Meta[kid])
		if err != nil {
			continue
		}
		j, err := value.JSONOf(en.V)
		if err != nil {
			continue
		}
		nv, n, err := jsonRewrite(j, fix)
		if err != nil {
			return engine.MetaWrite{}, 0, err
		}
		if n == 0 {
			continue
		}
		mw.JSON = append(mw.JSON, meta.Term{KeyID: kid, Value: string(nv.Raw())})
		total += n
	}
	return mw, total, nil
}

// compareText orders two strings.
func compareText(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
