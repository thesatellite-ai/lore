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

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// dataRel is the committed data folder, relative to the project root.
var dataRel = rowfs.LoreDir + "/" + rowfs.DataDir

// dataIgnoreMarker names lore's re-include block in .gitignore (the
// v0.1.17 block marker, so both binaries keep one block).
const dataIgnoreMarker = gitBlockMarker

// dataJunkPatterns are operating-system clutter names ignored again right
// after the re-include (in a .gitignore the later rule wins).
var dataJunkPatterns = []string{".DS_Store", "Desktop.ini", "Thumbs.db", "desktop.ini", "._*"}

// dataUnignoreLines re-include lore's data folder. A negation cannot
// re-include a file whose parent folder is excluded (`.lore/`): that case
// is reported instead.
func dataUnignoreLines() []string {
	out := []string{"!" + dataRel + "/", "!" + dataRel + "/**"}
	for _, p := range dataJunkPatterns {
		out = append(out, dataRel+"/**/"+p)
	}
	return out
}

// maxIgnoredShown caps the ignored paths an error lists (a whole excluded
// folder hides every table; the first few name the rule).
const maxIgnoredShown = 5

// dataProbePath is the file name stem used inside table folders: not a
// valid row id, so a stray file by that name is skipped, never read.
const dataProbePath = "sample"

// dataProbes are the paths, relative to the project root, of one file of
// every kind lore commits under .lore/data.
func dataProbes() []string {
	out := []string{path.Join(dataRel, rowfs.MetaFileName), path.Join(dataRel, rowfs.PurgedFileName)}
	for _, t := range schema.Builtin() {
		if !t.Synced || t.MachineLocal {
			continue
		}
		out = append(out, path.Join(dataRel, t.Name.String(), dataProbePath+rowfs.RowFileExt))
	}
	return out
}

// ignoredData maps each probe git ignores in root to the rule responsible
// ("<source>:<line>:<pattern>"). A git that cannot run reports nothing.
func ignoredData(ctx context.Context, root string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-v", "--no-index", "--stdin", "-z")
	cmd.Stdin = strings.NewReader(strings.Join(dataProbes(), "\x00") + "\x00")
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return nil, nil // exit 1: nothing ignored
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git check-ignore", err)
	}
	// -z -v: <source> NUL <linenum> NUL <pattern> NUL <pathname> NUL ...
	f := bytes.Split(out, []byte{0})
	res := map[string]string{}
	for i := 0; i+3 < len(f); i += 4 {
		pattern := string(f[i+2])
		if strings.HasPrefix(pattern, "!") {
			continue // matched by a negation: not ignored
		}
		res[string(f[i+3])] = string(f[i]) + ":" + string(f[i+1]) + ":" + pattern
	}
	return res, nil
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
	if err := ensureFileBlock(gi, dataIgnoreMarker, dataUnignoreLines()); err != nil {
		return false, nil, err
	}
	now, _ := os.ReadFile(filepath.Clean(gi))
	changed := !bytes.Equal(now, original)
	if ignored, err = ignoredData(ctx, root); err != nil {
		return changed, nil, err
	}
	if changed && len(ignored) >= before {
		if existed {
			err = os.WriteFile(gi, original, gitignoreMode)
		} else {
			err = os.Remove(gi)
		}
		if err != nil {
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
