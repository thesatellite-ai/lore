package app

// Process guards that run before any command (v0.1.17 rootCmd
// PersistentPreRunE and explainStorageError): the root refusal and the
// disk-full explanation.

import (
	"context"
	"os"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// envAllowRoot is the override of the root refusal (CLI_REFERENCE 5.x
// E_ROOT_REFUSED: "`LORE_ALLOW_ROOT=1` overrides"). v0.1.17's legacy
// MINI_ALLOW_ROOT is not read: the aicoder-mini name is gone.
const envAllowRoot = "LORE_ALLOW_ROOT"

// envOn is the one value that turns envAllowRoot on.
const envOn = "1"

// rootUID is the effective user id of root.
const rootUID = 0

// geteuid is the effective user id; a variable so the root refusal can be
// tested without running as root. os.Geteuid returns -1 on Windows, so
// Windows is never refused.
var geteuid = os.Geteuid

// refuseRoot refuses to run as root unless LORE_ALLOW_ROOT=1: a command run
// with sudo would write root-owned row files, caches and locks into the
// user's repo and ~/.lore, which the user's own later commands then cannot
// write (v0.1.17 E_ROOT_REFUSED, exit 23).
func refuseRoot() error {
	if geteuid() != rootUID || os.Getenv(envAllowRoot) == envOn {
		return nil
	}
	return errs.New(errs.RootRefused, "lore refuses to run as root: files it writes would be owned by root").
		WithHint("run lore as your own user (without sudo), or set " + envAllowRoot + "=" + envOn + " if you really mean it")
}

// readOnlyHomeHookName names the read-only-home hook.
const readOnlyHomeHookName = "read-only-home"

// readOnlyHomeHook refuses a writing operation up front when lore cannot
// write its home (wire.RefuseWriteInReadOnlyHome, devco:U-4): reads go on
// from the read-only home, a write says which folder and how to fix it
// instead of storing a row and failing E_INTERNAL on its audit record.
func readOnlyHomeHook(home string) hosthooks.Hook {
	return hosthooks.Hook{
		Name: readOnlyHomeHookName,
		Before: func(_ context.Context, run *hosthooks.Run) error {
			if run.Entry == nil || run.Entry.Spec().Access != op.AccessWrite {
				return nil
			}
			return wire.RefuseWriteInReadOnlyHome(home)
		},
	}
}

// diskFullHookName names the disk-full hook (host.Replace keys on it).
const diskFullHookName = "disk-full"

// diskFullHook turns a full disk under any operation into E_DISK_FULL with
// a hint instead of E_INTERNAL (sqlite.ExplainDiskFull: SQLITE_FULL from
// the cache, ENOSPC from a row file write).
func diskFullHook() hosthooks.Hook {
	return hosthooks.Hook{
		Name: diskFullHookName,
		OnError: func(_ context.Context, _ *hosthooks.Run, err error) error {
			return sqlite.ExplainDiskFull(err)
		},
	}
}
