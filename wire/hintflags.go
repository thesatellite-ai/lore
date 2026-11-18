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

// notRoutedByStore is how the home hint of a command with no store flag
// names --store (devco D-622): literally, so the reader learns the flag
// that would not reach the row, but never as "does not apply" (D-586: a
// reader could not tell that from "a store would not work") nor as if the
// person had passed it (D-589).
const notRoutedByStore = "not routed by --store <org>/<folder>"

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
			// the command takes no store flag: its id names the row
			// (never "--store does not apply", devco D-586: a reader
			// cannot tell that from "a store would not work"). The flag is
			// named literally (devco D-622): "takes no store flag" left a
			// reader, and the where-you-stand matrix, without --store.
			ways = append(ways, "add --setup <setup> so the row's id finds its store (the id names the row; the command is "+notRoutedByStore+")")
		} else {
			ways = append(ways, "or a setup with --setup <setup>")
		}
	}
	if len(ways) == 0 {
		// context, and a command given --setup that still has no store here:
		// it reads or writes the repo it runs in
		// (devco D-622: name --store literally, as D-586's wording did not)
		return "cd into a git checkout (or the store's clone): this command works on the folder it runs in and is " + notRoutedByStore + "; lore setup list shows each setup's folders and lore store list --setup <setup> its clones"
	}
	return "cd into a git checkout, or " + strings.Join(ways, " ") + "; lore store list and lore setup list show them"
}
