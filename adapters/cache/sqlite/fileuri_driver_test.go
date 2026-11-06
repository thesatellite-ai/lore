package sqlite

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// uriAuthorityErr is SQLite's refusal of a file: URI with a host part.
const uriAuthorityErr = "invalid uri authority"

// TestDriverWindowsFileURI: the DSN lore builds for a Windows drive path
// reaches SQLite without a host part. The url.URL a path used to be put
// in printed `file://C:/...`, which SQLite refuses on every system with
// "invalid uri authority: C:" (so every cache open failed on Windows);
// fileuri's `file:///C:/...` is parsed as a path (on Linux it then fails
// only because the folder does not exist).
func TestDriverWindowsFileURI(t *testing.T) {
	t.Parallel()
	const winPath = "C:/Users/a b/.lore/cache/x.db"
	ping := func(dsn string) error {
		db, err := sql.Open(driverName, dsn)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		return db.Ping()
	}
	old := (&url.URL{Scheme: fileuri.Scheme, Path: winPath, RawQuery: "mode=ro"}).String()
	if err := ping(old); err == nil || !strings.Contains(err.Error(), uriAuthorityErr) {
		t.Fatalf("the old form %s: %v, want %q (the driver changed; this proof is stale)", old, err, uriAuthorityErr)
	}
	dsn := fileuri.URL(winPath, "mode=ro")
	if err := ping(dsn); err != nil && strings.Contains(err.Error(), uriAuthorityErr) {
		t.Fatalf("%s: %v", dsn, err)
	}
}
