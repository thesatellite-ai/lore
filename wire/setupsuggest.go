package wire

// setupsuggest.go — App's opssetup.AccountCoverer (issue 6): which setup
// covers a GitHub account Home would suggest a setup for, read from
// config.yaml by importscan.ReadAccountCovers, and the person's "Suggest
// again" choices in a small machine-state file beside the import banner's.
//
//	setup.discover      ─▶ AccountCover       ─▶ config.yaml (setups, trash, trash.purged)
//	                                           + ~/.lore/state/suggest-again.json
//	setup.suggest_again ─▶ RecordSuggestAgain ─▶ ~/.lore/state/suggest-again.json (login|purged_at)

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/importscan"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.AccountCoverer = (*App)(nil)

// suggestAgainFile holds the "Suggest again" choices under the machine
// state folder (never synced): one key per purge, so a newer purge for the
// same account hides its suggestion again.
const suggestAgainFile = "suggest-again.json"

// suggestAgainMax bounds the keys kept (oldest dropped first).
const suggestAgainMax = 50

// suggestAgain is the file's shape.
type suggestAgain struct {
	Keys []string `json:"keys"`
}

// suggestAgainKey is one choice's key: the login in lower case and the
// purge time ("alice|2026-11-08T01:02:03Z").
func suggestAgainKey(login, purgedAt string) string {
	return strings.ToLower(strings.TrimSpace(login)) + "|" + purgedAt
}

func (a *App) suggestAgainPath() string {
	return filepath.Join(a.opts.Home, localStateDir, suggestAgainFile)
}

// readSuggestAgain reads the file; missing or broken is empty (an empty
// file only hides the suggestion again, which Suggest again undoes).
func (a *App) readSuggestAgain() suggestAgain {
	var d suggestAgain
	b, err := os.ReadFile(a.suggestAgainPath())
	if err != nil {
		return d
	}
	_ = json.Unmarshal(b, &d) // a broken file is rewritten on the next choice
	return d
}

// AccountCover implements opssetup.AccountCoverer.
func (a *App) AccountCover(_ context.Context, login, setupName string) (opssetup.AccountCoverFact, bool) {
	c, ok := importscan.ReadAccountCovers(a.opts.Home).For(login, setupName)
	if !ok {
		return opssetup.AccountCoverFact{}, false
	}
	f := opssetup.AccountCoverFact{State: string(c.State), Setup: c.Setup, PurgedAt: c.PurgedAt, Line: c.Line}
	if c.State == importscan.AccountPurged {
		f.Again = slices.Contains(a.readSuggestAgain().Keys, suggestAgainKey(login, c.PurgedAt))
	}
	return f, true
}

// RecordSuggestAgain implements opssetup.AccountCoverer.
func (a *App) RecordSuggestAgain(_ context.Context, login, purgedAt string) error {
	d := a.readSuggestAgain()
	key := suggestAgainKey(login, purgedAt)
	if slices.Contains(d.Keys, key) {
		return nil
	}
	d.Keys = append(d.Keys, key)
	if len(d.Keys) > suggestAgainMax {
		d.Keys = d.Keys[len(d.Keys)-suggestAgainMax:]
	}
	b, err := json.Marshal(d)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+suggestAgainFile, err)
	}
	return writeFileAtomic(a.suggestAgainPath(), b, importDismissMode)
}
