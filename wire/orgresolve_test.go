package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// orgTestRepo is a git repo with remote, plus a lore home whose
// config.yaml is config ("" = none).
func orgTestRepo(t *testing.T, remote, config string) (home, dir string) {
	t.Helper()
	home, dir = t.TempDir(), t.TempDir()
	if config != "" {
		if err := os.WriteFile(orgcfg.ConfigPath(home), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", remote}} {
		if _, err := (gitRun{dir: dir, args: args}).output(context.Background()); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".lore", "data", "_meta.json"), []byte(`{"project_id":"prj_1","_v":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, dir
}

// orgTestConfig: acme owns github.com/acme/* and serves github.com/client/*
// (no stores: links never decide, patterns do).
const orgTestConfig = "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com, name: A, handle: a}\n    own: [\"github.com/acme/*\"]\n    repos: [\"github.com/client/*\"]\n"

// TestStandaloneOrgHooks: the standalone binary resolves orgs with the
// core's resolution (no E_UNSUPPORTED any more, FA-OLDCODE-1): no org is
// internal with no org identity; the org's own repo is internal with the
// org identity; a client repo is client; LORE_ORG naming no entry is a
// refusal and answers client (fail closed).
func TestStandaloneOrgHooks(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, remote, config, env string
		wantOrg                   string
		wantClient                bool
		wantErr                   errs.Code
	}{
		{name: "no org", remote: "git@github.com:acme/web.git"},
		{name: "own repo", remote: "git@github.com:acme/web.git", config: orgTestConfig, wantOrg: "acme"},
		{name: "client repo", remote: "https://github.com/client/app", config: orgTestConfig, wantOrg: "acme", wantClient: true},
		{name: "LORE_ORG unknown", remote: "git@github.com:acme/web.git", config: orgTestConfig, env: "nobody", wantClient: true, wantErr: errs.InvalidInput},
		{name: "broken config", remote: "git@github.com:acme/web.git", config: "orgs: [\n", wantClient: true, wantErr: errs.ConfigInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(orgcfg.EnvOrg, tc.env)
			home, dir := orgTestRepo(t, tc.remote, tc.config)
			o := StandaloneOptions(dir, home, dir, "test")
			org, oerr := o.Org(ctx)
			client, cerr := o.RepoIsClient(ctx)
			if client != tc.wantClient {
				t.Fatalf("RepoIsClient = %v (%v), want %v", client, cerr, tc.wantClient)
			}
			if tc.wantErr != "" {
				if !errs.Has(oerr, tc.wantErr) {
					t.Fatalf("Org err = %v, want %s", oerr, tc.wantErr)
				}
				return
			}
			if oerr != nil || errs.Has(cerr, errs.Unsupported) {
				t.Fatalf("Org err = %v, RepoIsClient err = %v", oerr, cerr)
			}
			got := ""
			if org != nil {
				got = org.Name
				if org.Identity.Key != "human:a@acme.com" {
					t.Fatalf("org identity = %+v", org.Identity)
				}
			}
			if got != tc.wantOrg {
				t.Fatalf("Org = %q, want %q", got, tc.wantOrg)
			}
		})
	}
}

// TestOrgResolutionApplyKeepsSetHooks: a hook the embedder already set
// wins; unset hooks are filled.
func TestOrgResolutionApplyKeepsSetHooks(t *testing.T) {
	mine := func(context.Context) (*identity.Org, error) { return &identity.Org{Name: "mine"}, nil }
	o := Options{Org: mine}
	NewOrgResolution(t.TempDir(), t.TempDir(), nil).Apply(&o)
	got, err := o.Org(context.Background())
	if err != nil || got == nil || got.Name != "mine" {
		t.Fatalf("Org = %+v, %v; the preset hook was replaced", got, err)
	}
	if o.RepoIsClient == nil || o.OrgStores == nil || o.OrgEntryStore == nil || o.OrgSigners == nil || o.DefaultTarget == nil || o.GuardRepoWrite == nil {
		t.Fatal("Apply left an org hook unset")
	}
}

// TestGuardRepoWriteClientRepo is F14 in a client repo with no linked
// project: a memory without --to is refused naming --to (internal
// knowledge never lands in a client repo, D8); `--to repo` is accepted and runs the
// F16 write check with the repo root and the row's kind; a kind F14 does
// not route is never checked.
func TestGuardRepoWriteClientRepo(t *testing.T) {
	ctx := context.Background()
	home, dir := orgTestRepo(t, "https://github.com/client/app", orgTestConfig)
	r := StandaloneOrgResolution(home, dir)
	var checked []string
	r.RepoWriteCheck = func(_ context.Context, root, kind, text string) error {
		checked = append(checked, root+"|"+kind+"|"+text)
		return nil
	}
	q := WriteQuery{Table: schema.TableMemories, Text: "a note"}
	// No project linked: F14 refuses both the routed default and the
	// write, naming --to (v0.1.17's ErrNoLinkedProject).
	if _, err := r.DefaultTarget(ctx, q); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("DefaultTarget err = %v, want E_INVALID_INPUT", err)
	}
	if err := r.GuardRepoWrite(ctx, q, false); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("a memory written into a client repo without --to: err = %v, want E_INVALID_INPUT", err)
	}
	if len(checked) != 0 {
		t.Fatalf("the leak check ran for a refused write: %v", checked)
	}
	if err := r.GuardRepoWrite(ctx, q, true); err != nil {
		t.Fatalf("--to repo: %v", err)
	}
	if len(checked) != 1 || checked[0] != dir+"|memory|a note" {
		t.Fatalf("leak check calls = %v", checked)
	}
	if err := r.GuardRepoWrite(ctx, WriteQuery{Table: row.Table("nonexistent_kind"), Text: "x"}, false); err != nil {
		t.Fatalf("unrouted kind: %v", err)
	}
	if r.Routed() != "" {
		t.Fatalf("Routed = %q with nothing routed", r.Routed())
	}
}
