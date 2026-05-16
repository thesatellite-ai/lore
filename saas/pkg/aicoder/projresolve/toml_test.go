package projresolve

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupModeA(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, MarkerDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MarkerDir, ModeAFile), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setupModeB(t *testing.T, tomlContent string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, MarkerDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MarkerDir, ModeBFile), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetectMode_ModeA(t *testing.T) {
	dir := setupModeA(t)
	mode, err := DetectMode(dir)
	if err != nil {
		t.Fatalf("DetectMode: %v", err)
	}
	if mode != ModeA {
		t.Errorf("expected ModeA, got %v", mode)
	}
}

func TestDetectMode_ModeB(t *testing.T) {
	dir := setupModeB(t, `db_path = "/tmp/x.db"
project_id = "prj_018f3b2c9a8e7891b4f51234567890ab"`)
	mode, err := DetectMode(dir)
	if err != nil {
		t.Fatalf("DetectMode: %v", err)
	}
	if mode != ModeB {
		t.Errorf("expected ModeB, got %v", mode)
	}
}

func TestDetectMode_Ambiguous(t *testing.T) {
	dir := setupModeA(t)
	if err := os.WriteFile(filepath.Join(dir, MarkerDir, ModeBFile), []byte("db_path=\"x\"\nproject_id=\"y\""), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := DetectMode(dir)
	if !errors.Is(err, ErrAmbiguousProject) {
		t.Errorf("expected ErrAmbiguousProject, got %v", err)
	}
}

func TestDetectMode_NotProjectRoot(t *testing.T) {
	dir := t.TempDir() // no .lore/
	_, err := DetectMode(dir)
	if !errors.Is(err, ErrNotProjectRoot) {
		t.Errorf("expected ErrNotProjectRoot, got %v", err)
	}
}

func TestLoadTOML_Valid(t *testing.T) {
	dir := setupModeB(t, `db_path = "/tmp/test.db"
project_id = "prj_018f3b2c9a8e7891b4f51234567890ab"`)

	p, err := LoadTOML(dir)
	if err != nil {
		t.Fatalf("LoadTOML: %v", err)
	}
	if p.DBPath != "/tmp/test.db" {
		t.Errorf("DBPath = %q", p.DBPath)
	}
	if p.ProjectID != "prj_018f3b2c9a8e7891b4f51234567890ab" {
		t.Errorf("ProjectID = %q", p.ProjectID)
	}
}

func TestLoadTOML_MissingDBPath(t *testing.T) {
	dir := setupModeB(t, `project_id = "prj_xxx"`)
	_, err := LoadTOML(dir)
	if !errors.Is(err, ErrTomlMissingField) {
		t.Errorf("expected ErrTomlMissingField, got %v", err)
	}
}

func TestLoadTOML_MissingProjectID(t *testing.T) {
	dir := setupModeB(t, `db_path = "/tmp/x.db"`)
	_, err := LoadTOML(dir)
	if !errors.Is(err, ErrTomlMissingField) {
		t.Errorf("expected ErrTomlMissingField, got %v", err)
	}
}

func TestExpandPath_Home(t *testing.T) {
	t.Setenv("HOME", "/test/home")
	got, err := ExpandPath("${HOME}/db.sqlite", "/proj")
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	if got != "/test/home/db.sqlite" {
		t.Errorf("got %q", got)
	}
}

func TestExpandPath_AicoderHome(t *testing.T) {
	t.Setenv("LORE_HOME", "/custom/aicoder")
	got, err := ExpandPath("${LORE_HOME}/shared.db", "/proj")
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	if got != "/custom/aicoder/shared.db" {
		t.Errorf("got %q", got)
	}
}

func TestExpandPath_AicoderHomeFallback(t *testing.T) {
	t.Setenv("LORE_HOME", "")
	t.Setenv("HOME", "/h")
	got, err := ExpandPath("${LORE_HOME}/x", "/proj")
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	if got != "/h/.lore/x" {
		t.Errorf("got %q", got)
	}
}

func TestExpandPath_ProjectRoot(t *testing.T) {
	got, err := ExpandPath("${PROJECT_ROOT}/.lore/shared.db", "/work/chatbot")
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	if got != "/work/chatbot/.lore/shared.db" {
		t.Errorf("got %q", got)
	}
}

func TestExpandPath_RefusesTraversal(t *testing.T) {
	t.Setenv("LORE_ALLOW_DB_PATH_OUTSIDE", "")
	cases := []string{
		"../../etc/passwd",
		"../../../sensitive",
		"./normal/../escape/passwd",
	}
	for _, c := range cases {
		_, err := ExpandPath(c, "/proj")
		if !errors.Is(err, ErrBadPath) {
			t.Errorf("ExpandPath(%q) expected ErrBadPath, got %v", c, err)
		}
	}
}

func TestExpandPath_AllowsTraversalWhenEnvSet(t *testing.T) {
	t.Setenv("LORE_ALLOW_DB_PATH_OUTSIDE", "1")
	_, err := ExpandPath("../../some/path", "/proj")
	if err != nil {
		t.Errorf("expected env override allow, got %v", err)
	}
}

func TestResolve_ModeA(t *testing.T) {
	dir := setupModeA(t)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	c, err := Resolve(Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Mode != ModeA {
		t.Errorf("Mode=%v", c.Mode)
	}
	if c.SourceDB != "local" {
		t.Errorf("SourceDB=%q", c.SourceDB)
	}
	if !strings.HasSuffix(c.DBPath, ".lore/lore.db") {
		t.Errorf("DBPath=%q", c.DBPath)
	}
}

func TestResolve_FlagOverridesEverything(t *testing.T) {
	dir := setupModeA(t)
	_ = os.Chdir(dir)

	t.Setenv("LORE_DB", "/env/path.db")

	c, err := Resolve(Inputs{FlagDB: "/flag/path.db"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.SourceDB != "flag" {
		t.Errorf("SourceDB=%q", c.SourceDB)
	}
	if c.DBPath != "/flag/path.db" {
		t.Errorf("DBPath=%q", c.DBPath)
	}
}

func TestResolve_EnvOverridesToml(t *testing.T) {
	dir := setupModeB(t, `db_path = "/toml/x.db"
project_id = "prj_018f3b2c9a8e7891b4f51234567890ab"`)
	_ = os.Chdir(dir)

	t.Setenv("LORE_DB", "/env/y.db")

	c, err := Resolve(Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.SourceDB != "env" {
		t.Errorf("SourceDB=%q", c.SourceDB)
	}
	if c.DBPath != "/env/y.db" {
		t.Errorf("DBPath=%q", c.DBPath)
	}
	// ProjectID should still come from toml since no flag/env for project.
	if c.ProjectID != "prj_018f3b2c9a8e7891b4f51234567890ab" {
		t.Errorf("ProjectID=%q", c.ProjectID)
	}
}

func TestResolve_NoProjectRoot_Refuses(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chdir(dir)

	_, err := Resolve(Inputs{})
	if !errors.Is(err, ErrNotProjectRoot) {
		t.Errorf("expected ErrNotProjectRoot, got %v", err)
	}
}

func TestResolve_AmbiguousMarker_Refuses(t *testing.T) {
	dir := setupModeA(t)
	if err := os.WriteFile(filepath.Join(dir, MarkerDir, ModeBFile), []byte("db_path=\"x\"\nproject_id=\"y\""), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chdir(dir)

	_, err := Resolve(Inputs{})
	if !errors.Is(err, ErrAmbiguousProject) {
		t.Errorf("expected ErrAmbiguousProject, got %v", err)
	}
}
