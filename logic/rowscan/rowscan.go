// Package rowscan re-scans stored rows with the current secret rules
// (layer L1: logic/scan, model and schema only). It is the one every-row
// scan of the core: `lore lint` (secret_found) and `lore check leaks
// --rows` both call Scan, so the two cannot drift apart (SQ12: stored text
// is not re-scanned on show; lint and check leaks re-scan with the rules
// of today, so a rule added later finds old rows).
//
// Flow:
//
//	allows = the secret_allows rows (malformed ones allow nothing)
//	per table (sorted, secret_rules skipped: patterns, not values)
//	  per live row × field (sorted) × text (text, json, each text item)
//	    sc.Scan ─► scan.Filter(row id, allows) ─► one Hit per match
//
// A Hit carries the masked preview only, never the matched text.
package rowscan

import (
	"maps"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// secret_allows field names (schema.Builtin).
const (
	fAllowRowID = "row_id"
	fAllowField = "field"
	fAllowRule  = "rule_id"
	fAllowHash  = "match_hash"
)

// Row is one stored row and the store it was read from.
type Row struct {
	Source string
	Row    row.Row
}

// Hit is one finding in one stored row.
type Hit struct {
	Table  row.Table
	Source string
	ID     ids.AnyID
	// Finding.Field is the row field; Preview is masked (logic/scan).
	Finding scan.Finding
}

// Scan re-scans every live row of rows with sc and returns every finding
// no secret_allows row accepts, in a stable order (table, row order as
// given, field, match). Deleted rows are skipped: they print nowhere.
func Scan(sc scan.Scanner, rows map[row.Table][]Row) []Hit {
	allows := Allows(rows[schema.TableSecretAllows])
	// Every text to scan, in the output order; the scans run in parallel
	// and the hits are collected in this order, so the result is the same
	// as one scan after another (BUG-TIME-BENCH: a store with 100k time
	// entries is 100k texts). sc must be safe for concurrent use
	// (scan.Scanner's contract).
	type job struct {
		table  row.Table
		source string
		id     ids.AnyID
		field  string
		text   string
	}
	var jobs []job
	for _, t := range slices.Sorted(maps.Keys(rows)) {
		if t == schema.TableSecretRules {
			continue
		}
		for _, s := range rows[t] {
			if s.Row.Deleted {
				continue
			}
			for _, f := range slices.Sorted(maps.Keys(s.Row.Fields)) {
				for _, txt := range Texts(s.Row.Fields[f]) {
					jobs = append(jobs, job{table: t, source: s.Source, id: s.Row.ID, field: f, text: txt})
				}
			}
		}
	}
	// A scan's findings depend on the field and the text only, so each
	// distinct pair is scanned once (B1-23: a big store repeats the same
	// values across rows, and gitleaks costs per call as well as per byte);
	// the allow filter, which depends on the row, runs per job.
	type text struct{ field, text string }
	uniq := map[text]int{}
	var texts []text
	of := make([]int, len(jobs))
	for i, j := range jobs {
		k := text{field: j.field, text: j.text}
		n, ok := uniq[k]
		if !ok {
			n = len(texts)
			uniq[k] = n
			texts = append(texts, k)
		}
		of[i] = n
	}
	scanned := make([][]scan.Finding, len(texts))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), len(texts)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= len(texts) {
					return
				}
				scanned[i] = sc.Scan(texts[i].field, texts[i].text)
			}
		}()
	}
	wg.Wait()
	var out []Hit
	for i, j := range jobs {
		for _, hit := range scan.Filter(j.id, scanned[of[i]], allows) {
			out = append(out, Hit{Table: j.table, Source: j.source, ID: j.id, Finding: hit})
		}
	}
	return out
}

// Texts is the scannable text of one field value: a text or JSON value,
// or each text item of a list (nested lists included).
func Texts(v value.Value) []string {
	switch x := v.(type) {
	case value.Text:
		return []string{x.String()}
	case value.JSON:
		return []string{x.String()}
	case value.List:
		var out []string
		for _, it := range x.Items() {
			out = append(out, Texts(it)...)
		}
		return out
	}
	return nil
}

// Allows reads the secret_allows rows; a deleted, incomplete or malformed
// one allows nothing (fail closed: the finding stays).
func Allows(rs []Row) []scan.Allow {
	var out []scan.Allow
	for _, s := range rs {
		if s.Row.Deleted {
			continue
		}
		target, err := ids.ParseAny(text(s.Row, fAllowRowID))
		if err != nil {
			continue
		}
		n, ok := s.Row.Fields[fAllowHash].(value.Number)
		if !ok {
			continue
		}
		h, err := n.Int64()
		if err != nil || h < 0 || h > math.MaxUint32 {
			continue
		}
		out = append(out, scan.Allow{RowID: target, Field: text(s.Row, fAllowField), RuleID: text(s.Row, fAllowRule), MatchHash: uint32(h)})
	}
	return out
}

// text is field f of r as a string ("" when unset or null).
func text(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil && v.Kind() != value.KindNull {
		return v.String()
	}
	return ""
}
