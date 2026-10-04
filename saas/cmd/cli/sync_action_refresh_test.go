package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installAt installs the workflow for the project at dir with v0.1.11 and
// returns the file path.
func installAt(t *testing.T, dir string, o syncActionOptions) string {
	t.Helper()
	if o.loreVersion == "" {
		o.loreVersion = "v0.1.11"
	}
	if len(o.branches) == 0 {
		o.branches = []string{"main"}
	}
	res, err := installSyncAction(context.Background(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	return res.Path
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func refresh(t *testing.T, dir, own string) syncActionRefresh {
	t.Helper()
	r, err := refreshSyncActionPin(context.Background(), dir, own)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The main path: an auto-pinned file follows a newer lore release, keeps
// every other setting, and carries a valid checksum afterwards.
func TestRefreshUpgradesAutoPin(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	file := installAt(t, dir, syncActionOptions{branches: []string{"main", "staging"}})
	r := refresh(t, dir, "0.1.12")
	if r.From != "v0.1.11" || r.To != "v0.1.12" || r.Path != file || r.Note != "" {
		t.Fatalf("refresh = %+v", r)
	}
	s, err := parseSyncAction(readFile(t, file))
	if err != nil {
		t.Fatal(err)
	}
	if s.version != "v0.1.12" || s.pin != syncActionPinAuto || strings.Join(s.branches, ",") != "main,staging" || s.manual {
		t.Fatalf("settings after refresh: %+v", s)
	}
	if syncActionChecksum(readFile(t, file)) != s.sum {
		t.Fatal("refreshed file carries a stale checksum")
	}
	if again := refresh(t, dir, "0.1.12"); again != (syncActionRefresh{}) {
		t.Fatalf("second run must be a no-op: %+v", again)
	}
}

// Cases where the file must stay exactly as it is.
func TestRefreshLeavesAlone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		o    syncActionOptions
		own  string
	}{
		{"older lore never downgrades", syncActionOptions{}, "0.1.10"},
		{"same version", syncActionOptions{}, "v0.1.11"},
		{"development build", syncActionOptions{}, "0.1.0-dev"},
		{"git-describe build", syncActionOptions{}, "v0.1.12-3-gabc1234-dirty"},
		{"fixed pin", syncActionOptions{pin: syncActionPinFixed}, "0.1.12"},
		{"fixed latest", syncActionOptions{loreVersion: loreVersionLatest, pin: syncActionPinFixed}, "0.1.12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := gitInitRepo(t)
			file := installAt(t, dir, tc.o)
			before := readFile(t, file)
			if r := refresh(t, dir, tc.own); r != (syncActionRefresh{}) {
				t.Fatalf("refresh = %+v", r)
			}
			if readFile(t, file) != before {
				t.Fatal("file changed")
			}
		})
	}
}

// An auto pin that was installed as "latest" (from a development build)
// moves to the first release a developer runs.
func TestRefreshAutoLatestPinsToRelease(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	installAt(t, dir, syncActionOptions{loreVersion: loreVersionLatest})
	if r := refresh(t, dir, "0.1.12"); r.From != loreVersionLatest || r.To != "v0.1.12" {
		t.Fatalf("refresh = %+v", r)
	}
}

func TestRefreshSkipsHandEditedFile(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	file := installAt(t, dir, syncActionOptions{})
	edited := strings.Replace(readFile(t, file), "timeout-minutes: 10", "timeout-minutes: 20", 1)
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	r := refresh(t, dir, "0.1.12")
	if r.Note == "" || r.To != "" || !strings.Contains(r.Note, "edited by hand") {
		t.Fatalf("refresh = %+v", r)
	}
	if readFile(t, file) != edited {
		t.Fatal("hand edit overwritten")
	}
}

// A Windows checkout converts the file to CRLF: not a hand edit.
func TestRefreshToleratesCRLF(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	file := installAt(t, dir, syncActionOptions{})
	crlf := strings.ReplaceAll(readFile(t, file), "\n", "\r\n")
	if err := os.WriteFile(file, []byte(crlf), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := refresh(t, dir, "0.1.12"); r.To != "v0.1.12" {
		t.Fatalf("CRLF file must still refresh: %+v", r)
	}
}

func TestRefreshNeverCreatesOrTouchesForeignFiles(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	file := filepath.Join(dir, filepath.FromSlash(syncActionFileRel("")))
	if r := refresh(t, dir, "0.1.12"); r != (syncActionRefresh{}) {
		t.Fatalf("not installed: %+v", r)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("refresh created the workflow")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("name: theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := refresh(t, dir, "0.1.12"); r != (syncActionRefresh{}) || readFile(t, file) != "name: theirs\n" {
		t.Fatalf("foreign file touched: %+v", r)
	}
}

func TestRefreshKeepsManualWorkflowManual(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	file := installAt(t, dir, syncActionOptions{manual: true, branches: []string{"develop"}})
	if r := refresh(t, dir, "0.1.12"); r.To != "v0.1.12" {
		t.Fatalf("refresh = %+v", r)
	}
	s, err := parseSyncAction(readFile(t, file))
	if err != nil || !s.manual || strings.Join(s.branches, ",") != "develop" {
		t.Fatalf("manual settings lost: %+v %v", s, err)
	}
}

// Two lore projects in one repository each get their own workflow; refreshing
// one never touches the other.
func TestSyncActionPerProjectFiles(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	api, web := filepath.Join(dir, "services", "api"), filepath.Join(dir, "web")
	for _, d := range []string{api, web} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rootFile := installAt(t, dir, syncActionOptions{})
	apiFile := installAt(t, api, syncActionOptions{})
	webFile := installAt(t, web, syncActionOptions{pin: syncActionPinFixed})
	if filepath.Base(apiFile) != "lore-sync-merge-services-api.yml" || filepath.Base(webFile) != "lore-sync-merge-web.yml" {
		t.Fatalf("names: %s %s", apiFile, webFile)
	}
	for _, f := range []string{apiFile, webFile} {
		if !strings.Contains(readFile(t, f), "group: lore-sync-merge-") {
			t.Fatal("missing concurrency group")
		}
	}
	if !strings.Contains(readFile(t, apiFile), "group: lore-sync-merge-services-api-") {
		t.Fatal("concurrency group must be per project")
	}
	webBefore := readFile(t, webFile)
	if r := refresh(t, api, "0.1.12"); r.Path != apiFile || r.To != "v0.1.12" {
		t.Fatalf("api refresh = %+v", r)
	}
	if readFile(t, webFile) != webBefore {
		t.Fatal("refreshing one project touched another")
	}
	if res, err := uninstallSyncAction(context.Background(), web, false); err != nil || res.Path != webFile {
		t.Fatalf("uninstall web: %+v %v", res, err)
	}
	for _, f := range []string{apiFile, rootFile} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("uninstalling one project removed %s", f)
		}
	}
}

// legacyFile turns a freshly rendered file into what lore v0.1.11 wrote: no
// pin line and no checksum line.
func legacyFile(content string) string {
	var kept []string
	for _, l := range strings.Split(content, "\n") {
		if !strings.HasPrefix(l, syncActionPinPrefix) && !strings.HasPrefix(l, syncActionSumPrefix) {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

func TestRefreshLegacyFile(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	file := installAt(t, dir, syncActionOptions{})
	if err := os.WriteFile(file, []byte(legacyFile(readFile(t, file))), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := refresh(t, dir, "0.1.12"); r.To != "v0.1.12" {
		t.Fatalf("legacy file must refresh: %+v", r)
	}
	s, err := parseSyncAction(readFile(t, file))
	if err != nil || s.sum == "" || s.pin != syncActionPinAuto {
		t.Fatalf("refreshed legacy file lacks the new header: %+v %v", s, err)
	}

	edited := gitInitRepo(t)
	f2 := installAt(t, edited, syncActionOptions{})
	legacy := strings.Replace(legacyFile(readFile(t, f2)), "timeout-minutes: 10", "timeout-minutes: 30", 1)
	if err := os.WriteFile(f2, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := refresh(t, edited, "0.1.12"); r.Note == "" || r.To != "" {
		t.Fatalf("hand-edited legacy file must be left alone: %+v", r)
	}
}

// lore v0.1.11 wrote every project's workflow to the root name; a subfolder
// project's file moves to its own name on refresh (and on install).
func TestRefreshMovesLegacySubfolderFile(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	api := filepath.Join(dir, "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	own := installAt(t, api, syncActionOptions{})
	root := filepath.Join(dir, filepath.FromSlash(syncActionFileRel("")))
	if err := os.Rename(own, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte(legacyFile(readFile(t, root))), 0o644); err != nil {
		t.Fatal(err)
	}
	r := refresh(t, api, "0.1.12")
	if r.Path != own || r.To != "v0.1.12" {
		t.Fatalf("refresh = %+v", r)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("legacy root-named file left behind")
	}
	// A root-named file of the ROOT project is never taken by a subfolder.
	other := gitInitRepo(t)
	rootFile := installAt(t, other, syncActionOptions{})
	sub := filepath.Join(other, "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := refresh(t, sub, "0.1.12"); r != (syncActionRefresh{}) {
		t.Fatalf("subfolder took the root project's workflow: %+v", r)
	}
	if _, err := os.Stat(rootFile); err != nil {
		t.Fatal("root project's workflow removed")
	}
}

func TestReleaseLess(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"v0.1.9", "v0.1.10", true},
		{"v0.1.10", "v0.1.9", false},
		{"v0.1.11", "v0.1.11", false},
		{"v0.9.0", "v1.0.0", true},
		{"v1.2.3", "v1.10.0", true},
	} {
		if got := releaseLess(tc.a, tc.b); got != tc.want {
			t.Fatalf("%s < %s = %v", tc.a, tc.b, got)
		}
	}
}

func TestParseSyncActionRejects(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	good := readFile(t, installAt(t, dir, syncActionOptions{}))
	for name, bad := range map[string]string{
		"no marker":   "name: x\n",
		"bad pin":     strings.Replace(good, syncActionPinPrefix+"auto", syncActionPinPrefix+"sometimes", 1),
		"broken yaml": good + "\n\t: : :\n",
		"no ci-merge": strings.ReplaceAll(good, "lore sync ci-merge", "lore sync other"),
	} {
		if _, err := parseSyncAction(bad); err == nil {
			t.Fatalf("%s: must be rejected", name)
		}
	}
}

// An upgrade invalidates the cached wiring check, or the pin would only be
// refreshed when something else changed.
func TestWiringVersionIncludesLoreVersion(t *testing.T) {
	before := wiringVersion()
	old := version
	version = old + ".next"
	t.Cleanup(func() { version = old })
	if wiringVersion() == before {
		t.Fatal("wiring cache key ignores the lore version")
	}
}
