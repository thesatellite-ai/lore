package row

import (
	"cmp"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Format determinism (FORMAT_EVOLUTION 7.3, rule 3): the bytes of a row file
// depend only on the row's content, never on who wrote it, in which order a
// writer emitted its keys, how it spaced them, how it escaped a string, or
// how often it was written. Two machines that read the same row and write it
// back therefore produce byte-identical files, and a second write changes
// nothing. The tests below sweep that input space as a matrix:
//
//	field value kinds (text, unicode, escapes, empty, huge, numbers in every
//	literal form, bools, null, lists, nested objects, empty containers)
//	× source version (_v 1 with and without updated_by, _v 2)
//	× key order (as written, reversed, shuffled with several seeds)
//	× spacing (compact, indented, padded)
//	× string escaping (literal vs \u-escaped)
//	× repetition (encode N times, decode → encode → decode → encode)

// detField is one body field of the matrix: its key and its raw JSON as a
// writer may have spelled it (escaped is an equivalent spelling with every
// non-ASCII rune and quote written as a \u escape).
type detField struct {
	key, raw, escaped string
}

// detFields are the value dimension of the matrix. Every key is a valid
// field name; the odd ones (digits, long, underscore-inner) are on purpose.
func detFields() []detField {
	long := strings.Repeat("lorem ipsum ", 400) // ~4.8 KB body
	return []detField{
		{"title", `"ship it"`, `"ship it"`},
		{"body", `"` + long + `"`, ""},
		{"empty", `""`, ""},
		{"unicode", `"naïve café — 日本語 🚀"`, `"naïve café — 日本語 🚀"`},
		{"html", `"<a href=\"x\">&amp;</a>"`, `"<a href=\"x\">&amp;</a>"`},
		{"ctrl", `"tab\tnew\nline\\back"`, `"tab\u0009new\u000aline\\back"`},
		{"int", `42`, ""},
		{"neg", `-7`, ""},
		{"zero", `0`, ""},
		{"decimal", `1.50`, ""},
		{"exp", `6.02e23`, ""},
		{"flag_true", `true`, ""},
		{"flag_false", `false`, ""},
		{"nothing", `null`, ""},
		{"tags", `["b","a","a"]`, ""},
		{"no_items", `[]`, ""},
		{"nums", `[3,1,2]`, ""},
		{"mixed", `[1,"a",true,null]`, ""},
		{"nested", `{"z":{"y":[{"b":1,"a":2}],"x":null},"a":"é"}`, `{"z":{"y":[{"b":1,"a":2}],"x":null},"a":"é"}`},
		{"empty_obj", `{}`, ""},
		{"k9", `"digits in the name"`, ""},
		{"a_very_long_field_name_that_is_still_valid_x", `"x"`, ""},
	}
}

// detHeader are the envelope members of each source version.
func detHeader(v int, withActor bool) [][2]string {
	h := [][2]string{
		{"_table", `"memories"`},
		{"id", `"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00"`},
		{"created_at", `"2026-01-02T03:04:05.678901234Z"`},
		{"updated_at", `"2026-01-02T03:04:05.678901234Z"`},
	}
	if v == 1 {
		h = append(h, [2]string{"_v", "1"})
	} else {
		h = append(h, [2]string{"_v", "2"}, [2]string{"_hlc", `"2026-01-02T03:04:05.678Z/0002/act_01936f0000007a1b8c2d3e4f5a6b7c8d"`})
	}
	if withActor {
		h = append(h, [2]string{"updated_by", `"act_01936f0000007a1b8c2d3e4f5a6b7c8d"`}, [2]string{"created_by", `"act_01936f0000007a1b8c2d3e4f5a6b7c8d"`})
	}
	return h
}

// detOrder is one key-order variant.
type detOrder struct {
	name string
	perm func([][2]string) [][2]string
}

// detOrders are the key-order dimension: as written, reversed and three
// fixed permutations (keys ordered by an FNV hash salted with a seed: a
// different, reproducible order per seed, no random source needed).
func detOrders() []detOrder {
	out := []detOrder{
		{"as-written", func(m [][2]string) [][2]string { return m }},
		{"reversed", func(m [][2]string) [][2]string {
			r := make([][2]string, len(m))
			for i := range m {
				r[len(m)-1-i] = m[i]
			}
			return r
		}},
	}
	for _, seed := range []uint64{1, 7, 1789} {
		out = append(out, detOrder{"shuffle-" + strconv.FormatUint(seed, 10), func(m [][2]string) [][2]string {
			r := append([][2]string(nil), m...)
			key := func(kv [2]string) uint64 {
				h := fnv.New64a()
				_, _ = h.Write([]byte(strconv.FormatUint(seed, 10) + "/" + kv[0])) // hash.Hash.Write never fails
				return h.Sum64()
			}
			slices.SortFunc(r, func(a, b [2]string) int { return cmp.Compare(key(a), key(b)) })
			return r
		}})
	}
	return out
}

// detSpacing renders members as a JSON object in one spacing style.
type detSpacing struct {
	name   string
	render func([][2]string) string
}

func detSpacings() []detSpacing {
	join := func(m [][2]string, open, sep, colon, closing string) string {
		parts := make([]string, len(m))
		for i, kv := range m {
			parts[i] = `"` + kv[0] + `"` + colon + kv[1]
		}
		return open + strings.Join(parts, sep) + closing
	}
	return []detSpacing{
		{"compact", func(m [][2]string) string { return join(m, "{", ",", ":", "}") }},
		{"indented", func(m [][2]string) string { return join(m, "{\n  ", ",\n  ", ": ", "\n}\n") }},
		{"padded", func(m [][2]string) string { return join(m, " \t{ ", " ,\r\n ", " : ", " }\n\n") }},
	}
}

// TestRowBytesIndependentOfWriterSpelling: every spelling of the same row
// (key order × spacing × escaping) decodes and re-encodes to the same bytes,
// per source version, and those bytes are a fixed point (a second write is
// byte-identical). This is "two machines produce the same file".
func TestRowBytesIndependentOfWriterSpelling(t *testing.T) {
	t.Parallel()
	for _, src := range []struct {
		name  string
		v     int
		actor bool
	}{{"v1-actor", 1, true}, {"v1-no-actor", 1, false}, {"v2", 2, true}} {
		for _, escaped := range []bool{false, true} {
			members := detHeader(src.v, src.actor)
			for _, f := range detFields() {
				raw := f.raw
				if escaped && f.escaped != "" {
					raw = f.escaped
				}
				members = append(members, [2]string{f.key, raw})
			}
			var want []byte
			for _, o := range detOrders() {
				for _, sp := range detSpacings() {
					doc := sp.render(o.perm(members))
					r, err := Decode([]byte(doc))
					if err != nil {
						t.Fatalf("%s %s %s: decode: %v", src.name, o.name, sp.name, err)
					}
					if r.SourceVersion != src.v {
						t.Fatalf("%s: source version %d", src.name, r.SourceVersion)
					}
					got, err := Encode(r)
					if err != nil {
						t.Fatalf("%s %s %s: encode: %v", src.name, o.name, sp.name, err)
					}
					if want == nil {
						want = got
					} else if string(got) != string(want) {
						t.Fatalf("%s escaped=%v %s %s: bytes differ\n got %s\nwant %s", src.name, escaped, o.name, sp.name, got, want)
					}
					// Fixed point: what a reader writes back is what it read.
					again, err := Decode(got)
					if err != nil {
						t.Fatal(err)
					}
					twice, err := Encode(again)
					if err != nil || string(twice) != string(got) {
						t.Fatalf("%s %s %s: second write changed the file (%v)\n%s\n%s", src.name, o.name, sp.name, err, got, twice)
					}
				}
			}
			if src.v == 1 && !strings.Contains(string(want), `"_v": 2`) {
				t.Fatalf("%s not upcast: %s", src.name, want)
			}
		}
	}
}

// TestUpcastSameBytesOnEveryMachine: a v1 row's upcast (the _hlc derived from
// its own updated_at and updated_by, FORMAT_EVOLUTION 7.3 item 7) is the
// same whatever the reader's clock, time zone or GOMAXPROCS: Decode takes no
// clock, so the only inputs are the bytes. The escaped and unescaped
// spellings of the v1 file give the same v2 file.
func TestUpcastSameBytesOnEveryMachine(t *testing.T) {
	plain := `{"_v":1,"_table":"memories","id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","body":"café","created_at":"2026-01-02T03:04:05.678901234Z","updated_at":"2026-01-02T03:04:05.678901234Z","updated_by":"act_01936f0000007a1b8c2d3e4f5a6b7c8d"}`
	escaped := strings.Replace(plain, "café", `café`, 1)
	var want string
	for _, tz := range []string{"UTC", "Asia/Kolkata", "America/Los_Angeles", "Pacific/Chatham"} {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			t.Skipf("no tzdata for %s: %v", tz, err) // the container may lack tzdata; UTC alone still ran
		}
		prev := time.Local
		time.Local = loc
		for _, doc := range []string{plain, escaped} {
			r, err := Decode([]byte(doc))
			if err != nil {
				time.Local = prev
				t.Fatal(err)
			}
			out, err := Encode(r)
			if err != nil {
				time.Local = prev
				t.Fatal(err)
			}
			if want == "" {
				want = string(out)
			} else if string(out) != want {
				time.Local = prev
				t.Fatalf("tz %s: upcast differs\n%s\n%s", tz, out, want)
			}
		}
		time.Local = prev
	}
	if !strings.Contains(want, `"_hlc": "2026-01-02T03:04:05.678Z/0000/act_01936f0000007a1b8c2d3e4f5a6b7c8d"`) || !strings.Contains(want, `"body": "café"`) {
		t.Fatalf("upcast = %s", want)
	}
}

// TestEncodeIndependentOfMapOrder: a Row built in code encodes to the same
// bytes however its maps were filled (Go randomises map iteration, so each
// of the repeated encodes walks Fields, Reserved and Meta in a new order),
// and Fields filled in two opposite insertion orders give the same bytes.
func TestEncodeIndependentOfMapOrder(t *testing.T) {
	t.Parallel()
	id, err := ids.ParseAny("mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ids.Parse[ids.Actor]("act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := hlc.Parse("2026-11-04T10:42:07.123Z/0003/act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	if err != nil {
		t.Fatal(err)
	}
	build := func(reverse bool) Row {
		fs := detFields()
		r := Row{Table: "memories", ID: id, HLC: stamp, UpdatedBy: actor, UpdatedAt: time.Date(2026, 11, 4, 10, 42, 7, 123000000, time.UTC),
			Fields: map[string]value.Value{}, Reserved: map[string]value.Value{}, Meta: map[ids.ID[ids.MetaKey]]value.JSON{}}
		for i := range fs {
			f := fs[i]
			if reverse {
				f = fs[len(fs)-1-i]
			}
			v, ierr := value.Infer([]byte(f.raw))
			if ierr != nil {
				t.Fatalf("%s: %v", f.key, ierr)
			}
			r.Fields[f.key] = v
		}
		for i, k := range []string{"mky_01936f1100007b2c8d3e4f5a6b7c8d9e", "mky_01936f1100007b2c8d3e4f5a6b7c8d9f", "mky_01936f1100007b2c8d3e4f5a6b7c8da0"} {
			if reverse {
				k = []string{"mky_01936f1100007b2c8d3e4f5a6b7c8da0", "mky_01936f1100007b2c8d3e4f5a6b7c8d9f", "mky_01936f1100007b2c8d3e4f5a6b7c8d9e"}[i]
			}
			mk, perr := ids.Parse[ids.MetaKey](k)
			if perr != nil {
				t.Fatal(perr)
			}
			j, jerr := value.JSONOf([]byte(`{"v":"` + k[len(k)-2:] + `","name":"n","scope":"repo"}`))
			if jerr != nil {
				t.Fatal(jerr)
			}
			r.Meta[mk] = j
		}
		w, terr := value.TextOf("future")
		if terr != nil {
			t.Fatal(terr)
		}
		r.Reserved["_w"] = w
		return r
	}
	want, err := Encode(build(false))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		got, err := Encode(build(i%2 == 1))
		if err != nil || string(got) != string(want) {
			t.Fatalf("encode %d differs (%v)\n%s\n%s", i, err, got, want)
		}
	}
}
