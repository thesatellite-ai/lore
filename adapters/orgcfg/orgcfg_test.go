package orgcfg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
)

// writeFile writes body to path, creating its folder.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gitRepo makes a git repo in a temp folder with the given remote ("" =
// none) and project id ("" = no _meta.json).
func gitRepo(t *testing.T, remote, projectID string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		if _, err := gitRun(context.Background(), append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q")
	if remote != "" {
		run("remote", "add", "origin", remote)
	}
	if projectID != "" {
		writeFile(t, filepath.Join(dir, ".lore", "data", "_meta.json"), `{"project_id":"`+projectID+`","_v":2}`)
	}
	return dir
}

// codeOf is the errs code of err ("" when it carries none).
func codeOf(err error) errs.Code {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// TestLoadConfig: absent is an empty valid config; invalid is
// E_CONFIG_INVALID; valid parses.
func TestLoadConfig(t *testing.T) {
	home := t.TempDir()
	c, exists, err := LoadConfig(home)
	if err != nil || exists || c == nil || len(c.Orgs) != 0 {
		t.Fatalf("absent: cfg=%v exists=%v err=%v", c, exists, err)
	}
	writeFile(t, ConfigPath(home), "orgs:\n  - name: Bad Name\n")
	if _, _, lerr := LoadConfig(home); codeOf(lerr) != errs.ConfigInvalid {
		t.Fatalf("invalid: err=%v, want E_CONFIG_INVALID", lerr)
	}
	writeFile(t, ConfigPath(home), "orgs:\n  - name: acme\n")
	c, exists, err = LoadConfig(home)
	if err != nil || !exists || c.FindOrg("acme") == nil {
		t.Fatalf("valid: cfg=%v exists=%v err=%v", c, exists, err)
	}
}

// TestProjectIDAndOrgFile: the committed markers are read; missing ones are
// "" / nil, a bad org.yaml is an error.
func TestProjectIDAndOrgFile(t *testing.T) {
	dir := gitRepo(t, "", "")
	if ProjectID(dir) != "" {
		t.Fatal("ProjectID without _meta.json is not empty")
	}
	if f, err := LoadOrgFile(dir); f != nil || err != nil {
		t.Fatalf("LoadOrgFile absent = %v, %v", f, err)
	}
	dir = gitRepo(t, "", "prj_1")
	if got := ProjectID(dir); got != "prj_1" {
		t.Fatalf("ProjectID = %q", got)
	}
	writeFile(t, filepath.Join(dir, ".lore", "org.yaml"), "org: acme\nproject: web\n")
	f, err := LoadOrgFile(dir)
	if err != nil || f == nil || f.Org != "acme" || f.Project != "web" {
		t.Fatalf("LoadOrgFile = %+v, %v", f, err)
	}
	writeFile(t, filepath.Join(dir, ".lore", "org.yaml"), "project: web\n")
	if _, err := LoadOrgFile(dir); err == nil {
		t.Fatal("an org.yaml without org was accepted")
	}
}

// TestRemotes: remotes are normalised and sorted; a local path is a note.
func TestRemotes(t *testing.T) {
	dir := gitRepo(t, "git@github.com:acme/web.git", "")
	if _, err := gitRun(context.Background(), "-C", dir, "remote", "add", "local", "/srv/repo"); err != nil {
		t.Fatal(err)
	}
	norm, notes := Remotes(context.Background(), dir)
	if len(norm) != 1 || norm[0] != "github.com/acme/web" {
		t.Fatalf("Remotes norm = %v", norm)
	}
	if len(notes) != 1 {
		t.Fatalf("Remotes notes = %v, want one note for the local path", notes)
	}
	if n, _ := Remotes(context.Background(), t.TempDir()); n != nil {
		t.Fatalf("Remotes outside a repo = %v", n)
	}
}

// TestGatherRoles is F10 through Gather: no org (internal), an own:
// pattern (own repo, internal), a repos: pattern (client), LORE_ORG naming
// no entry (refusal, client), and a broken config (error).
func TestGatherRoles(t *testing.T) {
	ctx := context.Background()
	const cfg = "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com}\n    own: [\"github.com/acme/*\"]\n    repos: [\"github.com/client/*\"]\n"
	cases := []struct {
		name, config, remote, env string
		wantOrg                   string
		wantRole                  orgresolve.Role
		wantClient                bool
		wantRefusal               errs.Code
	}{
		{name: "no config", remote: "git@github.com:acme/web.git", wantRole: orgresolve.RoleNone},
		{name: "own repo", config: cfg, remote: "git@github.com:acme/web.git", wantOrg: "acme", wantRole: orgresolve.RoleOwn},
		{name: "client repo", config: cfg, remote: "https://github.com/client/app", wantOrg: "acme", wantRole: orgresolve.RoleClient, wantClient: true},
		{name: "unrelated repo", config: cfg, remote: "git@github.com:else/x.git", wantRole: orgresolve.RoleNone},
		{name: "LORE_ORG unknown", config: cfg, remote: "git@github.com:acme/web.git", env: "nobody", wantClient: true, wantRefusal: errs.InvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.config != "" {
				writeFile(t, ConfigPath(home), tc.config)
			}
			dir := gitRepo(t, tc.remote, "prj_1")
			s, err := Gather(ctx, In{Home: home, Dir: dir, EnvOrg: tc.env})
			if err != nil {
				t.Fatalf("Gather: %v", err)
			}
			client, cerr := s.IsClient()
			if client != tc.wantClient {
				t.Fatalf("IsClient = %v (%v), want %v", client, cerr, tc.wantClient)
			}
			if tc.wantRefusal != "" {
				if s.ResolveErr == nil || codeOf(ResolveError(s.ResolveErr)) != tc.wantRefusal {
					t.Fatalf("ResolveErr = %v, want code %s", s.ResolveErr, tc.wantRefusal)
				}
				return
			}
			if s.ResolveErr != nil {
				t.Fatalf("ResolveErr = %v", s.ResolveErr)
			}
			if s.Result.OrgName() != tc.wantOrg || s.Result.Role != tc.wantRole {
				t.Fatalf("org=%q role=%q, want %q %q", s.Result.OrgName(), s.Result.Role, tc.wantOrg, tc.wantRole)
			}
		})
	}
	home := t.TempDir()
	writeFile(t, ConfigPath(home), "orgs: [\n")
	if _, err := Gather(ctx, In{Home: home, Dir: t.TempDir()}); codeOf(err) != errs.ConfigInvalid {
		t.Fatalf("broken config: err=%v, want E_CONFIG_INVALID", err)
	}
}
