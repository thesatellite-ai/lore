package scan

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestMask (item 29): the preview is "sha256:" + 12 hex of the value's
// SHA-256, the same for the same value, different for different values,
// and never holds a character run of the value.
func TestMask(t *testing.T) {
	t.Parallel()
	if got := Mask("abc"); got != "sha256:ba7816bf8f01" {
		t.Errorf("Mask(abc) = %q", got)
	}
	seen := map[string]bool{}
	for _, in := range []string{"", "abcd", "Zk3Qp9Lm2Xv8Rt5W", strings.Repeat("x", 40), "AKIA" + "Q7WE9RT2YU4IO6PA"} {
		got := Mask(in)
		if len(got) != len("sha256:")+PreviewHexLen || !strings.HasPrefix(got, "sha256:") {
			t.Errorf("Mask(%d chars) = %q, not a match hash", len(in), got)
		}
		if seen[got] {
			t.Errorf("Mask(%d chars) repeats another value's hash", len(in))
		}
		seen[got] = true
		for i := 0; i+4 <= len(in); i++ {
			if strings.Contains(got, in[i:i+4]) {
				t.Errorf("Mask(%d chars) carries 4 characters of the value", len(in))
			}
		}
		if Mask(in) != got {
			t.Errorf("Mask(%d chars) is not stable", len(in))
		}
	}
}

func TestRegexScannerAndFilter(t *testing.T) {
	t.Parallel()
	key := "AKIA" + strings.Repeat("Q", 4) + "7Z2X9KL0PM4N"
	s, err := NewRegexScanner([]Rule{
		{ID: "aws-access-token", Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: SourceBuiltin},
		{ID: "never", Regex: `zzz`, Keywords: []string{"nomatch"}, Source: SourceStore},
		{ID: "entropy", Regex: `a+`, Entropy: 1, Source: SourceStore},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := "first line\nkey " + key + " zzz"
	got := s.Scan("body", text)
	if len(got) != 1 {
		t.Fatalf("findings %+v", got)
	}
	f := got[0]
	if f.RuleID != "aws-access-token" || f.Line != 2 || f.Offset != strings.Index(text, key) || f.Length != len(key) {
		t.Fatalf("finding %+v", f)
	}
	if strings.Contains(f.Preview, key[:9]) || f.MatchHash != HashMatch(key) {
		t.Fatalf("preview leaks or hash wrong: %+v", f)
	}
	row, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	allow := Allow{RowID: row.Any(), Field: "body", RuleID: f.RuleID, MatchHash: f.MatchHash}
	if len(Filter(row.Any(), got, []Allow{allow})) != 0 {
		t.Fatal("allowed finding kept")
	}
	for name, a := range map[string]Allow{
		"other row": {RowID: other.Any(), Field: "body", RuleID: f.RuleID, MatchHash: f.MatchHash},
		"field":     {RowID: row.Any(), Field: "title", RuleID: f.RuleID, MatchHash: f.MatchHash},
		"rule":      {RowID: row.Any(), Field: "body", RuleID: "x", MatchHash: f.MatchHash},
		"hash":      {RowID: row.Any(), Field: "body", RuleID: f.RuleID, MatchHash: f.MatchHash + 1},
	} {
		if len(Filter(row.Any(), got, []Allow{a})) != 1 {
			t.Errorf("%s: partial allow dropped the finding", name)
		}
	}
}

func TestNewRegexScannerRefuses(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]Rule{
		"no id":      {Regex: "x", Source: SourceStore},
		"bad regex":  {ID: "x", Regex: "(", Source: SourceStore},
		"bad source": {ID: "x", Regex: "x", Source: "web"},
	} {
		if _, err := NewRegexScanner([]Rule{r}); !errs.Has(err, errs.ConfigInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestEntropy(t *testing.T) {
	t.Parallel()
	if Entropy("aaaa") != 0 || Entropy("") != 0 || Entropy("ab") != 1 {
		t.Fatal("entropy")
	}
}
