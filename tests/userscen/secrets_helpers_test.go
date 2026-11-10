//go:build userscen

package userscen

// Helpers of the secrets area (secrets, vars, metadata, secret rules,
// config): a one-person repo, an org laptop with an own repo and a client
// repo, and secret-shaped fixture values built at run time so no literal
// key ever sits in this file (the repo's own commit hook would refuse it).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// secPerson is the one person of the solo scenarios (placeholder identity).
const (
	secPersonName  = "person"
	secPersonEmail = "person@example.com"
)

// Org fixture of the client-repo scenarios: alice works for northwind; its
// own repos are github.com/northwind/*, its client repos github.com/acme/*.
const (
	secOrg        = "northwind"
	secOrgKey     = "human:alice@northwind.example"
	secSlugStore  = "github.com/northwind/lore"
	secSlugOwn    = "github.com/northwind/tools"
	secSlugClient = "github.com/acme/app"
	secOrgFolder  = "org"
)

// secSolo is one person, one code repo with a lore project (no org).
type secSolo struct {
	w *th.World
	a *th.Actor
	r *th.Remote
	c *th.Clone
}

// newSecSolo builds the solo repo: clone of an empty remote, `lore init`.
func newSecSolo(t *testing.T) *secSolo {
	t.Helper()
	scenParallel(t)
	w := newWorld(t)
	a := w.Human(secPersonName, secPersonEmail)
	r := w.Remote("github.com/octo-personal/web")
	c := a.Clone(r, "web")
	c.MustLore("init", "--non-interactive", "--name=web")
	return &secSolo{w: w, a: a, r: r, c: c}
}

// secOrgLaptop is alice's laptop with the northwind org: an own repo
// (tools) and a client repo (acme/app), both with a lore project, and the
// org store initialised.
type secOrgLaptop struct {
	w      *th.World
	alice  *th.Actor
	own    *th.Clone
	client *th.Clone
	ownR   *th.Remote
	cliR   *th.Remote
}

// newSecOrgLaptop builds the org laptop the way a person sets it up: the
// config entry, `lore store init` of the org store, a clone and `lore init`
// per repo, pushed so the repo has a main.
func newSecOrgLaptop(t *testing.T) *secOrgLaptop {
	t.Helper()
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "alice@northwind.example")
	alice.WriteConfig(th.Config{
		Personal: &th.Personal{Store: th.PersonalLocal},
		Orgs: []th.Org{{
			Name:     secOrg,
			Identity: th.Identity{Key: secOrgKey, Name: "Alice Doe", Handle: "alice"},
			Stores:   []th.Store{{URL: "https://" + secSlugStore, OrgFolder: secOrgFolder}},
			Own:      []string{"github.com/northwind/*"},
			Repos:    []string{"github.com/acme/*"},
		}},
		Live: th.FreshFetch(),
	})
	w.Remote(secSlugStore)
	dir := filepath.Join(alice.Home, "store-init", "northwind_lore")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	w.Git(dir, "init", "-q", "-b", "main")
	alice.Lore(dir, "init", "--non-interactive", "--name=northwind_lore").OK(t)
	alice.Lore(dir, "store", "init", "northwind_lore", "https://"+secSlugStore, "--yes").OK(t)
	o := &secOrgLaptop{w: w, alice: alice}
	mk := func(slug, name string) (*th.Clone, *th.Remote) {
		r := w.Remote(slug)
		c := alice.Clone(r, name)
		c.MustLore("init", "--non-interactive", "--name="+name)
		c.CommitAll("lore")
		c.AdminPush("origin", "main")
		return c, r
	}
	o.own, o.ownR = mk(secSlugOwn, "tools")
	o.client, o.cliR = mk(secSlugClient, "app")
	return o
}

// secFixture is one secret-shaped value and the rule lore names for it.
type secFixture struct {
	rule  string
	value string
}

// secAWSKey is the public AWS documentation sample key, split so the
// literal never appears in source.
func secAWSKey() string { return "AKIA" + "IOSFODNN7EXAMPLE" }

// secFixtures is one value per default rule of lore's pack (gitleaks
// pack.go), each built from pieces at run time.
func secFixtures() []secFixture {
	return []secFixture{
		{"aws_access_key", secAWSKey()},
		{"github_token", "gh" + "p_" + strings.Repeat("a1B2", 9)},
		{"jwt", "ey" + "JhbGciOiJIUzI1NiJ9" + ".ey" + "JzdWIiOiIxIn0" + ".c2lnbmF0dXJl"},
		{"stripe_live_key", "sk_" + "live_" + "abcdefghijklmnopqrstuvwx12"},
		{"model_key", "sk" + "-proj-" + "abcdefghijklmnopqrstuv"},
		{"slack_token", "xo" + "xb-" + "1234567890-abcdefghij"},
		{"google_api_key", "AI" + "za" + "SyA1234567890abcdefghijklmnopqrstuv"},
		{"pem_private_key", "-----BEGIN " + "RSA PRIVATE " + "KEY-----"},
		{"shopify_token", "shp" + "at_" + "0123456789abcdef0123456789abcdef"},
		{"npm_token", "np" + "m_" + "abcdefghijklmnopqrstuvwxyz0123456789"},
		{"twilio_key", "S" + "K" + "0123456789abcdef0123456789abcdef"},
		{"authorization_bearer", "Authorization: " + "Bearer " + "abcdef0123456789xyz"},
		{"password_pair", "pass" + "word=" + "Xk9mQ2vL7pZ"},
		{"url_with_password", "postgres://app:" + "s3cr3tPw9" + "@db.example.com/app"},
		{"url_token_user", "https://" + "gh" + "p_tokenvalue1234" + "@github.com/x/y"},
	}
}

// secTail is the part of a secret that no masked preview may show: the
// previews keep at most the first eight characters.
func secTail(v string) string {
	if len(v) <= 10 {
		return v
	}
	return v[8:]
}

// secErr is the --json error envelope fields the secrets scenarios read.
type secErr struct {
	Error struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Hint     string `json:"hint"`
		Exit     int    `json:"exit"`
		Class    string `json:"class"`
		Question string `json:"question"`
		Next     struct {
			AddArgsOneOf [][]string `json:"add_args_one_of"`
		} `json:"next"`
		Findings []struct {
			N      int    `json:"n"`
			Rule   string `json:"rule"`
			Field  string `json:"field"`
			Line   int    `json:"line"`
			Masked string `json:"masked"`
		} `json:"findings"`
	} `json:"error"`
}

// secDecodeErr decodes a failed --json command's error envelope.
func secDecodeErr(t *testing.T, r th.Result) secErr {
	t.Helper()
	if r.Code == 0 {
		t.Fatalf("expected a failure: %s", r)
	}
	var e secErr
	body := strings.TrimSpace(r.Stdout)
	if body == "" {
		body = strings.TrimSpace(r.Stderr)
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("decode error JSON from %s: %v", r, err)
	}
	return e
}

// secID is the id of a --json add ({"id": …} or {"data": {"id": …}}).
func secID(t *testing.T, r th.Result) string {
	t.Helper()
	r.OK(t)
	var v struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("decode id from %s: %v", r, err)
	}
	if v.ID != "" {
		return v.ID
	}
	if v.Data.ID == "" {
		t.Fatalf("no id in %s", r)
	}
	return v.Data.ID
}

// secDataFiles lists every file under the clone's .lore/data (what a
// commit would carry).
func secDataFiles(t *testing.T, c *th.Clone) []string {
	t.Helper()
	var out []string
	root := filepath.Join(c.Dir, ".lore", "data")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// secDataHolding names the .lore/data files whose text holds needle.
func secDataHolding(t *testing.T, c *th.Clone, needle string) []string {
	t.Helper()
	var hit []string
	for _, p := range secDataFiles(t, c) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), needle) {
			hit = append(hit, p)
		}
	}
	return hit
}
