// sync_core.go — the 0.1.18 half of `lore sync`: after the v0.1.17 pass,
// every new-format store attached in .lore/stores.yaml is synced through
// the core (spec 6.1 "lore sync per store"; O12b: there is no separate
// stores noun, `lore sync` is the one sync command).
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// syncReportJSON is the `lore sync --json` data: the v0.1.17 pass report
// with the new-format stores' results added under "stores". The --json
// contract only ever adds fields (OQ5), so every v0.1.17 key keeps its name
// and place; "stores" is left out when no new-format store is attached.
type syncReportJSON struct {
	lsync.Report
	Stores []opsstores.Report `json:"stores,omitempty"`
}

// syncCoreStores syncs every new-format store through the core. A nil core
// (the working directory or home could not be read at start-up, which
// mountCore already reported) has no stores to sync.
func syncCoreStores(ctx context.Context) (opsstores.SyncOut, error) {
	if coreApp == nil {
		return opsstores.SyncOut{}, nil
	}
	return coreApp.SyncStores(ctx, "")
}

// printCoreSyncReport prints one line per new-format store and one indented
// line per problem, in the style of printSyncReport.
func printCoreSyncReport(out opsstores.SyncOut) {
	for _, r := range out.Stores {
		line := "store " + r.Store + ": sent " + strconv.Itoa(r.Sent) +
			", received " + strconv.Itoa(r.Received) +
			", merged " + strconv.Itoa(r.Merged) +
			", refused " + strconv.Itoa(r.Refused)
		if r.Offline {
			line += style.Muted(" (offline: local changes kept, sent on the next sync)")
		}
		fmt.Println(line)
		for _, p := range r.Problems {
			fmt.Println("  " + p)
		}
	}
}

// coreSyncRefusal is the error `lore sync` returns when a new-format store
// refused rows: E_STALE (exit 23), printed here because main treats every
// core error as already printed (coreExitCode). Nothing was lost: the
// refused rows stay local until resolved (spec 10.4).
func coreSyncRefusal(out opsstores.SyncOut) error {
	refused := 0
	for _, r := range out.Stores {
		refused += r.Refused
	}
	if refused == 0 {
		return nil
	}
	e := errs.New(errs.Stale, strconv.Itoa(refused)+" row(s) changed on both sides and could not be merged automatically").
		WithHint("the rows are listed above; edit each to the text to keep, then run lore sync again")
	fmt.Fprintln(os.Stderr, style.Error("ERROR: ")+e.Error())
	fmt.Fprintln(os.Stderr, hintPrefix+e.Hint)
	return e
}
