package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateURLCredentials pins step url_credentials (14.5): a project
// row whose origin_url holds a token is rewritten clean through the
// engine, the step line names the field and says to rotate, no output or
// file keeps the token, and a second run changes nothing.
func TestMigrateURLCredentials(t *testing.T) {
	ctx := background()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	pid := projectID(t, a)
	tok := "ghp_" + strings.Repeat("Q", 36)
	root, err := os.OpenRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	rel := filepath.Join(".lore", "data", "projects", pid+".json")
	doc, err := root.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	dirty := strings.Replace(string(doc), `"name":`, `"origin_url":"https://`+tok+`@github.com/acme/hub.git","name":`, 1)
	if dirty == string(doc) {
		t.Fatalf("fixture: no name field in %s", doc)
	}
	if err = root.WriteFile(rel, []byte(dirty), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newTestApp(t, repo)
	dry, err := b.MigrateSettings(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.MigrateSettings(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(append(dry.Steps, got.Steps...), "\n")
	if !strings.Contains(all, StepURLCredentials) || !strings.Contains(all, "rotate") || strings.Contains(all, tok) {
		t.Fatalf("steps = %s", strings.ReplaceAll(all, tok, "<tok>"))
	}
	after, err := root.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), tok) || !strings.Contains(string(after), "https://github.com/acme/hub.git") {
		t.Fatalf("row file not cleaned")
	}
	again, err := b.MigrateSettings(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(again.Steps, "\n"), StepURLCredentials) {
		t.Fatalf("second run must change nothing: %v", again.Steps)
	}
}
