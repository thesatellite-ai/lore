package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRunSchemaOnly pins 8.11 part 2 after a swap: inside a project that
// has a .lore/lore.db the NEW binary runs `setup --schema-only` in the
// project root; a project without one runs nothing; a new binary that
// fails is reported ("database not updated", the next command does it)
// and is not an error, since the swap already happened.
func TestRunSchemaOnly(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("shell-script stand-in binary")
	}
	cases := []struct {
		name     string
		withDB   bool
		exitCode string
		wantRan  bool
		wantOut  string
	}{
		{name: "project with lore.db", withDB: true, exitCode: "0", wantRan: true, wantOut: "Running: lore setup --schema-only (new binary)"},
		{name: "no lore.db", withDB: false, exitCode: "0", wantRan: false, wantOut: ""},
		{name: "new binary fails", withDB: true, exitCode: "3", wantRan: true, wantOut: "database not updated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".lore"), cliDirMode); err != nil {
				t.Fatal(err)
			}
			if tc.withDB {
				if err := os.WriteFile(filepath.Join(root, ".lore", "lore.db"), nil, cliFileMode); err != nil {
					t.Fatal(err)
				}
			}
			record := filepath.Join(t.TempDir(), "called")
			exe := filepath.Join(t.TempDir(), "lore")
			writeExecutable(t, exe, "#!/bin/sh\necho \"$PWD $*\" > '"+record+"'\nexit "+tc.exitCode+"\n")
			var out bytes.Buffer
			if err := runSchemaOnly(context.Background(), &out, exe, root); err != nil {
				t.Fatalf("runSchemaOnly: %v (a failed schema update must not fail the upgrade)", err)
			}
			got, rerr := os.ReadFile(filepath.Clean(record))
			ran := rerr == nil
			if ran != tc.wantRan {
				t.Fatalf("new binary ran = %v, want %v (out %q)", ran, tc.wantRan, out.String())
			}
			if ran && strings.TrimSpace(string(got)) != root+" setup --schema-only" {
				t.Fatalf("new binary called as %q, want %q", strings.TrimSpace(string(got)), root+" setup --schema-only")
			}
			if tc.wantOut == "" && out.Len() != 0 || !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("out = %q, want %q", out.String(), tc.wantOut)
			}
		})
	}
}

// TestUpgradeScriptDowngradeSwap: `lore upgrade --version` to an OLDER
// release from a local release server (no network) swaps the binary the
// same way an upgrade does (checksum, smoke, atomic swap, no temp file
// left); asking about the downgrade is confirmDowngrade's job, before this.
func TestUpgradeScriptDowngradeSwap(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("unix swap path")
	}
	archive := tarGz(t, map[string]string{"lore": "#!/bin/sh\necho old-release\n"})
	sum := sha256.Sum256(archive)
	rel := fakeRelease{tag: "v0.1.17", archive: archive, sums: hex.EncodeToString(sum[:]) + "  " + archiveName(runtime.GOOS, runtime.GOARCH) + "\n"}
	srv := httptest.NewServer(rel.handler())
	defer srv.Close()
	probe := func(_ context.Context, bin string) (string, error) {
		b, err := os.ReadFile(filepath.Clean(bin))
		if err != nil {
			return "", err
		}
		if strings.Contains(string(b), "old-release") {
			return "0.1.17", nil
		}
		return "0.1.18", nil
	}
	u := Updater{Version: "0.1.18", Home: t.TempDir(), ReleaseBase: srv.URL, VersionProbe: probe}
	dir := t.TempDir()
	exe := filepath.Join(dir, "lore")
	writeExecutable(t, exe, "current")
	var out bytes.Buffer
	if err := u.upgradeByScript(context.Background(), &out, newReleaseClient(srv.URL, 5*time.Second, "0.1.18"), exe, "0.1.18", "v0.1.17"); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(filepath.Clean(exe)); !strings.Contains(string(b), "old-release") {
		t.Fatalf("binary not swapped to the older release: %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp files left beside the binary: %v", ents)
	}
	// a wrong checksum refuses and leaves the running binary alone
	writeExecutable(t, exe, "current")
	bad := fakeRelease{tag: "v0.1.17", archive: archive, sums: strings.Repeat("0", 64) + "  " + archiveName(runtime.GOOS, runtime.GOARCH) + "\n"}
	srv2 := httptest.NewServer(bad.handler())
	defer srv2.Close()
	u.ReleaseBase = srv2.URL
	if err := u.upgradeByScript(context.Background(), &out, newReleaseClient(srv2.URL, 5*time.Second, "0.1.18"), exe, "0.1.18", "v0.1.17"); err == nil {
		t.Fatal("a checksum mismatch must refuse the swap")
	}
	if b, _ := os.ReadFile(filepath.Clean(exe)); string(b) != "current" {
		t.Fatalf("a refused swap changed the binary: %q", b)
	}
}
