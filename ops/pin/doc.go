// Package pin is `lore pin set <id> --context full|line` and `lore pin unset <id>`
// (CONTEXT_BUDGET 6.4, 6.6): a person or an agent fixes the form one must
// rule or critical/high hotfix takes in .lore/LORE.md.
//
//	pin.set   ─► kn.Show(id) ─► must rule / critical|high hotfix? ─► Update context_pin + context_pinned_at
//	pin.unset ─► kn.Show(id) ─► Update context_pin = null, context_pinned_at = null
//	render    ─► full pins: always in full, before every other must rule, by pin time
//	             line pins: always the line form
//
// DECISION FA-CTX-6: the pin is two synced fields on the row
// (context_pin, context_pinned_at), so every machine renders the same file
// (D3); pinning anything that is not in the core (a should rule, a medium
// hotfix, another kind) is refused with E_INVALID_INPUT. Agents may pin
// like anyone (AGENTS OVERRIDE, D8); each write is attributed.
package pin
