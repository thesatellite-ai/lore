package row

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/value"
)

// specMemory is the spec 4.1 example in canonical form (keys sorted).
const specMemory = `{
  "_hlc": "2026-11-04T10:42:07.123Z/0003/act_01936f0000007a1b8c2d3e4f5a6b7c8d",
  "_o": {
    "repo": "rep_01936f0000007c3d8e4f5a6b7c8d9e0f",
    "store": "sto_01936f00000071aa8b2c3d4e5f6a7b8c"
  },
  "_table": "memories",
  "_v": 2,
  "body": "The payment gateway returns 502 under load; ${var:db_host} is fine. Retry twice with backoff.",
  "created_at": "2026-11-04T10:42:07.123000000Z",
  "created_by": "act_01936f0000007a1b8c2d3e4f5a6b7c8d",
  "id": "mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00",
  "meta": {
    "mky_01936f1100007b2c8d3e4f5a6b7c8d9e": {
      "name": "jira",
      "scope": "project:acme-hub",
      "v": "HUB-412"
    }
  },
  "title": "Checkout needs retry on 502",
  "updated_at": "2026-11-04T10:42:07.123000000Z",
  "updated_by": "act_01936f0000007a1b8c2d3e4f5a6b7c8d"
}
`

const specTombstone = `{
  "_deleted": true,
  "_hlc": "2026-11-05T08:01:44.002Z/0000/act_01936f0000007a1b8c2d3e4f5a6b7c8d",
  "_table": "memories",
  "_v": 2,
  "id": "mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00",
  "updated_at": "2026-11-05T08:01:44.002000000Z",
  "updated_by": "act_01936f0000007a1b8c2d3e4f5a6b7c8d"
}
`

func TestSpecExamplesRoundTripByteExact(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{"memory": specMemory, "tombstone": specTombstone} {
		r, err := Decode([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, err := Encode(r)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(out) != doc {
			t.Errorf("%s: re-encode differs\n%s", name, out)
		}
		if r.SourceVersion != hlcVersion {
			t.Errorf("%s: source version %d", name, r.SourceVersion)
		}
	}
	r, err := Decode([]byte(specMemory))
	if err != nil {
		t.Fatal(err)
	}
	if r.Table != "memories" || r.HLC.Counter != 3 || len(r.Meta) != 1 || r.Fields["title"].Kind() != value.KindText {
		t.Fatalf("decoded %+v", r)
	}
}

func TestDecodeUnsortedInputGivesCanonicalOutput(t *testing.T) {
	t.Parallel()
	in := `{"title":"x","id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","_v":2,"_table":"memories","_hlc":"2026-11-04T10:42:07.123Z/0000/","n":1.50}`
	r, err := Decode([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"n": 1.50`) || !strings.HasPrefix(string(out), "{\n  \"_hlc\"") {
		t.Fatalf("not canonical: %s", out)
	}
}

func TestUpcastV1(t *testing.T) {
	t.Parallel()
	v1 := `{"_v":1,"_table":"memories","id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","body":"b","created_at":"2026-01-02T03:04:05.678901234Z","updated_at":"2026-01-02T03:04:05.678901234Z","updated_by":"act_01936f0000007a1b8c2d3e4f5a6b7c8d"}`
	r, err := Decode([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceVersion != 1 || r.HLC.String() != "2026-01-02T03:04:05.678Z/0000/act_01936f0000007a1b8c2d3e4f5a6b7c8d" {
		t.Fatalf("upcast hlc %q v%d", r.HLC, r.SourceVersion)
	}
	out, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"_v": 2`) || !strings.Contains(string(out), `"_hlc"`) {
		t.Fatalf("v1 not rewritten as v2: %s", out)
	}
	noActor := strings.Replace(v1, `,"updated_by":"act_01936f0000007a1b8c2d3e4f5a6b7c8d"`, "", 1)
	r, err = Decode([]byte(noActor))
	if err != nil || r.HLC.Actor.String() != "" || r.HLC.IsZero() {
		t.Fatalf("v1 without actor: %v %v", r.HLC, err)
	}
}

func TestDecodeRefuses(t *testing.T) {
	t.Parallel()
	base := `"_table":"memories","id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","_hlc":"2026-11-04T10:42:07.123Z/0000/"`
	cases := map[string]struct {
		doc  string
		code errs.Code
	}{
		"newer":         {`{"_v":` + strconv.Itoa(FormatVersion+1) + `,` + base + `}`, errs.OldLore},
		"no version":    {`{` + base + `}`, errs.InvalidInput},
		"v0":            {`{"_v":0,` + base + `}`, errs.InvalidInput},
		"v2 no hlc":     {`{"_v":2,"_table":"memories","id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00"}`, errs.InvalidInput},
		"bad table":     {`{"_v":2,"_table":"Memories","id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","_hlc":"2026-11-04T10:42:07.123Z/0000/"}`, errs.InvalidInput},
		"bad id":        {`{"_v":2,"_table":"memories","id":"mem_x","_hlc":"2026-11-04T10:42:07.123Z/0000/"}`, errs.InvalidID},
		"deleted false": {`{"_v":2,` + base + `,"_deleted":false}`, errs.InvalidInput},
		"tomb fields":   {`{"_v":2,` + base + `,"_deleted":true,"body":"x"}`, errs.InvalidInput},
		"camel field":   {`{"_v":2,` + base + `,"bodyText":"x"}`, errs.InvalidInput},
		"meta key":      {`{"_v":2,` + base + `,"meta":{"jira":{"v":1}}}`, errs.InvalidID},
		"meta scalar":   {`{"_v":2,` + base + `,"meta":{"mky_01936f1100007b2c8d3e4f5a6b7c8d9e":1}}`, errs.InvalidInput},
		"origin array":  {`{"_v":2,` + base + `,"_o":[]}`, errs.InvalidInput},
		"dup key":       {`{"_v":2,` + base + `,"x":1,"x":2}`, errs.InvalidInput},
		"bad time":      {`{"_v":2,` + base + `,"updated_at":"yesterday"}`, errs.InvalidInput},
		"array doc":     {`[]`, errs.InvalidInput},
	}
	for name, tc := range cases {
		if _, err := Decode([]byte(tc.doc)); !errs.Has(err, tc.code) {
			t.Errorf("%s: err = %v, want %s", name, err, tc.code)
		}
	}
}

func TestReservedKeysPreserved(t *testing.T) {
	t.Parallel()
	in := `{"_v":2,"_table":"tasks","id":"tsk_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","_hlc":"2026-11-04T10:42:07.123Z/0000/","_w":{"by":"x"}}`
	r, err := Decode([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(r)
	if err != nil || !strings.Contains(string(out), `"_w": {`) {
		t.Fatalf("reserved key lost: %s %v", out, err)
	}
}

func TestEncodeRefuses(t *testing.T) {
	t.Parallel()
	r, err := Decode([]byte(specMemory))
	if err != nil {
		t.Fatal(err)
	}
	tomb := r
	tomb.Deleted = true
	if _, err := Encode(tomb); err == nil {
		t.Error("tombstone with fields encoded")
	}
	bad := r
	bad.Fields = map[string]value.Value{"created_at": value.Null{}}
	if _, err := Encode(bad); err == nil {
		t.Error("reserved field name encoded")
	}
	noHLC := r
	noHLC.HLC = hlc.HLC{}
	if _, err := Encode(noHLC); err == nil {
		t.Error("row without hlc encoded")
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(specMemory))
	f.Add([]byte(specTombstone))
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := Decode(b)
		if err != nil {
			return
		}
		out, err := Encode(r)
		if err != nil {
			t.Fatalf("decoded row does not encode: %v", err)
		}
		r2, err := Decode(out)
		if err != nil {
			t.Fatalf("encoded row does not decode: %v", err)
		}
		out2, err := Encode(r2)
		if err != nil || string(out2) != string(out) {
			t.Fatalf("encode not a fixed point")
		}
	})
}
