package orgcmd

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// defaultingHost is fakeHost with a default org (DefaultOrger).
type defaultingHost struct {
	fakeHost
	org, why string
	err      error
}

func (d *defaultingHost) DefaultOrg(context.Context, string) (string, string, error) {
	return d.org, d.why, d.err
}

// TestWithDefaultOrg: an empty org takes the host's default and the
// report starts with the line naming it; a named org is used as given; a
// host without a default, or one that refuses, refuses.
func TestWithDefaultOrg(t *testing.T) {
	ctx := context.Background()
	h := &defaultingHost{org: "acme", why: "the only org entry on this machine"}
	var got string
	org := ""
	r, err := withDefaultOrg(ctx, h, OpRefresh, &org, func() (Report, error) {
		got = org
		return Report{Lines: []string{"refreshed"}}, nil
	})
	if err != nil || got != "acme" || len(r.Lines) != 2 || r.Lines[0] != "org acme (the only org entry on this machine)" {
		t.Fatalf("default: org %q, lines %q, err %v", got, r.Lines, err)
	}
	org = "beta"
	r, err = withDefaultOrg(ctx, h, OpRefresh, &org, func() (Report, error) { return Report{Lines: []string{"x"}}, nil })
	if err != nil || len(r.Lines) != 1 || org != "beta" {
		t.Fatalf("named: org %q, lines %q, err %v", org, r.Lines, err)
	}
	org = ""
	_, err = withDefaultOrg(ctx, &fakeHost{}, OpRefresh, &org, func() (Report, error) { return Report{}, nil })
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "lore org refresh <org>") {
		t.Fatalf("no default: %v", err)
	}
	h.err = errs.New(errs.InvalidInput, "name the org")
	ran := false
	_, err = withDefaultOrg(ctx, h, OpLink, &org, func() (Report, error) { ran = true; return Report{}, nil })
	if err == nil || ran {
		t.Fatalf("refused default: err %v, ran %v", err, ran)
	}
}
