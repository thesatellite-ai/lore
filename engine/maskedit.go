package engine

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// An edit made from a masked read (devco:D-717). Every read masks a stored
// match no allow entry accepts (R-14): show, `lore row markdown`, the
// editor view of `lore edit`, the page. A person who saves such a view
// with a change elsewhere in the same field sent the field back with
// "[redacted <rule>]" where the key was, and the update stored that text
// over the key (the row file and its git history lost the value and kept
// the mask). Update now reads each patched text field against the mask
// lore prints for it: every mask token still there exactly as printed
// stands for the stored value and is put back (the write scan does not
// ask about it again: it is not new input); a token removed is a value
// removed; a token changed, moved out of order or ambiguous is refused
// with the way to change a secret (replace the whole token with the new
// value and answer it with the --secret-* flags).

// maskedSpan is one stored value the read mask replaced in a text field:
// the token printed for it and the findings it stood for.
type maskedSpan struct {
	token  string
	stored string
	found  []scan.Finding
}

// keptSpan is a stored value an update put back over its mask: where it
// sits in the new field text and the findings it stood for, so detect
// does not ask about it as new input.
type keptSpan struct {
	at, end int
	found   []scan.Finding
}

// keptSpansKey carries the kept spans of one Update into its detect.
type keptSpansKey struct{}

// withKeptSpans returns ctx carrying the kept spans by field.
func withKeptSpans(ctx context.Context, kept map[string][]keptSpan) context.Context {
	if len(kept) == 0 {
		return ctx
	}
	return context.WithValue(ctx, keptSpansKey{}, kept)
}

// keptFinding reports whether f (in field, item -1) is the stored value of
// a span the update put back unchanged: its match lies inside the span
// (the write may scan with more rules than the read mask), or it overlaps
// the span with the rule and match hash of a finding the span was masked
// for. A match reaching into new text with another hash is new input.
func keptFinding(ctx context.Context, field string, f scan.Finding) bool {
	kept, _ := ctx.Value(keptSpansKey{}).(map[string][]keptSpan)
	for _, k := range kept[field] {
		if f.Offset < 0 || f.Offset >= k.end || f.Offset+f.Length <= k.at {
			continue
		}
		if f.Offset >= k.at && f.Offset+f.Length <= k.end {
			return true
		}
		for _, g := range k.found {
			if g.RuleID == f.RuleID && g.MatchHash == f.MatchHash {
				return true
			}
		}
	}
	return false
}

// keepMaskedSpans rewrites patch so no mask lore printed for cur is ever
// stored over the value it stands for (see the note above). It returns
// the patch to write and the kept spans by field for detect.
func (e *Engine) keepMaskedSpans(ctx context.Context, src store.Source, cur row.Row, patch map[string]value.Value) (map[string]value.Value, map[string][]keptSpan, error) {
	if e.readScanner == nil || rawRead(ctx) || readMaskSkip[cur.Table] || len(patch) == 0 {
		return patch, nil, nil
	}
	var (
		allows    []scan.Allow
		allowsErr error
		loaded    bool
		out       map[string]value.Value
		kept      map[string][]keptSpan
	)
	loreRec := e.loreRecordFields(cur.ID)
	names := make([]string, 0, len(patch))
	for name := range patch {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		stored, held := cur.Fields[name]
		nv := patch[name]
		if !held || stored == nil || nv == nil {
			continue
		}
		var hits []*textJob
		for _, j := range scanJobs(map[string]value.Value{name: stored}, []string{name}) {
			for _, f := range e.scanRead(j.field, j.text) {
				if f.RuleID == scan.RuleSessionID && loreRec[j.field] {
					continue
				}
				j.found = append(j.found, f)
			}
			if len(j.found) > 0 {
				hits = append(hits, j)
			}
		}
		if len(hits) == 0 {
			continue
		}
		if !loaded {
			loaded = true
			if allows, allowsErr = e.settings.Allows(ctx, src); allowsErr != nil {
				allows = nil // as the read: fail closed, every match was masked
			}
		}
		var jobs []*textJob
		for _, j := range hits {
			if j.found = scan.Filter(cur.ID, j.found, allows); len(j.found) > 0 {
				jobs = append(jobs, j)
			}
		}
		if len(jobs) == 0 {
			continue // every match is allowed: the read showed the stored text
		}
		if out == nil {
			out = make(map[string]value.Value, len(patch))
			for k, v := range patch {
				out[k] = v
			}
		}
		st, isText := stored.(value.Text)
		nt, newText := nv.(value.Text)
		if isText && newText && len(jobs) == 1 && jobs[0].item < 0 {
			text, ks, err := restoreMasked(cur, name, st.String(), nt.String(), jobs[0].found)
			if err != nil {
				return nil, nil, err
			}
			if ks == nil {
				continue // no mask in the new text: a new value, scanned as usual
			}
			if text == st.String() {
				delete(out, name) // saved as printed: the field does not change
				continue
			}
			t, err := value.TextOf(text)
			if err != nil {
				return nil, nil, err
			}
			out[name] = t
			if kept == nil {
				kept = map[string][]keptSpan{}
			}
			kept[name] = ks
			continue
		}
		// a list or JSON value: the whole value as printed keeps the stored
		// one; a changed value still holding a mask cannot be read back
		masked := stored
		for _, j := range jobs {
			repl := make([]string, len(j.found))
			for i, f := range j.found {
				repl[i] = ReadRedacted + f.RuleID + "]"
			}
			masked = maskValue(masked, j.item, j.found, repl)
		}
		switch {
		case nv.String() == masked.String():
			delete(out, name) // saved as printed: the field does not change
		case strings.Contains(nv.String(), ReadRedacted):
			return nil, nil, maskEditErr(cur, name, jobs[0].found[0].RuleID)
		}
	}
	if out == nil {
		return patch, nil, nil
	}
	return out, kept, nil
}

// maskedSpans is the read mask of text (the same cut as redactText: last
// match first, an overlapping match skipped) as the text lore printed and
// the spans in printed order.
func maskedSpans(text string, fs []scan.Finding) (string, []maskedSpan) {
	order := make([]int, len(fs))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return fs[b].Offset - fs[a].Offset })
	type cut struct {
		lo, hi int
		f      scan.Finding
	}
	var cuts []cut
	end := len(text) + 1
	for _, i := range order {
		f := fs[i]
		lo, hi := f.Offset, f.Offset+f.Length
		if lo < 0 || hi > len(text) || hi > end {
			continue
		}
		cuts = append(cuts, cut{lo: lo, hi: hi, f: f})
		end = lo
	}
	slices.Reverse(cuts)
	var b strings.Builder
	spans := make([]maskedSpan, 0, len(cuts))
	at := 0
	for _, c := range cuts {
		b.WriteString(text[at:c.lo])
		tok := ReadRedacted + c.f.RuleID + "]"
		b.WriteString(tok)
		sp := maskedSpan{token: tok, stored: text[c.lo:c.hi]}
		for _, f := range fs { // every finding the span stood for
			if f.Offset < c.hi && f.Offset+f.Length > c.lo {
				sp.found = append(sp.found, f)
			}
		}
		spans = append(spans, sp)
		at = c.hi
	}
	b.WriteString(text[at:])
	return b.String(), spans
}

// restoreMasked reads next, the new text of field, against the mask of
// stored. Each mask token found exactly as printed, in printed order, is
// replaced by the stored value it stands for. It returns nil spans when
// next holds no mask token at all (a new value), and refuses a token that
// was changed, reordered or that cannot be told from a twin (two masks
// with the same rule, one of them removed).
func restoreMasked(cur row.Row, field, stored, next string, fs []scan.Finding) (string, []keptSpan, error) {
	masked, spans := maskedSpans(stored, fs)
	if len(spans) == 0 {
		return next, nil, nil
	}
	if next == masked {
		return stored, []keptSpan{}, nil // saved as printed: the stored text
	}
	literal := strings.Count(masked, ReadRedacted) - len(spans) // "[redacted " the stored text itself holds
	var (
		b         strings.Builder
		ks        []keptSpan
		at, j     int
		unmatched int
		matched   int
	)
	for {
		i := strings.Index(next[at:], ReadRedacted)
		if i < 0 {
			b.WriteString(next[at:])
			break
		}
		i += at
		k := -1
		for n := j; n < len(spans); n++ {
			if strings.HasPrefix(next[i:], spans[n].token) {
				k = n
				break
			}
		}
		if k < 0 {
			unmatched++
			b.WriteString(next[at : i+len(ReadRedacted)])
			at = i + len(ReadRedacted)
			continue
		}
		b.WriteString(next[at:i])
		start := b.Len()
		b.WriteString(spans[k].stored)
		ks = append(ks, keptSpan{at: start, end: b.Len(), found: spans[k].found})
		at = i + len(spans[k].token)
		j = k + 1
		matched++
	}
	if unmatched > literal {
		return "", nil, maskEditErr(cur, field, firstRule(spans))
	}
	if matched == 0 {
		return next, nil, nil
	}
	if matched < len(spans) && twinTokens(spans) {
		return "", nil, maskEditErr(cur, field, firstRule(spans))
	}
	return b.String(), ks, nil
}

// twinTokens reports whether two spans print the same token, so a removed
// one cannot be told from the one kept.
func twinTokens(spans []maskedSpan) bool {
	seen := map[string]bool{}
	for _, s := range spans {
		if seen[s.token] {
			return true
		}
		seen[s.token] = true
	}
	return false
}

// firstRule is the rule of the first span's token.
func firstRule(spans []maskedSpan) string {
	return strings.TrimSuffix(strings.TrimPrefix(spans[0].token, ReadRedacted), "]")
}

// maskEditErr refuses an edit that changed a mask lore printed: lore
// cannot tell what to store there, so nothing is written. It names the
// row, field and rule (never the value) and the way to change a secret.
func maskEditErr(cur row.Row, field, rule string) *errs.Error {
	return errs.New(errs.InvalidInput, cur.ID.String()+": "+field+" holds a changed "+ReadRedacted+rule+"] mask: that text stands for a stored value lore masked when it printed the row, so lore cannot tell what to store there (nothing written)").
		WithHint("keep each " + ReadRedacted + "…] exactly as printed to keep the stored value, or delete the whole mask to remove it; to change the value, replace the whole mask with the new value and answer it on the same command with --secret-ref <manager>://<locator>, --secret-unstored, or --not-a-secret --reason \"<why>\"")
}
