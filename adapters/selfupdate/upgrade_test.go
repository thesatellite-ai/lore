package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestUpgradeDetectInstallMethod(t *testing.T) {
	cases := []struct {
		exe, ver string
		want     installMethod
	}{
		{"/opt/homebrew/Cellar/lore/0.1.19/bin/lore", "0.1.19", installBrew},
		{"/home/linuxbrew/.linuxbrew/Cellar/lore/0.1.19/bin/lore", "0.1.19", installBrew},
		{"/usr/local/bin/lore", "0.1.19", installScript},
		{`C:\Users\mira\AppData\Local\lore\lore.exe`, "0.1.19", installScript},
		{"/usr/local/bin/lore", "0.1.0-dev", installSource},
		{"/usr/local/bin/lore", "0.0.0-snapshot-abc", installSource},
		{"/opt/homebrew/Cellar/lore/0.1.19/bin/lore", "0.1.0-dev", installBrew},
		{"/home/u/.asdf/installs/lore/0.1.19/bin/lore", "0.1.19", installOther},
		{"/nix/store/abc-lore/bin/lore", "0.1.19", installOther},
	}
	for _, c := range cases {
		if got := detectInstall(c.exe, c.ver); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.exe, c.ver, got, c.want)
		}
	}
}

func TestUpgradeRespectsPins(t *testing.T) {
	newest := func() (string, error) { return "v0.1.20", nil }
	team := pinChoice{pin: "0.1.19", source: "team"}
	cases := []struct {
		name      string
		want      string
		pin       pinChoice
		ignore    bool
		ci        bool
		tag, code string
	}{
		{"newest", "", pinChoice{}, false, false, "v0.1.20", ""},
		{"team pin", "", team, false, false, "v0.1.19", ""},
		{"ignore pin", "", team, true, false, "v0.1.20", ""},
		{"version vs pin", "v0.1.18", team, false, false, "", string(errs.UpgradePinned)},
		{"version same as pin", "v0.1.19", team, false, false, "v0.1.19", ""},
		{"version ignore pin", "0.1.18", team, true, false, "v0.1.18", ""},
		{"ci no pin", "", pinChoice{}, false, true, "", string(errs.UpgradePinned)},
		{"ci pin", "", team, false, true, "v0.1.19", ""},
		{"ci version", "v0.1.20", pinChoice{}, false, true, "v0.1.20", ""},
	}
	for _, c := range cases {
		tag, err := chooseTarget(c.want, c.pin, c.ignore, c.ci, newest)
		if c.code != "" {
			if !errs.Has(err, errs.Code(c.code)) {
				t.Errorf("%s: err %v, want %s", c.name, err, c.code)
			}
			continue
		}
		if err != nil || tag != c.tag {
			t.Errorf("%s: %q %v, want %q", c.name, tag, err, c.tag)
		}
	}
	t.Setenv(envVersionPin, "0.1.17")
	if p := effectivePin(projectMeta{Pin: "0.1.19"}); p.pin != "0.1.17" || p.source != envVersionPin {
		t.Errorf("env pin does not override team pin: %+v", p)
	}
	// A target below the floor or a feature's Since cannot write (8.9).
	if why := lockedOut("0.1.17", projectMeta{MinLoreVersion: "0.1.18"}); len(why) == 0 {
		t.Error("pin below the floor not reported")
	}
	if why := lockedOut("0.1.18", projectMeta{MinLoreVersion: "0.1.18"}); len(why) != 0 {
		t.Errorf("at the floor reported: %v", why)
	}
}

func TestUpgradeSwapRollsBackOnWindows(t *testing.T) {
	var calls []string
	failSecond := swapOps{
		remove: func(string) error { return nil },
		rename: func(a, b string) error {
			calls = append(calls, a+">"+b)
			if a == "new.exe" {
				return errors.New("in use")
			}
			return nil
		},
	}
	if err := swapBinary(goosWindows, "new.exe", "lore.exe", failSecond); err == nil {
		t.Fatal("swap succeeded")
	}
	want := []string{"lore.exe>lore.exe.old", "new.exe>lore.exe", "lore.exe.old>lore.exe"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v", calls, want)
	}
	calls = nil
	if err := swapBinary("linux", "new", "lore", failSecond); err != nil || len(calls) != 1 {
		t.Fatalf("unix swap: %v %v", calls, err)
	}
}

// fakeRelease serves the redirect, archives and checksums.txt of one tag.
type fakeRelease struct {
	asset    string // archive file name; "" = this host's
	tag      string
	archive  []byte
	sums     string
	truncate bool
}

func (f *fakeRelease) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+f.tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/releases/download/"), "/")
		if len(parts) != 2 || parts[0] != f.tag {
			http.NotFound(w, r)
			return
		}
		switch parts[1] {
		case checksumsFile:
			_, _ = io.WriteString(w, f.sums)
		case f.assetName():
			if f.truncate {
				w.Header().Set("Content-Length", fmt.Sprint(len(f.archive)))
				_, _ = w.Write(f.archive[:len(f.archive)/2])
				return
			}
			_, _ = w.Write(f.archive)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func (f *fakeRelease) assetName() string {
	if f.asset != "" {
		return f.asset
	}
	return archiveName(runtime.GOOS, runtime.GOARCH)
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUpgradeAgainstLocalReleaseServer(t *testing.T) {
	// A linux archive on every host: the server and extract take the
	// platform as data, so this runs the same everywhere.
	const goos, goarch = "linux", "amd64"
	// Windows releases ship as .zip (goreleaser's format override).
	if got := archiveName(goosWindows, "arm64"); got != BinaryName+"_windows_arm64.zip" {
		t.Fatalf("windows archive name = %q", got)
	}
	archive := tarGz(t, map[string]string{"lore": "new-binary", "skills/SKILL.md": "skill", "README.md": "x"})
	sum := sha256.Sum256(archive)
	name := archiveName(goos, goarch)
	good := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	ctx := context.Background()

	cases := []struct {
		name  string
		rel   fakeRelease
		tag   string
		code  errs.Code
		check func(t *testing.T, files releaseFiles)
	}{
		{name: "happy", rel: fakeRelease{asset: name, tag: "v0.1.20", archive: archive, sums: good}, tag: "v0.1.20",
			check: func(t *testing.T, files releaseFiles) {
				if string(files.binary) != "new-binary" || string(files.skills["SKILL.md"]) != "skill" || len(files.skills) != 1 {
					t.Fatalf("extract: %q %v", files.binary, files.skills)
				}
			}},
		{name: "wrong hash", rel: fakeRelease{asset: name, tag: "v0.1.20", archive: archive, sums: strings.Repeat("0", 64) + "  " + name + "\n"}, tag: "v0.1.20", code: errs.UpgradeChecksum},
		{name: "missing line", rel: fakeRelease{asset: name, tag: "v0.1.20", archive: archive, sums: "abc  other.tar.gz\n"}, tag: "v0.1.20", code: errs.UpgradeChecksum},
		{name: "truncated", rel: fakeRelease{asset: name, tag: "v0.1.20", archive: archive, sums: good, truncate: true}, tag: "v0.1.20", code: errs.UpgradeChecksum},
		{name: "404 tag", rel: fakeRelease{asset: name, tag: "v0.1.20", archive: archive, sums: good}, tag: "v0.1.99", code: errs.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.rel.handler())
			defer srv.Close()
			old := releaseBaseURL
			releaseBaseURL = srv.URL
			defer func() { releaseBaseURL = old }()
			rc := newReleaseClient(5*time.Second, "0.1.19")
			latest, err := rc.latestTag(ctx)
			if err != nil || latest != c.rel.tag {
				t.Fatalf("latest %q %v", latest, err)
			}
			files, err := func() (releaseFiles, error) {
				a, ferr := rc.fetch(ctx, c.tag, name)
				if ferr != nil {
					return releaseFiles{}, ferr
				}
				s, ferr := rc.fetch(ctx, c.tag, checksumsFile)
				if ferr != nil {
					return releaseFiles{}, ferr
				}
				if ferr = verifySHA(a, s, name); ferr != nil {
					return releaseFiles{}, ferr
				}
				return extract(a, goos)
			}()
			if c.code != "" {
				if !errs.Has(err, errs.Code(c.code)) {
					t.Fatalf("err %v, want %s", err, c.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, files)
		})
	}

	// Slow server past the timeout and no server at all: E_OFFLINE.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer slow.Close()
	old := releaseBaseURL
	releaseBaseURL = slow.URL
	defer func() { releaseBaseURL = old }()
	if _, err := newReleaseClient(50*time.Millisecond, "0.1.19").latestTag(ctx); !errs.Has(err, errs.Offline) {
		t.Fatalf("slow server: %v", err)
	}
}

func TestUpgradeScriptSwapEndToEnd(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("unix swap path")
	}
	archive := tarGz(t, map[string]string{"lore": "#!/bin/sh\necho new\n"})
	sum := sha256.Sum256(archive)
	name := archiveName(runtime.GOOS, runtime.GOARCH)
	rel := fakeRelease{tag: "v0.1.20", archive: archive, sums: hex.EncodeToString(sum[:]) + "  " + name + "\n"}
	srv := httptest.NewServer(rel.handler())
	defer srv.Close()
	old := releaseBaseURL
	releaseBaseURL = srv.URL
	defer func() { releaseBaseURL = old }()
	oldRun := runVersion
	runVersion = func(_ context.Context, bin string) (string, error) {
		b, err := os.ReadFile(filepath.Clean(bin))
		if err != nil {
			return "", err
		}
		if strings.Contains(string(b), "echo new") {
			return "0.1.20", nil
		}
		return "0.1.19", nil
	}
	defer func() { runVersion = oldRun }()
	u := Updater{Version: "0.1.19", Home: t.TempDir()}
	dir := t.TempDir()
	exe := filepath.Join(dir, "lore")
	writeExecutable(t, exe, "old")
	var out bytes.Buffer
	if err := u.upgradeByScript(context.Background(), &out, newReleaseClient(5*time.Second, "0.1.19"), exe, "0.1.19", "v0.1.20"); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(filepath.Clean(exe)); !strings.Contains(string(b), "echo new") {
		t.Fatalf("binary not swapped: %q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp files left: %v", ents)
	}
	// Second run finds the target installed (the race case of 8.9).
	out.Reset()
	if err := u.upgradeByScript(context.Background(), &out, newReleaseClient(5*time.Second, "0.1.19"), exe, "0.1.19", "v0.1.20"); err != nil || !strings.Contains(out.String(), "already 0.1.20") {
		t.Fatalf("%v %q", err, out.String())
	}
}

// writeExecutable writes an executable file for the swap test.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), cliFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, defaultBinaryMode); err != nil {
		t.Fatal(err)
	}
}

// TestUpgradeDowngradeAsks pins 8.9: a downgrade into a project the target
// could not write is E_NEEDS_CONFIRM naming --yes, unless confirmed; a
// downgrade the target can still write, and any upgrade, never asks.
func TestUpgradeDowngradeAsks(t *testing.T) {
	locked := projectMeta{MinLoreVersion: "0.1.19"}
	cases := []struct {
		name           string
		have, want     string
		meta           projectMeta
		inProject, yes bool
		ask            bool
	}{
		{"upgrade", "0.1.18", "0.1.19", locked, true, false, false},
		{"downgrade locked out", "0.1.19", "0.1.18", locked, true, false, true},
		{"downgrade locked out, confirmed", "0.1.19", "0.1.18", locked, true, true, false},
		{"downgrade still writes", "0.1.19", "0.1.18", projectMeta{}, true, false, false},
		{"downgrade outside a project", "0.1.19", "0.1.18", locked, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			err := Updater{Confirmed: c.yes}.confirmDowngrade(&out, c.have, c.want, "acme", c.meta, c.inProject)
			if !c.ask {
				if err != nil {
					t.Fatalf("asked: %v", err)
				}
				return
			}
			if !errs.Has(err, errs.NeedsConfirm) {
				t.Fatalf("err %v, want E_NEEDS_CONFIRM", err)
			}
			env := errs.EnvelopeOf(err).Error
			if !strings.Contains(env.Message, "acme would become read-only") || !strings.Contains(fmt.Sprint(env.Next), "--yes") {
				t.Fatalf("envelope %+v", env)
			}
		})
	}
}
