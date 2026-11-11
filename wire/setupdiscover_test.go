package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/ghapi"
	"github.com/thesatellite-ai/lore/adapters/probe"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// Fake tokens, built at run time (no key-shaped literal in the source).
func fakeTokens() map[string]string {
	p := strings.Repeat("Z", 4)
	return map[string]string{
		"mira":     "gho_" + p + "MIRA" + p + "secret1",
		"acme-bot": "gho_" + p + "ACME" + p + "secret2",
		"revoked":  "gho_" + p + "DEAD" + p + "secret3",
	}
}

// discoverWorld is a temp HOME with gh logged in as three users (gh is a
// fake script printing each user's token), a git global identity, one
// includeIf identity, and a fake GitHub API that answers only with the
// right token. acme-bot's token lacks user:email; revoked's token gets 401
// with the token echoed in the body (a hostile/buggy server).
func discoverWorld(t *testing.T) (*probe.Prober, ghapi.Client, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	toks := fakeTokens()
	root := t.TempDir()
	home, bin := filepath.Join(root, "home"), filepath.Join(root, "bin")
	for _, d := range []string{home, bin, filepath.Join(home, ".config", "gh")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hosts := "github.com:\n  users:\n    mira:\n    acme-bot:\n    revoked:\n    nogh:\n  user: mira\n"
	writeT(t, filepath.Join(home, ".config", "gh", "hosts.yml"), hosts)
	// gh auth token --hostname github.com --user U; "nogh" fails and
	// prints a token-looking line to stderr (must never surface).
	gh := "#!/bin/sh\ncase \"$6\" in\n"
	for u, tok := range toks {
		gh += u + ") echo " + tok + ";;\n"
	}
	gh += "*) echo \"no token, leaked " + toks["mira"] + "\" >&2; exit 1;;\nesac\n"
	fakeexec.Write(t, filepath.Join(bin, "gh"), gh, 0o755)
	writeT(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = Mira Doe\n\temail = mira@personal.example\n[includeIf \"gitdir:~/work/acme/\"]\n\tpath = ~/.gitconfig-acme\n")
	writeT(t, filepath.Join(home, ".gitconfig-acme"), "[user]\n\tname = Mira Doe\n\temail = acme@acme.example\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		login := ""
		for u, tok := range toks {
			if auth == "Bearer "+tok {
				login = u
			}
		}
		if r.URL.Path == "/avatar" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG fake"))
			return
		}
		if login == "" || login == "revoked" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials ` + auth + `"}`))
			return
		}
		switch r.URL.Path {
		case "/user":
			scopes := "repo, user:email"
			if login == "acme-bot" {
				scopes = "repo, read:org"
			}
			w.Header().Set("X-OAuth-Scopes", scopes)
			_ = json.NewEncoder(w).Encode(map[string]string{"login": login, "name": strings.ToUpper(login), "avatar_url": "http://" + r.Host + "/avatar?u=" + login})
		case "/user/emails":
			if login == "acme-bot" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found ` + auth + `"}`))
				return
			}
			_, _ = w.Write([]byte(`[{"email":"mira@personal.example","primary":true,"verified":true},{"email":"mira@other.example","verified":true}]`))
		case "/user/repos":
			_, _ = w.Write([]byte(`[{"full_name":"` + login + `/lore-me","name":"lore-me","ssh_url":"git@github.com:` + login + `/lore-me.git","private":true},{"full_name":"` + login + `/site","name":"site","ssh_url":"git@github.com:` + login + `/site.git"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home,
		"GIT_CONFIG_NOSYSTEM=1", "XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}
	lookPath := func(name string) (string, error) {
		if p := fakeexec.Exe(filepath.Join(bin, name)); discoverIsFile(p) { // gh.exe on Windows
			return p, nil
		}
		return exec.LookPath(name)
	}
	p := probe.New(probe.Options{Home: home, TmpDir: root, Env: env, LookPath: lookPath})
	return p, ghapi.Client{BaseURL: srv.URL}, toks
}

func writeT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func discoverIsFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// discoverFacts reads every login with its own token and the API's
// answers; one login without user:email is flagged, a revoked token and a
// login gh has no token for are fixed-text failures.
func TestDiscoverFacts(t *testing.T) {
	t.Parallel()
	p, api, _ := discoverWorld(t)
	f, err := discoverFacts(context.Background(), p, api, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]opssetup.GHAccountFacts{}
	for _, a := range f.GH {
		by[a.Login] = a
	}
	if m := by["mira"]; m.Status != "ok" || !m.Active || m.Name != "MIRA" || m.EmailScope != opssetup.EmailScopeOK ||
		len(m.Emails) != 2 || !m.Emails[0].Primary || len(m.Repos) != 1 || m.Repos[0].FullName != "mira/lore-me" ||
		!strings.HasPrefix(m.Avatar, "data:image/png;base64,") {
		t.Errorf("mira = %+v", m)
	}
	if a := by["acme-bot"]; a.Status != "ok" || a.EmailScope != opssetup.EmailScopeMissing || len(a.Emails) != 0 {
		t.Errorf("acme-bot = %+v", a)
	}
	if r := by["revoked"]; r.Status != string(probe.ClassAuthDenied) || r.Detail != detailAPIDenied {
		t.Errorf("revoked = %+v", r)
	}
	if n := by["nogh"]; n.Status != string(probe.ClassNotLoggedIn) {
		t.Errorf("nogh = %+v", n)
	}
	if f.GitGlobal.Email != "mira@personal.example" || len(f.GitIncludes) != 1 || f.GitIncludes[0].Folder != "~/work/acme/" || f.GitIncludes[0].Email != "acme@acme.example" {
		t.Errorf("git = %+v %+v", f.GitGlobal, f.GitIncludes)
	}
}

// The gh tokens never appear in anything discovery hands up: the facts
// (as JSON and as %+v), the merged op output, or anything logged while it
// runs — including the paths where gh prints a token to stderr and the
// API echoes the Authorization header in an error body.
func TestDiscoverNeverLeaksToken(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	p, api, toks := discoverWorld(t)
	f, err := discoverFacts(context.Background(), p, api, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	js, jerr := json.Marshal(f)
	if jerr != nil {
		t.Fatal(jerr)
	}
	out := opssetup.MergeDiscovery(f, opssetup.AliasesOut{})
	outJS, _ := json.Marshal(out)
	var errText strings.Builder
	for u, tok := range toks {
		tk, o := p.GHToken(context.Background(), u)
		errText.WriteString(o.Detail)
		_, uerr := api.User(context.Background(), tk)
		if uerr != nil {
			errText.WriteString(uerr.Error())
		}
		_, eerr := api.Emails(context.Background(), tk)
		if eerr != nil {
			errText.WriteString(eerr.Error())
		}
		slog.Info("token", "tok", tk)
		errText.WriteString(strings.Join([]string{tk.String(), tk.GoString()}, " "))
		_ = tok
	}
	_, o := p.GHToken(context.Background(), "nogh")
	errText.WriteString(o.Detail)
	for u, tok := range toks {
		for what, s := range map[string]string{"facts json": string(js), "op output": string(outJS), "errors": errText.String(), "logs": logs.String()} {
			if strings.Contains(s, tok) || strings.Contains(s, strings.TrimPrefix(tok, "gho_")) {
				t.Errorf("%s token appears in %s", u, what)
			}
		}
	}
	if !strings.Contains(logs.String(), "[redacted]") {
		t.Errorf("slog of a Token = %q, want [redacted]", logs.String())
	}
}

// With a trace in ctx (the setup page's Details and debug log) every gh
// and API call is recorded, and no recorded text holds a token: gh auth
// token's stdout is withheld, and the tokens are registered as secrets
// (a token without a known prefix is masked too, TrimPrefix below).
func TestDiscoverTraceNeverLeaksToken(t *testing.T) {
	t.Parallel()
	p, api, toks := discoverWorld(t)
	api.Observe = recordAPICall
	ctx, tr := probe.WithTrace(context.Background())
	if _, err := discoverFacts(ctx, p, api, nil, nil); err != nil {
		t.Fatal(err)
	}
	calls := tr.Calls()
	js, err := json.Marshal(calls)
	if err != nil {
		t.Fatal(err)
	}
	var sawToken, sawAPI bool
	for _, c := range calls {
		sawToken = sawToken || strings.Contains(c.Command, "auth token")
		sawAPI = sawAPI || (c.Kind == probe.CallHTTP && strings.Contains(c.Command, "/user"))
	}
	if !sawToken || !sawAPI {
		t.Errorf("trace lacks the gh or API calls: %s", js)
	}
	for u, tok := range toks {
		if strings.Contains(string(js), tok) || strings.Contains(string(js), strings.TrimPrefix(tok, "gho_")) {
			t.Errorf("%s token appears in the trace", u)
		}
	}
}

// storeRepos keeps only store-like names, store-word names first, in
// GitHub's order within a rank, at most maxStoreRepos.
func TestStoreRepos(t *testing.T) {
	t.Parallel()
	mk := func(names ...string) []ghapi.Repo {
		var out []ghapi.Repo
		for _, n := range names {
			out = append(out, ghapi.Repo{FullName: "octo-personal/" + n, Name: n, SSHURL: "git@github.com:octo-personal/" + n + ".git"})
		}
		return out
	}
	got := storeRepos(mk("site", "team-memory", "notes", "lorekit", "my-lore", "app", "store"))
	var names []string
	for _, r := range got {
		names = append(names, strings.TrimPrefix(r.FullName, "octo-personal/"))
	}
	if want := "notes my-lore store team-memory lorekit"; strings.Join(names, " ") != want {
		t.Errorf("order = %v, want %s", names, want)
	}
	many := make([]string, 0, maxStoreRepos+5)
	for i := range maxStoreRepos + 5 {
		many = append(many, "notes-"+strconv.Itoa(i))
	}
	if n := len(storeRepos(mk(many...))); n != maxStoreRepos {
		t.Errorf("cap: %d repos", n)
	}
	if n := len(storeRepos(nil)); n != 0 {
		t.Errorf("empty: %d", n)
	}
}
