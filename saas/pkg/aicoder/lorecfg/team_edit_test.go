package lorecfg

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pidA = "prj_019a1b2c3d4e7f00a1b2c3d4e5f60718"
const pidB = "prj_019a1b2c3d4e7f00a1b2c3d4e5f60719"

func TestSetRepoOrgKeepsCommentsAndOtherKeys(t *testing.T) {
	body := `# my lore config
orgs:
  - name: acme   # the client
    identity: {key: "human:a@acme.example"}
  - name: beta
repo_orgs:
  ` + pidB + `: beta   # set by hand
`
	dir := writeTeam(t, body)
	ctx := context.Background()
	o := Options{Notices: &bytes.Buffer{}}
	if _, err := SetRepoOrg(ctx, dir, pidA, RepoOrg{Org: "acme", Remotes: []string{"github.com/acme/app"}}, o); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	s := string(raw)
	for _, want := range []string{"# my lore config", "# the client", "# set by hand", pidA, "github.com/acme/app"} {
		if !strings.Contains(s, want) {
			t.Errorf("edited file lacks %q:\n%s", want, s)
		}
	}
	cfg, _, err := LoadTeam(ctx, dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RepoOrgs[pidA].Org != "acme" || cfg.RepoOrgs[pidB].Org != "beta" {
		t.Fatalf("repo_orgs = %+v", cfg.RepoOrgs)
	}

	// set again (overwrite), then remove; run twice to prove idempotence
	for range 2 {
		if _, err := SetRepoOrg(ctx, dir, pidA, RepoOrg{Org: "beta"}, o); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _, _ = LoadTeam(ctx, dir, o)
	if cfg.RepoOrgs[pidA].Org != "beta" || len(cfg.RepoOrgs) != 2 {
		t.Fatalf("after overwrite: %+v", cfg.RepoOrgs)
	}
	removed, err := RemoveRepoOrg(ctx, dir, pidA, o)
	if err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	removed, err = RemoveRepoOrg(ctx, dir, pidA, o)
	if err != nil || removed {
		t.Fatalf("second remove: %v %v", removed, err)
	}
	cfg, _, _ = LoadTeam(ctx, dir, o)
	if _, ok := cfg.RepoOrgs[pidA]; ok || cfg.RepoOrgs[pidB].Org != "beta" {
		t.Fatalf("after remove: %+v", cfg.RepoOrgs)
	}
}

func TestSetRepoOrgRefusesInvalid(t *testing.T) {
	body := "orgs:\n  - name: acme\n"
	dir := writeTeam(t, body)
	ctx := context.Background()
	o := Options{Notices: &bytes.Buffer{}}
	if _, err := SetRepoOrg(ctx, dir, pidA, RepoOrg{Org: "nobody"}, o); err == nil {
		t.Fatal("unknown org accepted")
	}
	if _, err := SetRepoOrg(ctx, dir, "not-an-id", RepoOrg{Org: "acme"}, o); err == nil {
		t.Fatal("bad project id accepted")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if string(raw) != body {
		t.Fatalf("file changed by a refused edit:\n%s", raw)
	}
}

func TestSetRepoOrgCreatesFileWithPrivatePerm(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".lore")
	// No orgs at all → an org name cannot be valid; the refusal must not
	// create a file either.
	if _, err := SetRepoOrg(context.Background(), dir, pidA, RepoOrg{Org: "acme"}, Options{}); err == nil {
		t.Fatal("accepted an org that no entry defines")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("file created by a refused edit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("orgs: [{name: acme}]\nrepo_orgs: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	p, err := SetRepoOrg(context.Background(), dir, pidA, RepoOrg{Org: "acme"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o640 {
		t.Errorf("perm = %v, want the file's own 0640 kept", info.Mode().Perm())
	}
}

func TestOrgFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f, err := LoadOrgFile(ctx, dir, Options{})
	if err != nil || f != nil {
		t.Fatalf("absent: %v %v", f, err)
	}
	if _, err := WriteOrgFile(ctx, dir, &OrgFile{Org: "northwind", Project: "lore-tools"}, Options{}); err != nil {
		t.Fatal(err)
	}
	f, err = LoadOrgFile(ctx, dir, Options{})
	if err != nil || f.Org != "northwind" || f.Project != "lore-tools" {
		t.Fatalf("read back: %+v %v", f, err)
	}
	for _, bad := range []OrgFile{
		{Org: ""},
		{Org: "git@github.com:northwind/lore"}, // a URL is never a name
		{Org: "northwind", Project: "_root"},
		{Org: "northwind", Project: "a/b"},
	} {
		if _, err := WriteOrgFile(ctx, dir, &bad, Options{}); err == nil {
			t.Errorf("WriteOrgFile accepted %+v", bad)
		}
	}
	// a hand-written file with a URL as org is refused on read too
	if err := os.WriteFile(filepath.Join(dir, "org.yaml"), []byte("org: https://github.com/x/y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrgFile(ctx, dir, Options{}); err == nil {
		t.Error("LoadOrgFile accepted a URL as org name")
	}
}

func TestSetOrgIdentity(t *testing.T) {
	body := "# keep me\norgs:\n  - name: acme # client\n    stores: [{url: git@github.com:acme/lore}]\n  - name: beta\n    identity: {key: \"human:old@beta.example\"}\n"
	dir := writeTeam(t, body)
	ctx := context.Background()
	o := Options{Notices: &bytes.Buffer{}}
	id := OrgIdentity{Key: "human:alice@acme.example", Name: "Alice Doe", Handle: "alice"}
	if _, err := SetOrgIdentity(ctx, dir, "acme", id, o); err != nil {
		t.Fatal(err)
	}
	if _, err := SetOrgIdentity(ctx, dir, "beta", OrgIdentity{Key: "human:new@beta.example"}, o); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadTeam(ctx, dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FindOrg("acme").Identity != id || cfg.FindOrg("beta").Identity.Key != "human:new@beta.example" {
		t.Fatalf("identities: %+v / %+v", cfg.FindOrg("acme").Identity, cfg.FindOrg("beta").Identity)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if !strings.Contains(string(raw), "# keep me") || !strings.Contains(string(raw), "# client") {
		t.Errorf("comments lost:\n%s", raw)
	}
	before := string(raw)
	if _, err := SetOrgIdentity(ctx, dir, "ghost", id, o); err == nil {
		t.Error("identity for an unknown org accepted")
	}
	if _, err := SetOrgIdentity(ctx, dir, "acme", OrgIdentity{Key: "human:no-email"}, o); err == nil {
		t.Error("invalid key accepted")
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "config.yaml"))
	if string(raw) != before {
		t.Error("a refused edit changed the file")
	}
}
