package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestPrintJSONError (OQ5, CLI_REFERENCE 1.4): an old command's error
// under --json is the core's error object, with class, exit and the
// retry/person defaults from the one registry.
func TestPrintJSONError(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		code  errs.Code
		exit  int
		class errs.Class
	}{
		{"v0.1.17 not found", errcodes.New(errcodes.NotFound, "no x").WithHint("lore x list"), errs.NotFound, 27, errs.ClassNotFound},
		{"core confirm", errs.New(errs.NeedsConfirm, "sure?").WithNext("lore", "x", "--yes"), errs.NeedsConfirm, 26, errs.ClassNeedsConfirmation},
		{"plain error", bytes.ErrTooLarge, errs.Internal, 1, errs.ClassGeneric},
	}
	for _, c := range cases {
		var b bytes.Buffer
		printJSONError(&b, c.err)
		var env errs.Envelope
		if err := json.Unmarshal(b.Bytes(), &env); err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, b.String())
		}
		if env.Error.Code != c.code || env.Error.Exit != c.exit || env.Error.Class != c.class {
			t.Fatalf("%s: got %+v", c.name, env.Error)
		}
	}
}
