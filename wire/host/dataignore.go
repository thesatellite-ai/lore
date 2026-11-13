package host

// dataignore.go — lore's row files must reach git (OWNERQ-9 / G-DECIDE-9,
// v0.1.17 sync_wire.go ensureDataNotIgnored): when a repo's own ignore
// rules hide a file under .lore/data (`*.json`, `_*`, `snapshots/`), the
// git wiring appends a re-include block to the project's .gitignore and
// checks again; an edit that re-included nothing (the whole .lore/ folder
// is excluded) is undone, so .gitignore only changes when it helps.
// `lore sync` then fails closed with E_SYNC_DATA_IGNORED (exit 23, nothing
// pushed) while any row file stays ignored: teammates would silently miss
// those rows.
//
//	probes (_meta.json, _purged.json, <table>/<id>.json per synced table)
//	  ─▶ git check-ignore -v ─▶ none: done
//	  ─▶ some: re-include block in .gitignore ─▶ check again ─▶ nothing
//	     re-included: restore .gitignore ─▶ what is still ignored, with
//	     the rule responsible
//
// The probes, the check-ignore call and the OS clutter list live in
// adapters/dataignore, shared with `lore doctor`'s [sync] section.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/dataignore"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// dataRel is the committed data folder, relative to the project root.
const dataRel = dataignore.DataRel

// dataIgnoreMarker names lore's re-include block in .gitignore (the
// v0.1.17 block marker, so both binaries keep one block).
const dataIgnoreMarker = gitBlockMarker

// dataUnignoreLines re-include lore's data folder. A negation cannot
// re-include a file whose parent folder is excluded (`.lore/`): that case
// is reported instead.
func dataUnignoreLines() []string {
	out := []string{"!" + dataRel + "/", "!" + dataRel + "/**"}
	for _, p := range dataignore.OSJunkPatterns { // OS clutter stays ignored (the later rule wins)
		out = append(out, dataRel+"/**/"+p)
	}
	// a dead writer's tmp file stays ignored too (wire.DataTmpLine)
	return append(out, wire.DataTmpLine)
}

// maxIgnoredShown caps the ignored paths an error lists (a whole excluded
// folder hides every table; the first few name the rule).
const maxIgnoredShown = 5

// ignoredData maps each lore file kind git ignores in root (its probe
// path) to the rule responsible ("<source>:<line>:<pattern>").
func ignoredData(ctx context.Context, root string) (map[string]string, error) {
	return dataignore.Ignored(ctx, root, nil)
}

// restoreGitignore puts root's .gitignore back as it was before the block
// was added (existed false: it did not exist), inside root only.
func restoreGitignore(root string, original []byte, existed bool) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	if existed {
		err = r.WriteFile(gitignoreName, original, gitignoreMode)
	} else {
		err = r.Remove(gitignoreName)
	}
	return errors.Join(err, r.Close())
}

// EnsureDataNotIgnored is the re-include step (see the file comment): it
// returns whether .gitignore changed and what stays ignored, each as
// "path (ignored by rule)", sorted.
func EnsureDataNotIgnored(ctx context.Context, root string) (bool, []string, error) {
	ignored, err := ignoredData(ctx, root)
	if err != nil || len(ignored) == 0 {
		return false, nil, err
	}
	gi := filepath.Join(root, gitignoreName)
	original, rerr := os.ReadFile(filepath.Clean(gi))
	existed := rerr == nil
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return false, nil, errs.Wrap(errs.Internal, "read "+gitignoreName, rerr)
	}
	before := len(ignored)
	if berr := ensureFileBlock(gi, dataIgnoreMarker, dataUnignoreLines()); berr != nil {
		return false, nil, berr
	}
	now, _ := os.ReadFile(filepath.Clean(gi))
	changed := !bytes.Equal(now, original)
	if ignored, err = ignoredData(ctx, root); err != nil {
		return changed, nil, err
	}
	if changed && len(ignored) >= before {
		if err = restoreGitignore(root, original, existed); err != nil {
			return true, nil, errs.Wrap(errs.Internal, "restore "+gitignoreName, err)
		}
		changed = false
	}
	still := make([]string, 0, len(ignored))
	for p, rule := range ignored {
		still = append(still, p+" (ignored by "+rule+")")
	}
	sort.Strings(still)
	return changed, still, nil
}

// stillIgnoredData is what stays ignored in root: after the re-include
// step when fix is set, else as the rules stand.
func stillIgnoredData(ctx context.Context, root string, fix bool) ([]string, error) {
	if fix {
		_, still, err := EnsureDataNotIgnored(ctx, root)
		return still, err
	}
	ignored, err := ignoredData(ctx, root)
	if err != nil {
		return nil, err
	}
	still := make([]string, 0, len(ignored))
	for p, rule := range ignored {
		still = append(still, p+" (ignored by "+rule+")")
	}
	sort.Strings(still)
	return still, nil
}

// dataIgnoredError is `lore sync`'s refusal while row files stay ignored.
func dataIgnoredError(still []string) error {
	shown := still
	more := ""
	if len(still) > maxIgnoredShown {
		shown = still[:maxIgnoredShown]
		more = "\n  and " + strconv.Itoa(len(still)-maxIgnoredShown) + " more"
	}
	return errs.New(errs.SyncDataIgnored, "git ignores lore data, so teammates would not receive it; nothing was pushed:\n  "+strings.Join(shown, "\n  ")+more).
		WithHint("change that rule so " + dataRel + "/ is not excluded (a negation cannot re-include files inside an ignored folder), then run `lore sync` again")
}
