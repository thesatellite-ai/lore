package settings

// secrets_lastcheck.go — the stored last-check state of `lore secrets`
// (SECRETS 2.6, 15; inventory A-OTHER-1).
//
//	secrets check / list --missing ─▶ verify ─▶ saveChecks (per entry key)
//	secrets list (no --missing)    ─▶ loadChecks ─▶ State + CheckedAt filled
//
// DECISION FA-AGENT-3 (refines SEC-IDX1 under FA-OLD-LOCAL): the result of
// the last check is MACHINE-LOCAL (whether a vault entry is reachable is a
// fact about this machine's vault logins), kept in store/localstate table
// secret_checks, key "<manager>://<locator>#<fragment>", never synced. A
// plain list still opens no vault: it shows the stored state with its date.

import (
	"context"
	"errors"
	"time"

	"github.com/thesatellite-ai/lore/store/localstate"
)

// LocalStateSource is an optional Env upgrade: this machine's local state
// (wire.App.LocalState). Without it nothing is stored and a plain list
// shows no state, as before.
type LocalStateSource interface {
	LocalState(ctx context.Context) (localstate.Store, error)
}

// secretCheckDoc is one stored check result.
type secretCheckDoc struct {
	State     string    `json:"state"`
	CheckedAt time.Time `json:"checked_at"`
}

// checkKey is the secret_checks key of a use (one per vault entry).
func checkKey(u SecretUse) string { return u.Manager + "://" + u.Locator + "#" + u.Fragment }

// saveChecks stores every checked state of us (uses without a manager are
// not entries and are skipped).
func (o ops) saveChecks(ctx context.Context, us []SecretUse, at time.Time) (err error) {
	src, ok := o.env.(LocalStateSource)
	if !ok {
		return nil
	}
	ls, err := src.LocalState(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, ls.Close()) }()
	for _, u := range us {
		if u.Manager == "" || u.State == "" {
			continue
		}
		if err = localstate.PutJSON(ctx, ls, localstate.SecretChecks, checkKey(u), secretCheckDoc{State: u.State, CheckedAt: at}); err != nil {
			return err
		}
	}
	return nil
}

// loadChecks fills State and CheckedAt from the stored checks; last is the
// newest check time (zero when none was stored).
func (o ops) loadChecks(ctx context.Context, us []SecretUse) (last time.Time, err error) {
	src, ok := o.env.(LocalStateSource)
	if !ok {
		return time.Time{}, nil
	}
	ls, err := src.LocalState(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { err = errors.Join(err, ls.Close()) }()
	for i, u := range us {
		if u.Manager == "" {
			continue
		}
		d, found, gerr := localstate.GetJSON[secretCheckDoc](ctx, ls, localstate.SecretChecks, checkKey(u))
		if gerr != nil {
			return time.Time{}, gerr
		}
		if !found {
			continue
		}
		us[i].State, us[i].CheckedAt = d.State, d.CheckedAt.UTC().Format(time.DateOnly)
		if d.CheckedAt.After(last) {
			last = d.CheckedAt
		}
	}
	return last, nil
}
