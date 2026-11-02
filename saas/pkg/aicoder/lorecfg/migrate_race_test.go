package lorecfg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// raceIdentityTOML is a legacy identity file; its key must survive any
// interleaving of two migrations.
const raceIdentityTOML = "display_name = \"Alice\"\nstable_key = \"human:alice@legacy.example\"\n"

const raceKey = "human:alice@legacy.example"

func writeLegacy(t *testing.T, dir string) (tomlPath, yamlPath string) {
	t.Helper()
	tomlPath = filepath.Join(dir, "identity"+ExtTOML)
	yamlPath = filepath.Join(dir, "identity"+ExtYAML)
	if err := os.WriteFile(tomlPath, []byte(raceIdentityTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	return tomlPath, yamlPath
}

func assertYAMLHasKey(t *testing.T, yamlPath string) {
	t.Helper()
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("migrated YAML lost: %v", err)
	}
	if !strings.Contains(string(b), raceKey) {
		t.Fatalf("YAML lacks %s:\n%s", raceKey, b)
	}
}

// BUG-R1: process B converts the TOML, process A then runs a whole migration
// (YAML written, TOML renamed), then B commits. B must keep A's YAML.
func TestMigrateLoserKeepsWinnersYAML(t *testing.T) {
	dir := t.TempDir()
	tomlPath, yamlPath := writeLegacy(t, dir)

	out, perm, err := convertLegacy(tomlPath) // B reads first
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(context.Background(), tomlPath, yamlPath); err != nil { // A wins
		t.Fatalf("winner: %v", err)
	}
	_, err = commitMigration(tomlPath, yamlPath, out, perm) // B commits late
	if !errors.Is(err, errMigratedElsewhere) {
		t.Fatalf("loser: got %v, want errMigratedElsewhere", err)
	}
	assertYAMLHasKey(t, yamlPath)
	if _, err := os.Stat(tomlPath + MigratedSuffix); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	// A later Locate reads the YAML quietly.
	var notes bytes.Buffer
	loc, err := Locate(context.Background(), dir, "identity", Options{Notices: &notes})
	if err != nil || loc.Format != FormatYAML || !loc.Exists {
		t.Fatalf("Locate after race: %+v %v", loc, err)
	}
	if notes.Len() != 0 {
		t.Fatalf("unexpected notices: %s", notes.String())
	}
}

// The TOML vanishes between Locate's check and migrate's stat: the other
// process finished, so migrate reports it and Locate returns the YAML.
func TestMigrateTOMLGoneBeforeStat(t *testing.T) {
	dir := t.TempDir()
	tomlPath, yamlPath := writeLegacy(t, dir)
	if _, err := migrate(context.Background(), tomlPath, yamlPath); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(context.Background(), tomlPath, yamlPath); !errors.Is(err, errMigratedElsewhere) {
		t.Fatalf("got %v, want errMigratedElsewhere", err)
	}
	assertYAMLHasKey(t, yamlPath)
}

// With neither file present a vanished TOML is a real error, not "elsewhere".
func TestMigrateTOMLGoneWithoutYAMLIsAnError(t *testing.T) {
	dir := t.TempDir()
	_, err := migrate(context.Background(), filepath.Join(dir, "x"+ExtTOML), filepath.Join(dir, "x"+ExtYAML))
	if err == nil || errors.Is(err, errMigratedElsewhere) {
		t.Fatalf("got %v, want a stat error", err)
	}
}

// Many Locates at once on one legacy file: every caller ends on a YAML that
// holds the identity, and exactly one backup exists.
func TestLocateConcurrentMigration(t *testing.T) {
	const callers = 16
	for round := 0; round < 20; round++ {
		dir := t.TempDir()
		tomlPath, yamlPath := writeLegacy(t, dir)
		var wg sync.WaitGroup
		errs := make(chan error, callers)
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				loc, err := Locate(context.Background(), dir, "identity", Options{Notices: io.Discard})
				if err == nil && loc.Format != FormatYAML {
					err = errors.New("caller fell back to TOML: " + loc.Path)
				}
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		assertYAMLHasKey(t, yamlPath)
		if _, err := os.Stat(tomlPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("round %d: TOML still present: %v", round, err)
		}
		m, _ := filepath.Glob(tomlPath + MigratedSuffix + "*")
		if len(m) != 1 {
			t.Fatalf("round %d: backups %v, want exactly one", round, m)
		}
	}
}
