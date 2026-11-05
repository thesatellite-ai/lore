// coord_cmd.go — the new top-level coordination commands of lore for teams
// (LORE_TEAM_SPEC §1 "Commands", §7): `lore agent start`, `lore hold
// claim|release` (v0.1.17's `lore resource claim|release`, renamed for good
// by RQ11; `lore resource` now names resource kinds) and more. addCoordCommands is ONE line in main.go, so
// adding a P4 command never touches the shared root file again.
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/style"

	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// `lore agent start` is the operation agent.start (ops/agent over
// agentHost and the App's roster, P3-RUNS-6).

// ---------------------------------------------------------------- hold

// Hold places (§7.2; CLI_REFERENCE 3.3 --on).
const (
	scopeProject = "project"
	scopeMachine = "machine"
	flagTTL      = "ttl"
	flagOn       = "on"
	// defaultHoldTTL is a hold's length when --ttl is not given:
	// the claim lease, so one heartbeat interval keeps both alive.
	defaultHoldTTL = 0
)

// runHoldClaim is hold.claim (RES 3.17): hold a named resource on the
// project's signals ref, exclusively, until the ttl passes (default
// lease.ttl); claiming again renews. --on machine is not built.
func runHoldClaim(ctx context.Context, out io.Writer, in opscoord.HoldClaimIn) error {
	if in.On == scopeMachine {
		return errcodes.New(errcodes.NotImplemented, "--on machine defers to a machine-wide lock; run the command under your machine's own lock tool (for example flock) instead")
	}
	if in.On != "" && in.On != scopeProject {
		return errcodes.New(errcodes.InvalidInput, "--on is project or machine, not "+in.On)
	}
	w, err := openCoordWorld(ctx)
	if err != nil {
		return err
	}
	if err = w.requireActor(); err != nil {
		return err
	}
	st, err := w.repoSignalsChecked()
	if err != nil {
		return err
	}
	d := in.TTL
	if d <= defaultHoldTTL {
		d = w.Lease.TTL
	}
	var held signals.Signal
	_, err = st.Write(ctx, func(v signals.View) (signals.Change, error) {
		tok, terr := st.Tokens.New()
		if terr != nil {
			return signals.Change{}, terr
		}
		s, derr := signals.DecideHold(v, signals.HoldRequest{Caller: w.Caller(), Folder: w.Folder, Name: in.Name, TTL: d, Token: tok})
		if derr != nil {
			return signals.Change{}, derr
		}
		held = s
		return signals.Change{Put: map[string]signals.Signal{signals.ResourcePath(w.Folder, in.Name): s}}, nil
	})
	if err != nil {
		return coordError(err)
	}
	_, _ = fmt.Fprintf(out, "%s holding %s until %s\n", style.Success("✓"), in.Name, held.Expires.UTC().Format(time.RFC3339))
	return nil
}

// runHoldRelease is hold.release: remove your own hold.
func runHoldRelease(ctx context.Context, out io.Writer, in opscoord.HoldReleaseIn) error {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return err
	}
	if err = w.requireActor(); err != nil {
		return err
	}
	st, err := w.repoSignalsChecked()
	if err != nil {
		return err
	}
	_, err = st.Write(ctx, func(v signals.View) (signals.Change, error) {
		p, derr := signals.DecideUnhold(v, w.Caller(), w.Folder, in.Name)
		return signals.Change{Delete: []string{p}}, derr
	})
	if err != nil {
		return coordError(err)
	}
	_, _ = fmt.Fprintf(out, "%s released %s\n", style.Success("✓"), in.Name)
	return nil
}
