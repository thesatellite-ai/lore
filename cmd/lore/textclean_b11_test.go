package main

// P3-TEXT-1 through the release binary's whole path: a memory body with a
// right-to-left override, a zero-width space and CRLF is stored cleaned in
// its row file; a body with a NUL byte is refused and nothing is written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryBodyTextCleaned(t *testing.T) {
	dir := envScopeRepo(t)
	if code, _, errOut := runCaptured(t, "memory", "add", "--body", "safe\u202eevil\u200b\r\nline"); code != 0 {
		t.Fatalf("memory add: exit %d %s", code, errOut)
	}
	files, err := filepath.Glob(filepath.Join(dir, ".lore", "data", "memories", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("memory row files = %v, %v", files, err)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"body": "safeevil\nline"`) {
		t.Fatalf("body not cleaned in the row file:\n%s", b)
	}
	if code, _, _ := runCaptured(t, "memory", "add", "--body", "bad\x00body"); code == 0 {
		t.Fatal("a NUL byte in a body was stored")
	}
	if again, _ := filepath.Glob(filepath.Join(dir, ".lore", "data", "memories", "*.json")); len(again) != 1 {
		t.Fatalf("a refused write left a row file: %v", again)
	}
}
