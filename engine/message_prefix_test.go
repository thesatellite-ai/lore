package engine

// Guard: no engine error message starts with the package name. The
// surfaces print an error's Message as is, so a "engine: " prefix reached
// every person and every --json body (campaign knowledge 2026-11-08); a
// message names the row, the store or the field, never the Go package.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoEnginePrefixInMessages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, `"engine: `) {
				t.Errorf("%s:%d: a message starts with the package name: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no engine source file was read: the guard checks nothing")
	}
}
