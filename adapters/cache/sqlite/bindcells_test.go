package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"math"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/value"
)

// oldValueCell is the JSON cell a typed insert sent before H-285 (one JSON
// array per row, read back with json_extract per column): the reference
// the bound parameters must store the same as.
func oldValueCell(v value.Value) json.RawMessage {
	if v == nil {
		return jsonNull
	}
	switch v.Kind() {
	case value.KindNull:
		return jsonNull
	case value.KindBool:
		if b, ok := v.(value.Bool); ok && b.Bool() {
			return json.RawMessage("1")
		}
		return json.RawMessage("0")
	case value.KindNumber:
		return json.RawMessage(v.String())
	case value.KindJSON, value.KindList:
		raw, err := v.MarshalJSON()
		if err != nil {
			return jsonNull
		}
		return canon.String(string(raw))
	}
	return canon.String(v.String())
}

// boundCellsDB opens a scratch database with the old and the new insert
// over one column of every affinity (TEXT, INTEGER, REAL, NUMERIC, BLOB,
// none), so a value is stored through both and compared by type and value.
func boundCellsDB(t *testing.T) (db *sql.DB, oldQ, newQ string) {
	t.Helper()
	db, err := sql.Open(driverName, mustDSN(t, filepath.Join(t.TempDir(), "cells.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cols := "a TEXT, b INTEGER, c REAL, d NUMERIC, e BLOB, f"
	for _, tb := range []string{"old", "new"} {
		if _, err := db.Exec("CREATE TABLE " + tb + " (k INTEGER PRIMARY KEY, " + cols + ")"); err != nil {
			t.Fatal(err)
		}
	}
	oldQ = "INSERT INTO old (k, a, b, c, d, e, f) VALUES (?1, " + strings.ReplaceAll(extractList(6), "?1", "?2") + ")"
	newQ = "INSERT INTO new (k, a, b, c, d, e, f) VALUES (?, " + bindList(6) + ")"
	return db, oldQ, newQ
}

// storeBoth stores one value's cell through the old JSON array and its
// parameters through bindList, under key k.
func storeBoth(t *testing.T, db *sql.DB, oldQ, newQ string, k int, cell json.RawMessage, v, num driver.Value) {
	t.Helper()
	cells := make([]json.RawMessage, 6)
	for i := range cells {
		cells[i] = cell
	}
	arr, err := json.Marshal(cells)
	if err != nil {
		t.Fatalf("value %d: encode %q: %v", k, cell, err)
	}
	if _, err := db.Exec(oldQ, k, string(arr)); err != nil {
		t.Fatalf("value %d: old insert %s: %v", k, arr, err)
	}
	args := []any{k}
	for range 6 {
		args = append(args, v, num)
	}
	if _, err := db.Exec(newQ, args...); err != nil {
		t.Fatalf("value %d: new insert %v / %v: %v", k, v, num, err)
	}
}

// TestBoundCellsStoreTheSame: a typed row's values bound as parameters
// (bindList, valueArg, textArg) store exactly what the JSON array read back
// with json_extract stored (H-285, devco:D-1023), in a column of every
// affinity: every kind, numbers of every spelling (integers past int64,
// exponents, random floats), text with invalid UTF-8, NUL, controls,
// U+2028 and emoji. Same type and same value (quote), byte for byte.
func TestBoundCellsStoreTheSame(t *testing.T) {
	db, oldQ, newQ := boundCellsDB(t)
	var vals []value.Value
	for _, raw := range []string{
		`null`, `true`, `false`, `0`, `-0`, `1`, `-1`, `1.0`, `1.50`, `0.1`, `1e5`, `1E-7`, `-2.5e+300`, `1e308`, `4.9e-324`,
		`9223372036854775807`, `9223372036854775808`, `-9223372036854775808`, `-9223372036854775809`, `123456789012345678901234567890`,
		`0.30000000000000004`, `2.2250738585072011e-308`, `1.7976931348623157e308`,
		`""`, `"plain"`, `"a\"b\\c"`, `"\u0000x"`, `"x\u0000"`, `"tab\tnl\ncr\r"`, `"  "`, `"ü ı ✓ 😀"`, `"<a&b>"`, `"\u001f"`,
		`{}`, `{"a":1,"b":[true,null,"x"]}`, `[]`, `[1,"two",{"three":3}]`, `{"nested":{"deep":"ü\u0000"}}`,
	} {
		v, err := value.Infer([]byte(raw))
		if err != nil {
			t.Fatalf("infer %s: %v", raw, err)
		}
		vals = append(vals, v)
	}
	rng := rand.New(rand.NewPCG(285, 1023))
	for range 3000 {
		f := math.Float64frombits(rng.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		for _, lit := range []string{strconv.FormatFloat(f, 'g', -1, 64), strconv.FormatFloat(f, 'e', 3+rng.IntN(20), 64)} {
			if n, err := value.NumberOf(lit); err == nil {
				vals = append(vals, n)
			}
		}
		vals = append(vals, value.Int(rng.Int64()-rng.Int64()))
		if n, err := value.Float(rng.NormFloat64() * math.Pow(10, float64(rng.IntN(40)-20))); err == nil {
			vals = append(vals, n)
		}
	}
	for _, d := range []string{"2026-11-27", "1999-01-01"} {
		v, err := value.Parse(value.KindDate, canon.String(d))
		if err != nil {
			t.Fatal(err)
		}
		vals = append(vals, v)
	}
	k := 0
	for _, v := range vals {
		k++
		a, num := valueArg(v)
		storeBoth(t, db, oldQ, newQ, k, oldValueCell(v), a, num)
	}
	k++
	storeBoth(t, db, oldQ, newQ, k, oldValueCell(nil), nil, nil)
	// textArg: ids, sources and any string, valid UTF-8 or not.
	texts := []string{"", "src", "a\x00b", "\xff", "a\xffb\xfe", "\xe2\x82", "\xed\xa0\x80", "ok ü\x80\x80", " ", "\x7f\x01"}
	for range 2000 {
		b := make([]byte, rng.IntN(12))
		for i := range b {
			b[i] = byte(rng.UintN(256))
		}
		texts = append(texts, string(b))
	}
	for _, s := range texts {
		k++
		storeBoth(t, db, oldQ, newQ, k, canon.String(s), textArg(s), nil)
	}
	const q = "SELECT k, typeof(a)||':'||quote(a), typeof(b)||':'||quote(b), typeof(c)||':'||quote(c), typeof(d)||':'||quote(d), typeof(e)||':'||quote(e), typeof(f)||':'||quote(f) FROM "
	read := func(tb string) map[int]string {
		rows, err := db.Query(q + tb)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		out := map[int]string{}
		for rows.Next() {
			var id int
			var c [6]string
			if err := rows.Scan(&id, &c[0], &c[1], &c[2], &c[3], &c[4], &c[5]); err != nil {
				t.Fatal(err)
			}
			out[id] = strings.Join(c[:], " | ")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	old, got := read("old"), read("new")
	if len(old) != k || len(got) != k {
		t.Fatalf("stored %d old and %d new rows, want %d", len(old), len(got), k)
	}
	bad := 0
	for i := 1; i <= k; i++ {
		if old[i] != got[i] {
			bad++
			if bad <= 10 {
				t.Errorf("value %d: bound parameters stored\n  %s\nthe JSON cell stored\n  %s", i, got[i], old[i])
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d values stored differently", bad, k)
	}
}
