package orgcmd

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestConfigSetList: --value replaces, --add appends what is missing,
// --remove takes out; a bad key, no change or a bad pattern is refused
// before anything is read (devco D-453).
func TestConfigSetList(t *testing.T) {
	cur := []string{"github.com/acme/*", "github.com/acme/old-*"}
	for _, c := range []struct {
		in   ConfigSetIn
		want []string
	}{
		{ConfigSetIn{Add: []string{"github.com/acme/client-*", "github.com/acme/*"}}, []string{"github.com/acme/*", "github.com/acme/old-*", "github.com/acme/client-*"}},
		{ConfigSetIn{Remove: []string{"github.com/acme/old-*"}}, []string{"github.com/acme/*"}},
		{ConfigSetIn{Values: []string{"github.com/x/*", "github.com/x/*"}}, []string{"github.com/x/*"}},
		{ConfigSetIn{Values: []string{"github.com/x/*"}, Add: []string{"github.com/y/*"}, Remove: []string{"github.com/x/*"}}, []string{"github.com/y/*"}},
	} {
		if got := NewList(cur, c.in); !slices.Equal(got, c.want) {
			t.Errorf("%+v: %v, want %v", c.in, got, c.want)
		}
	}
	if cur[1] != "github.com/acme/old-*" {
		t.Fatal("NewList changed its input")
	}
	for _, in := range []ConfigSetIn{
		{Key: "defaults", Add: []string{"github.com/acme/*"}},
		{Key: ConfigKeyRepos},
		{Key: ConfigKeyRepos, Add: []string{"git*.com/acme/x"}},
		{Key: ConfigKeyOwn, Values: []string{"github.com/acme/x**"}},
	} {
		if err := CheckConfigSet(in); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%+v: %v, want E_INVALID_INPUT", in, err)
		}
	}
	if err := CheckConfigSet(ConfigSetIn{Key: ConfigKeyRepos, Add: []string{"github.com/acme/client-*"}}); err != nil {
		t.Fatalf("a partial pattern refused: %v", err)
	}
}
