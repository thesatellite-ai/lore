package wire

// hintflags.go — the home-folder E_NOT_A_STORE hint names only the flags
// the refused command takes (devco:D-539). The git backend writes one hint
// for every command ("--to <org>/<folder> (or --store …) or --setup"), but
// context takes neither --to nor --store, memory edit takes no --to, and a
// command already given --setup was told to add it.

import (
	"context"
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// homeHintMarker is the part of the git backend's home-folder hint
// (adapters/backend/git notAWorkTreeHomeHint) that names the store flags.
const homeHintMarker = "name the store from here with --to <org>/<folder>"

// fitHomeHint returns err with its hint rewritten for e when it is the
// home-folder E_NOT_A_STORE refusal; any other error is returned as is.
func fitHomeHint(ctx context.Context, e op.Entry, err error) error {
	var le *errs.Error
	if err == nil || !errors.As(err, &le) || le.Code != errs.NotAStore || !strings.Contains(le.Hint, homeHintMarker) {
		return err
	}
	return le.WithHint(homeHint(e, strings.TrimSpace(op.SetupOf(ctx)) != ""))
}

// homeHint is the next step for e refused outside a repo: the store flags
// e takes, --setup unless it was given, else the repo-only way out (cd).
func homeHint(e op.Entry, setupGiven bool) string {
	var to, st, id bool
	for _, f := range e.InputFields() {
		switch f.Name {
		case engine.InputTarget:
			to = true
		case readStoreField:
			st = true
		case "id":
			id = true
		}
	}
	var ways []string
	switch {
	case to && st:
		ways = append(ways, "name the store with --to <org>/<folder> (or --store <org>[/<folder>]; --to me for your own)")
	case to:
		ways = append(ways, "name the store with --to <org>/<folder> (--to me for your own)")
	case st:
		ways = append(ways, "name the store with --store <org>[/<folder>]")
	}
	if !setupGiven && (to || st || id) {
		if id && !to && !st {
			ways = append(ways, "add --setup <setup> so the row's id finds its store (--store does not apply)")
		} else {
			ways = append(ways, "or a setup with --setup <setup>")
		}
	}
	if len(ways) == 0 {
		// context, and a command given --setup that still has no store here:
		// it reads or writes the repo it runs in
		return "cd into a git checkout (or the store's clone): this command works on the folder it runs in, so --store does not apply; lore setup list shows each setup's folders and lore store list --setup <setup> its clones"
	}
	return "cd into a git checkout, or " + strings.Join(ways, " ") + "; lore store list and lore setup list show them"
}
