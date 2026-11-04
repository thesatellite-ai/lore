package settings_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceListWhereSort: resource list reads the one --where grammar
// (XQ7) and the one --sort grammar (XQ10) over every field kind.
func TestResourceListWhereSort(t *testing.T) {
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"region","type":"text"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"env","type":"text","values":"prod,staging"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"cores","type":"number"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"tags","type":"list"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"pw","type":"text","sensitive":true}`},
		[2]string{settings.OpKindAdd, `{"kind":"db"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"db","field":"host","type":"ref","ref_kind":"host"}`},
	)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"alpha","set":["region=eu-central","env=prod","cores=16","tags=web, edge","pw=s1"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"beta","set":["region=us-east","env=staging","cores=4"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"gamma","set":["region=EU-west","env=prod","cores=8","tags=web"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"db","name":"main","set":["host=gamma"]}`)

	names := func(in string) []string {
		var out settings.ResListOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResList, in))), &out); err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, r := range out.Data {
			n = append(n, r.Name)
		}
		return n
	}
	cases := []struct {
		in   string
		want []string
	}{
		{`{"kind":"host"}`, []string{"alpha", "beta", "gamma"}},
		{`{"kind":"host","where":["env=prod"]}`, []string{"alpha", "gamma"}},
		{`{"kind":"host","where":["env:prod,staging"]}`, []string{"alpha", "beta", "gamma"}},
		{`{"kind":"host","where":["env!=prod"]}`, []string{"beta"}},
		{`{"kind":"host","where":["region~=EU"]}`, []string{"alpha", "gamma"}},
		{`{"kind":"host","where":["cores>4"]}`, []string{"alpha", "gamma"}},
		{`{"kind":"host","where":["cores>=4 cores<16"]}`, []string{"beta", "gamma"}},
		{`{"kind":"host","where":["env=prod","cores>8"]}`, []string{"alpha"}},
		{`{"kind":"host","where":["tags=edge"]}`, []string{"alpha"}},
		{`{"kind":"host","where":["tags?"]}`, []string{"alpha", "gamma"}},
		{`{"kind":"host","where":["tags!?"]}`, []string{"beta"}},
		{`{"kind":"host","where":["has:tags"]}`, []string{"alpha", "gamma"}},
		// a sensitive field matches on its stored value (RES 3.12), printed masked.
		{`{"kind":"host","where":["pw=s1"]}`, []string{"alpha"}},
		// a ref field takes the target's name or its res_ id.
		{`{"kind":"db","where":["host=gamma"]}`, []string{"main"}},
		{`{"kind":"db","where":["host=alpha"]}`, nil},
		{`{"kind":"host","sort":["cores"]}`, []string{"beta", "gamma", "alpha"}},
		{`{"kind":"host","sort":["cores:desc"]}`, []string{"alpha", "gamma", "beta"}},
		{`{"kind":"host","sort":["env:desc","name:desc"]}`, []string{"beta", "gamma", "alpha"}},
		{`{"kind":"host","sort":["name:desc"]}`, []string{"gamma", "beta", "alpha"}},
		{`{"kind":"host","sort":["tags"]}`, []string{"gamma", "alpha", "beta"}},
	}
	for _, c := range cases {
		if got := names(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.in, got, c.want)
		}
	}
	checkRefusals(t, h, []refusal{
		{settings.OpResList, `{"kind":"host","where":["region>eu"]}`, errs.InvalidInput, `">" compares numbers, dates and datetimes; region is text`},
		{settings.OpResList, `{"kind":"host","where":["env=dev"]}`, errs.InvalidInput, `env "dev" is not one of prod, staging`},
		{settings.OpResList, `{"kind":"host","where":["cores=many"]}`, errs.InvalidInput, `"many" is not a number`},
		{settings.OpResList, `{"kind":"host","where":["nope=1"]}`, errs.ResourceInvalid, "unknown field nope (fields: cores, env, pw, region, tags)"},
		{settings.OpResList, `{"kind":"host","where":["meta.env=prod"]}`, errs.ResourceInvalid, "unknown field meta.env"},
		{settings.OpResList, `{"kind":"host","sort":["-cores"]}`, errs.InvalidInput, "a leading - is not a sort direction"},
		{settings.OpResList, `{"kind":"host","sort":["ram"]}`, errs.InvalidInput, "unknown key ram (keys: name, last_checked, archived_at, cores, env, pw, region, tags)"},
	})
	if got := opstest.Text(h.Must(settings.OpResList, `{"kind":"host","where":["pw=s1"]}`)); !strings.Contains(got, "•••") || strings.Contains(got, "s1") {
		t.Errorf("sensitive value printed: %q", got)
	}
}
