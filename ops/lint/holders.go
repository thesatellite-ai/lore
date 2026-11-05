package lint

import (
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// StaleHolderLabel is a ${secret:…} reference whose machine holder's label
// no longer matches that machine's current label (SECRETS 5.2: labels go
// stale, ids do not; `lore secrets relabel` fixes them in one batch).
const StaleHolderLabel Code = "stale_holder_label"

// fieldActorHandle is the actors table's handle field (schema.Builtin).
const fieldActorHandle = "handle"

// holderLabelSep separates the person handle from the machine label in a
// holder label ("mira.studio_mac", SECRETS 5.2).
const holderLabelSep = "."

// holderFindings checks every secret reference with a machine holder:
// the label's machine part (after the handle) must equal the machine
// row's label; an actor holder's label must equal the actor's handle
// (OV13b). A holder lore has no row for and a reference without a label
// are not checked (nothing to compare).
//
//	machines rows → id/machine_id → label
//	every text field → refs.References → secret alternates → holder+label → compare
func holderFindings(all map[row.Table][]sourced) []Finding {
	labels := map[string]string{}
	for _, m := range all[schema.TableMachines] {
		l := text(m.r, schema.FieldMachineLabel)
		if l == "" {
			continue
		}
		labels[m.r.ID.String()] = l
		if mid := text(m.r, schema.FieldMachineID); mid != "" {
			labels[mid] = l
		}
	}
	handles := map[string]string{} // act_ id → handle (an actor holder's label, 5.2)
	for _, a := range all[schema.TableActors] {
		if h := text(a.r, fieldActorHandle); h != "" {
			handles[a.r.ID.String()] = h
		}
	}
	if len(labels) == 0 && len(handles) == 0 {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	for _, t := range slices.Sorted(maps.Keys(all)) {
		for _, s := range all[t] {
			for _, f := range slices.Sorted(maps.Keys(s.r.Fields)) {
				for _, txt := range texts(s.r.Fields[f]) {
					for _, r := range refs.References(txt) {
						if r.Scheme != refs.SchemeSecret {
							continue
						}
						for _, alt := range r.Alternates {
							holder, label := option(alt, refs.OptHolder), option(alt, refs.OptLabel)
							want, known := labels[holder]
							machine := label
							if hd, isActor := handles[holder]; isActor {
								want, known = hd, true // an actor holder's label is the handle itself
							} else if _, after, ok := strings.Cut(label, holderLabelSep); ok {
								machine = after
							}
							if holder == "" || label == "" || !known {
								continue
							}
							key := s.r.ID.String() + "\x00" + holder + "\x00" + label
							if machine == want || seen[key] {
								continue
							}
							seen[key] = true
							out = append(out, Finding{Code: StaleHolderLabel, ID: s.r.ID.String(), Store: string(s.src),
								Message: f + ": a secret reference names holder " + holder + " with label " + label + ", but its current label is " + want,
								Fix:     "lore secrets relabel --holder " + holder})
						}
					}
				}
			}
		}
	}
	return out
}

// option is a secret target's option value ("" when absent).
func option(t refs.SecretTarget, name string) string {
	for _, o := range t.Options {
		if o.Name == name {
			return o.Value
		}
	}
	return ""
}
