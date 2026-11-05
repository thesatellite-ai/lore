package renderhost

// render_cut.go — the cut_summary of a render (CONTEXT_BUDGET 6.11):
// what `lore render` left out of the core of .lore/LORE.md and why, kept
// on the render history row so `lore why-context --cut` can show the last
// render's "moved out" list without re-rendering.
//
//	lore render ─► buildBudgetedFile → renderReport + pinSkips
//	            └► newCutSummary ─► render_histories.scope_summary.cut_summary
//	lore why-context --cut ─► read the newest row ─► ops/whycontext renders it
//
// DECISION FA-CTX-1: cut_summary is a key inside the existing
// scope_summary JSON text of the machine-local render_histories row, not a
// new column (render_histories stays on the old ent module, which gets no
// new generated code; the JSON already carries the render's breakdown).
// renderScopeSummary is the scope_summary JSON of a render history row.
// Cut is nil on rows written before 0.1.18 (no cut_summary key).
type renderScopeSummary struct {
	Included renderResult `json:"included"`
	Cut      *cutSummary  `json:"cut_summary,omitempty"`
}

// cutSummary is what one render moved out of the core of LORE.md.
type cutSummary struct {
	File    string `json:"file"`
	Profile string `json:"profile"`
	Budget  int    `json:"budget"`
	Before  int    `json:"before"`
	After   int    `json:"after"`
	Over    bool   `json:"budget_exceeded"`
	// Index is true when the topic index was collapsed to one line.
	Index bool `json:"index_collapsed"`
	// Entries: superseded and manual rows (left out), then must rules in
	// line form (budget or entry_max), each with its reason.
	Entries []reportEntry `json:"entries"`
}

// newCutSummary builds the cut summary from the render's report and the
// rows the pinned-set loader skipped.
func newCutSummary(r renderReport, skips pinSkips) cutSummary {
	c := cutSummary{File: r.File, Profile: r.Profile, Budget: r.Budget, Before: r.Before,
		After: r.After, Over: r.Over, Index: r.IndexFolded}
	c.Entries = append(c.Entries, skips.IDs...)
	c.Entries = append(c.Entries, r.Lines...)
	if c.Entries == nil {
		c.Entries = []reportEntry{}
	}
	return c
}
