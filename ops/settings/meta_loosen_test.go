package settings

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestCheckKeyLoosening(t *testing.T) {
	t.Parallel()
	head := `{"name":"margin_pct","internal":true,"strict":true,"changed_by":[{"id":"the_1","field":"internal","to":false,"at":"x"}]}`
	cases := []struct {
		name, staged string
		blocked      bool
	}{
		{"unchanged", head, false},
		{"internal dropped, no record", `{"name":"margin_pct","internal":false,"strict":true,"changed_by":[{"id":"the_1","field":"internal","to":false,"at":"x"}]}`, true},
		{"internal dropped, old record only", `{"name":"margin_pct","strict":true,"changed_by":[{"id":"the_1","field":"internal","to":false,"at":"x"}]}`, true},
		{"internal dropped, new record", `{"name":"margin_pct","strict":true,"changed_by":[{"id":"the_1","field":"internal","to":false,"at":"x"},{"id":"the_2","field":"internal","to":false,"at":"y"}]}`, false},
		{"strict dropped, record for another field", `{"name":"margin_pct","internal":true,"changed_by":[{"id":"the_1","field":"internal","to":false,"at":"x"},{"id":"the_2","field":"internal","to":false,"at":"y"}]}`, true},
		{"tightened", `{"name":"margin_pct","internal":true,"strict":true,"sensitive":true}`, false},
		{"undecodable staged", `<<<<<<<`, false},
	}
	for _, c := range cases {
		err := CheckKeyLoosening("meta_keys/mky_x.json", []byte(head), []byte(c.staged))
		if got := errs.Has(err, errs.LeakBlocked); got != c.blocked {
			t.Errorf("%s: blocked=%v (err %v), want %v", c.name, got, err, c.blocked)
		}
	}
}
