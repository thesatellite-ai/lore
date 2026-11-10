package value

import (
	"strings"
	"testing"
)

// TestSecretRefOf: entry names pass, secrets and malformed refs fail.
func TestSecretRefOf(t *testing.T) {
	pw := "p" // built at run time so no literal user:password url sits in the source
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"gopass://acme/hub_prod/ssh_deploy", true},
		{"aws-secrets://prod/db?region=eu-central-1", true},
		{"op://vault/item?holder=mch_1&label=mira.laptop", true},
		{"env://HUB_TOKEN", true},
		{"", false},
		{"acme/hub_prod/ssh", false},                  // no manager (ops adds the default first)
		{"gopass://", false},                          // no entry
		{"gopass://acme/hub prod", false},             // space
		{"gopass://acme/aB3dE5fG7hJ9kL2mN4pQ", false}, // random-looking part
		{"mailto:x@example.com", false},               // opaque
		{"gopass://u:" + pw + "@acme/x", false},       // user info
		{"Gopass://acme/x", false},                    // scheme case
		{"gopass://" + strings.Repeat("a/", 100), false},
	} {
		v, err := SecretRefOf(c.in)
		if (err == nil) != c.ok {
			t.Errorf("%q: err=%v want ok=%v", c.in, err, c.ok)
			continue
		}
		if err != nil && strings.Contains(err.Error(), c.in) && c.in != "" {
			t.Errorf("%q: error repeats the input", c.in)
		}
		if c.ok && v.String() != c.in {
			t.Errorf("%q round trip %q", c.in, v.String())
		}
	}
	v, _ := SecretRefOf("aws-secrets://prod/db")
	if v.Manager() != "aws-secrets" {
		t.Errorf("manager %q", v.Manager())
	}
}
