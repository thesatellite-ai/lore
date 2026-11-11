package secretmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// scriptMode lets the owner run a fake CLI script.
const scriptMode = 0o700

// fakeValue is a dummy value the fake CLIs print (never a real secret).
const fakeValue = "fake-sunflower-tractor-42"

// fakeCLIs writes shell scripts named after vendor CLIs into a temp dir
// and returns a LookPath that finds only those (a real manager is never
// called).
func fakeCLIs(t *testing.T, scripts map[string]string) (func(string) (string, error), string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		// owner-only execute: the fake CLI must run, and nobody else may
		// (name.exe on Windows, tests/fakeexec)
		fakeexec.Write(t, filepath.Join(dir, name), "#!/bin/sh\n"+body+"\n", scriptMode)
	}
	return func(name string) (string, error) {
		p := filepath.Join(dir, fakeexec.Exe(name))
		if _, err := os.Stat(p); err != nil {
			return "", err
		}
		return p, nil
	}, dir
}

func TestSecretNeverPrints(t *testing.T) {
	s := NewSecret([]byte(fakeValue))
	for _, got := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s %q %x", s, s, s, s, s, s), s.String(), s.GoString(),
		fmt.Errorf("wrapped: %v", s).Error()} {
		if strings.Contains(got, fakeValue) {
			t.Fatalf("value leaked: %q", got)
		}
	}
	j, _ := json.Marshal(map[string]Secret{"v": s})
	if strings.Contains(string(j), fakeValue) {
		t.Fatalf("value leaked in JSON: %s", j)
	}
	if s.Expose() != fakeValue {
		t.Fatal("Expose lost the value")
	}
}

func TestRevealMatrix(t *testing.T) {
	args := map[string]string{}
	look, dir := fakeCLIs(t, map[string]string{
		"gopass": `[ "$3" = "acme/missing" ] && { echo "entry is not in the password store" >&2; exit 1; }; [ "$3" = "acme/locked" ] && { echo "gpg: decryption failed: ` + fakeValue + `" >&2; exit 2; }; echo "$*" > "$(dirname "$0")/gopass.args"; printf '` + fakeValue + `\n'`,
		"op":     `echo "$*" > "$(dirname "$0")/op.args"; echo ` + fakeValue,
		"vault":  `echo "$*" > "$(dirname "$0")/vault.args"; echo ` + fakeValue,
		"aws":    `echo "$*" > "$(dirname "$0")/aws.args"; echo '{"db_password":"` + fakeValue + `","port":5432}'`,
		"gcloud": `echo "$*" > "$(dirname "$0")/gcloud.args"; echo ` + fakeValue,
		"az":     `echo "$*" > "$(dirname "$0")/az.args"; echo ` + fakeValue,
		"bw":     `if [ "$2" = "item" ]; then echo '{"fields":[{"name":"api","value":"` + fakeValue + `"}]}'; else echo "$*" > "$(dirname "$0")/bw.args"; echo ` + fakeValue + `; fi`,
		"slow":   `sleep 5`,
	})
	set := Builtin(Options{LookPath: look, Getenv: func(k string) string {
		if k == "HUB_DB" {
			return fakeValue
		}
		return ""
	}})
	cases := []struct {
		mgr      string
		loc      Locator
		wantArgs string
	}{
		{Gopass, Locator{Path: "acme/hub/db"}, "show -o acme/hub/db"},
		{OnePass, Locator{Path: "Acme/Hub Stripe/secret key"}, "read op://Acme/Hub Stripe/secret key"},
		{Vault, Locator{Path: "secret/acme/hub", Fragment: "db_password"}, "kv get -field=db_password secret/acme/hub"},
		{AWS, Locator{Path: "acme/hub", Fragment: "db_password", Options: []Option{{"region", "eu-central-1"}}}, "secretsmanager get-secret-value --secret-id acme/hub --query SecretString --output text --region eu-central-1"},
		{GCP, Locator{Path: "hub-db", Options: []Option{{"project", "acme"}}}, "secrets versions access latest --secret=hub-db --project=acme"},
		{Azure, Locator{Path: "acmekv/hub-db"}, "keyvault secret show --vault-name acmekv --name hub-db --query value -o tsv"},
		{Bitwarden, Locator{Path: "Hub staging/password"}, "get password Hub staging"},
		{Bitwarden, Locator{Path: "Hub staging/api"}, ""},
		{Env, Locator{Path: "HUB_DB"}, ""},
	}
	for _, c := range cases {
		m, _ := set.Get(c.mgr)
		s, err := m.Reveal(context.Background(), c.loc)
		if err != nil {
			t.Fatalf("%s %v: %v", c.mgr, c.loc, err)
		}
		if s.Expose() != fakeValue {
			t.Fatalf("%s: got a different value (len %d)", c.mgr, len(s.Expose()))
		}
		if c.wantArgs != "" {
			bin := map[string]string{Gopass: "gopass", OnePass: "op", Vault: "vault", AWS: "aws", GCP: "gcloud", Azure: "az", Bitwarden: "bw"}[c.mgr]
			b, _ := os.ReadFile(filepath.Clean(filepath.Join(dir, filepath.Base(bin)+".args")))
			args[c.mgr] = strings.TrimSpace(string(b))
			if args[c.mgr] != c.wantArgs {
				t.Fatalf("%s args = %q, want %q", c.mgr, args[c.mgr], c.wantArgs)
			}
		}
	}

	gp, _ := set.Get(Gopass)
	_, err := gp.Reveal(context.Background(), Locator{Path: "acme/missing"})
	if !errors.Is(err, ErrMissing) || !errs.Has(err, errs.VaultEntryUnavailable) {
		t.Fatalf("missing entry: %v", err)
	}
	_, err = gp.Reveal(context.Background(), Locator{Path: "acme/locked"})
	if err == nil || errors.Is(err, ErrMissing) || strings.Contains(err.Error(), fakeValue) || strings.Contains(err.Error(), "gpg") {
		t.Fatalf("locked: want unavailable without stderr, got %v", err)
	}
	en, _ := set.Get(Env)
	if _, err = en.Reveal(context.Background(), Locator{Path: "NOPE"}); !errors.Is(err, ErrMissing) {
		t.Fatalf("env missing: %v", err)
	}
}

func TestRevealUnavailable(t *testing.T) {
	// The fake would run a minute; the reveal must end long before. The
	// bound is loose because starting a freshly written program can take
	// seconds on Windows (the virus scanner reads it first).
	look, _ := fakeCLIs(t, map[string]string{"gopass": "sleep 60"})
	set := Builtin(Options{LookPath: look, Timeout: 200 * time.Millisecond})
	gp, _ := set.Get(Gopass)
	start := time.Now()
	_, err := gp.Reveal(context.Background(), Locator{Path: "a/b"})
	if !errs.Has(err, errs.VaultEntryUnavailable) || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 30*time.Second {
		t.Fatalf("timeout: %v after %s", err, time.Since(start))
	}
	op, _ := set.Get(OnePass)
	if err = op.Available(context.Background()); !errs.Has(err, errs.VaultEntryUnavailable) {
		t.Fatalf("op not on PATH: %v", err)
	}
	if _, err = op.Reveal(context.Background(), Locator{Path: "a/b/c"}); !errs.Has(err, errs.VaultEntryUnavailable) {
		t.Fatalf("op reveal not on PATH: %v", err)
	}
}

func TestValidateLocator(t *testing.T) {
	set := Builtin(Options{})
	bad := map[string]Locator{
		Vault:  {Path: "secret/x"}, // no #key
		Azure:  {Path: "only-one"}, // no vault/name
		Gopass: {Path: "-rf"},      // reads as a flag
		Env:    {Path: "A/B"},      // not a variable name
		AWS:    {Path: ""},         // empty
	}
	for mgr, l := range bad {
		m, _ := set.Get(mgr)
		if err := m.ValidateLocator(l); !errs.Has(err, errs.RefSyntax) {
			t.Fatalf("%s %v: want E_REF_SYNTAX, got %v", mgr, l, err)
		}
	}
	if got := len(set.Names()); got != 8 {
		t.Fatalf("managers = %d, want 8", got)
	}
}
