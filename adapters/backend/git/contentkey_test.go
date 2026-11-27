package git

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// mapContentKey is contentKey as it was before H-285: the file decoded
// into a map of its members. The streaming contentKey must give the same
// digest for every input, or the replay would take a version it accepted
// before for another content (a hand-made change) or the other way round.
func mapContentKey(raw []byte) contentSum {
	var obj map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(raw), &obj) != nil {
		return contentSum{}
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if k != contentFormatKey {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	h := sha256.New()
	var buf bytes.Buffer
	for _, k := range keys {
		buf.Reset()
		if json.Compact(&buf, obj[k]) != nil {
			buf.Write(obj[k])
		}
		h.Write([]byte(strconv.Quote(k)))
		h.Write([]byte{0})
		h.Write(buf.Bytes())
		h.Write([]byte{0})
	}
	var sum contentSum
	copy(sum[:], h.Sum(nil))
	return sum
}

// jsonGen writes random JSON in many spellings: nested objects and
// arrays, escapes, unicode and bytes that are not UTF-8, numbers of every
// form, odd whitespace, keys given twice, the format key.
type jsonGen struct {
	r *rand.Rand
	b strings.Builder
}

func (g *jsonGen) space() {
	for range g.r.IntN(3) {
		g.b.WriteByte(" \t\n\r"[g.r.IntN(4)])
	}
}

var genKeys = []string{"id", "_v", "body", "_deleted", "a", "A", "é", "\\u0061", "x\\ny", "\\ud83d\\ude00", "k\xff", "\\u00e9", "\\\"q", "", "ü", "\\/", "b c"}

var genStrings = []string{"", "plain", "with space", "ü ı 東京 😀", "\\u0000", "esc \\\" \\\\ \\/ \\b\\f\\n\\r\\t", "\\u2028", "bad \xff\xfe", "\\ud800 lone", "<a&b>", "\\uD83D\\uDE00"}

var genNumbers = []string{"0", "-0", "1", "-12", "1.50", "1e5", "1E-7", "-2.5e+300", "123456789012345678901234567890", "0.1"}

func (g *jsonGen) str(pool []string) {
	g.b.WriteByte('"')
	g.b.WriteString(pool[g.r.IntN(len(pool))])
	g.b.WriteByte('"')
}

func (g *jsonGen) value(depth int) {
	n := 6
	if depth > 3 {
		n = 4
	}
	switch g.r.IntN(n) {
	case 0:
		g.str(genStrings)
	case 1:
		g.b.WriteString(genNumbers[g.r.IntN(len(genNumbers))])
	case 2:
		g.b.WriteString([]string{"true", "false", "null"}[g.r.IntN(3)])
	case 3:
		g.str(genStrings)
	case 4:
		g.object(depth + 1)
	case 5:
		g.b.WriteByte('[')
		for i := range g.r.IntN(4) {
			if i > 0 {
				g.b.WriteByte(',')
			}
			g.space()
			g.value(depth + 1)
			g.space()
		}
		g.b.WriteByte(']')
	}
}

func (g *jsonGen) object(depth int) {
	g.b.WriteByte('{')
	for i := range g.r.IntN(7) {
		if i > 0 {
			g.b.WriteByte(',')
		}
		g.space()
		g.str(genKeys)
		g.space()
		g.b.WriteByte(':')
		g.space()
		g.value(depth)
		g.space()
	}
	g.b.WriteByte('}')
}

// genDoc is one random input: mostly an object, sometimes another
// top-level value, cut short or with something after it.
func genDoc(r *rand.Rand) []byte {
	g := &jsonGen{r: r}
	g.space()
	if r.IntN(10) == 0 {
		g.value(0)
	} else {
		g.object(0)
	}
	g.space()
	out := []byte(g.b.String())
	switch r.IntN(20) {
	case 0:
		out = out[:r.IntN(len(out)+1)]
	case 1:
		out = append(out, []byte(` {}`)...)
	case 2:
		out = append([]byte(" "), out...) // TrimSpace trims more than JSON whitespace
	}
	return out
}

// TestContentKeyMatchesMapDecode: the streaming contentKey gives the
// digest the map decode gave (H-285, devco:D-1023) on fixed edge cases,
// 20,000 random documents and every row file in the repository's test
// data, so the replay's authority decisions are the same.
func TestContentKeyMatchesMapDecode(t *testing.T) {
	cases := []string{
		``, ` `, `null`, ` null `, `{}`, `[]`, `"x"`, `1`, `true`, `{`, `{"a":1}x`, `{"a":1} {}`,
		`{"a":1,"a":2}`, `{"a":2,"b":0,"a":1}`, `{"_v":2}`, `{"_v":1,"_v":2,"x":"y"}`, `{"_v":3,"k":1}`,
		`{"a":{"b":[1, 2 ,{"c" : "d e"}]}, "z" : null}`, `{"k\xff":1}`, `{"\ud800":1,"�":2}`,
		`{"a":"é","é":"a"}`, "{\n  \"id\": \"mem_x\",\n  \"_v\": 2,\n  \"body\": \"two  spaces\"\n}\n",
	}
	check := func(name string, raw []byte) {
		t.Helper()
		if got, want := contentKey(raw), mapContentKey(raw); got != want {
			t.Fatalf("%s: contentKey %x, the map decode %x, for %q", name, got, want, raw)
		}
	}
	for i, c := range cases {
		check("case "+strconv.Itoa(i), []byte(c))
	}
	r := rand.New(rand.NewPCG(285, 1023))
	for i := range 20000 {
		check("random "+strconv.Itoa(i), genDoc(r))
	}
	files := 0
	root := filepath.Join("..", "..", "..")
	for _, dir := range []string{"wire/testdata", "adapters/backend/rowfs/testdata", "model/canon/testdata"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
				return err
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files++
			check(p, raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 50 {
		t.Fatalf("checked %d row files of the test data, want the whole set (50+)", files)
	}
}

// FuzzContentKey: any input, the same digest as the map decode.
func FuzzContentKey(f *testing.F) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 50 {
		f.Add(genDoc(r))
	}
	f.Add([]byte(`{"a":1,"a":2}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if got, want := contentKey(raw), mapContentKey(raw); got != want {
			t.Fatalf("contentKey %x, the map decode %x, for %q", got, want, raw)
		}
	})
}
