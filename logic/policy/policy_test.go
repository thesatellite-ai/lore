package policy

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

func txt(s string) value.Value {
	v, err := value.TextOf(s)
	if err != nil {
		panic(err)
	}
	return v
}

func list(items ...string) value.Value {
	vs := make([]value.Value, len(items))
	for i, s := range items {
		vs[i] = txt(s)
	}
	l, err := value.ListOf(value.KindText, vs...)
	if err != nil {
		panic(err)
	}
	return l
}

func src(n int) ids.AnyID {
	id, err := ids.ParseAny("pol_01936f3400007e5f8a6b7c8d9e0f1a2" + strconv.Itoa(n%10))
	if err != nil {
		panic(err)
	}
	return id
}

func TestResolveStricterWins(t *testing.T) {
	cases := []struct {
		name      string
		entries   []Entry
		key       Key
		want      string
		wantLevel Level
	}{
		{"no rows: default", nil, KeyOnDetect, "ask", ""},
		{"one level replaces the default, even looser",
			[]Entry{{Level: LevelOrg, Key: KeyOnDetect, Value: txt("redact"), Source: src(1)}},
			KeyOnDetect, "redact", LevelOrg},
		{"a nearer level cannot loosen a farther one",
			[]Entry{
				{Level: LevelRepo, Key: KeyOnDetect, Value: txt("redact"), Source: src(1)},
				{Level: LevelOrg, Key: KeyOnDetect, Value: txt("refuse"), Source: src(2)},
			}, KeyOnDetect, "refuse", LevelOrg},
		{"a nearer level tightens",
			[]Entry{
				{Level: LevelPersonal, Key: KeyOnDetect, Value: txt("redact"), Source: src(1)},
				{Level: LevelFolder, Key: KeyOnDetect, Value: txt("ask"), Source: src(2)},
			}, KeyOnDetect, "ask", LevelFolder},
		{"equal strictness: nearest is the source",
			[]Entry{
				{Level: LevelStoreRoot, Key: KeyOnDetect, Value: txt("ask"), Source: src(1)},
				{Level: LevelRepo, Key: KeyOnDetect, Value: txt("ask"), Source: src(2)},
			}, KeyOnDetect, "ask", LevelRepo},
		{"limit: smaller wins",
			[]Entry{
				{Level: LevelRepo, Key: KeyMetaMaxBytes, Value: value.Int(9000), Source: src(1)},
				{Level: LevelOrg, Key: KeyMetaMaxBytes, Value: value.Int(4000), Source: src(2)},
			}, KeyMetaMaxBytes, "4000", LevelOrg},
		{"batch preview: a lower count is stricter (XQ2)",
			[]Entry{
				{Level: LevelRepo, Key: KeyBatchPreviewRows, Value: value.Int(50), Source: src(1)},
				{Level: LevelOrg, Key: KeyBatchPreviewRows, Value: value.Int(5), Source: src(2)},
			}, KeyBatchPreviewRows, "5", LevelOrg},
		{"flag: false (fewer allowlists) is stricter",
			[]Entry{
				{Level: LevelRepo, Key: KeyUseRepoGitleaks, Value: value.BoolOf(true), Source: src(1)},
				{Level: LevelOrg, Key: KeyUseRepoGitleaks, Value: value.BoolOf(false), Source: src(2)},
			}, KeyUseRepoGitleaks, "false", LevelOrg},
		{"set: union of every level",
			[]Entry{
				{Level: LevelRepo, Key: KeyAgentsPersonOnly, Value: list("trash.empty"), Source: src(1)},
				{Level: LevelOrg, Key: KeyAgentsPersonOnly, Value: list("secrets.show", "trash.empty"), Source: src(2)},
			}, KeyAgentsPersonOnly, "[secrets.show trash.empty]", LevelRepo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Resolve(tc.entries)
			if err != nil {
				t.Fatal(err)
			}
			e, ok := s.Get(tc.key)
			if !ok {
				t.Fatal("key missing")
			}
			got := e.Value.String()
			if tc.key == KeyAgentsPersonOnly {
				got = "[" + strings.Join(s.Strings(tc.key), " ") + "]"
			}
			if got != tc.want || e.Level != tc.wantLevel {
				t.Fatalf("got %s at %q, want %s at %q", got, e.Level, tc.want, tc.wantLevel)
			}
		})
	}
}

func TestResolveFailsClosed(t *testing.T) {
	cases := map[string]Entry{
		"unknown key":   {Level: LevelRepo, Key: "secrets.whatever", Value: txt("x"), Source: src(1)},
		"bad choice":    {Level: LevelRepo, Key: KeyOnDetect, Value: txt("ignore"), Source: src(1)},
		"bad kind":      {Level: LevelRepo, Key: KeyMetaMaxBytes, Value: txt("big"), Source: src(1)},
		"over ceiling":  {Level: LevelRepo, Key: KeyTasksMaxDepth, Value: value.Int(9), Source: src(1)},
		"negative":      {Level: LevelRepo, Key: KeyTrashRetentionDays, Value: value.Int(-1), Source: src(1)},
		"unknown level": {Level: "galaxy", Key: KeyOnDetect, Value: txt("ask"), Source: src(1)},
		"no value":      {Level: LevelRepo, Key: KeyOnDetect, Source: src(1)},
	}
	for name, e := range cases {
		_, err := Resolve([]Entry{e})
		if !errs.Has(err, errs.ConfigInvalid) || !strings.Contains(err.Error(), src(1).String()) {
			t.Errorf("%s: err = %v, want E_CONFIG_INVALID naming the row", name, err)
		}
	}
}

func TestDefaultsAndLimits(t *testing.T) {
	d := Defaults()
	if d.OnDetect() != OnDetectAsk {
		t.Fatalf("default on_detect = %s", d.OnDetect())
	}
	if (Set{}).OnDetect() != OnDetectAsk {
		t.Fatal("zero Set must answer the decided default")
	}
	if _, set := d.Limit(KeyTrashRetentionDays); set {
		t.Fatal("trash retention must default to unset (keep forever)")
	}
	if n, set := d.Limit(KeyMetaMaxBytes); !set || n != defaultMaxBytes {
		t.Fatalf("meta.max_bytes = %d %v", n, set)
	}
	if n, set := d.Limit(KeyBatchPreviewRows); !set || n != 20 {
		t.Fatalf("batch.preview_rows = %d %v, want the XQ2 default 20", n, set)
	}
	for _, k := range Keys() {
		if k.Default != nil {
			if err := check(k, k.Default); err != nil {
				t.Errorf("%s: default fails its own shape: %v", k.Key, err)
			}
		}
	}
}

func TestEntryFromRow(t *testing.T) {
	raw, err := value.JSONOf([]byte(`"refuse"`))
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Table: schema.TablePolicies, ID: src(3), HLC: hlc.HLC{}, Fields: map[string]value.Value{
		"key": txt(string(KeyOnDetect)), "value": raw, "scope": txt("prj_x"),
	}}
	e, err := EntryFromRow(LevelOrg, r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Resolve([]Entry{e})
	if err != nil {
		t.Fatal(err)
	}
	if s.OnDetect() != OnDetectRefuse {
		t.Fatalf("on_detect = %s", s.OnDetect())
	}
	r.Deleted = true
	if _, err := EntryFromRow(LevelOrg, r); err == nil {
		t.Fatal("read a tombstone as a policy")
	}
}

func TestNearest(t *testing.T) {
	cands := []Leveled[string]{
		{Level: LevelOrg, Value: "org"},
		{Level: LevelFolder, Value: "folder-first"},
		{Level: LevelFolder, Value: "folder-second"},
		{Level: LevelPersonal, Value: "me"},
	}
	got, ok := Nearest(cands)
	if !ok || got.Value != "folder-first" {
		t.Fatalf("Nearest = %+v", got)
	}
	if _, ok := Nearest[string](nil); ok {
		t.Fatal("Nearest of nothing")
	}
}

func TestRules(t *testing.T) {
	entries := []RuleEntry{
		{Level: LevelRepo, RuleID: "aws-access-token", State: schema.SecretRuleOn},
		{Level: LevelOrg, RuleID: "aws-access-token", State: schema.SecretRuleOff, Source: src(1)},
		{Level: LevelPersonal, RuleID: "generic-api-key", State: schema.SecretRuleOff, Source: src(2)},
		{Level: LevelRepo, RuleID: "stripe", State: schema.SecretRuleChanged},
	}
	r := ResolveRules(entries)
	if !r.IsOff("aws-access-token", false) {
		t.Error("an org turn-off must hold even with a nearer on")
	}
	if !r.IsOff("generic-api-key", true) || r.IsOff("generic-api-key", false) {
		t.Error("a personal turn-off covers own captures only (BATCH 8)")
	}
	if r.IsOff("stripe", true) {
		t.Error("changed is not off")
	}
	off := r.Off()
	if len(off) != 2 || off[0].RuleID != "aws-access-token" || off[1].Level != LevelPersonal {
		t.Fatalf("Off = %+v", off)
	}
}
