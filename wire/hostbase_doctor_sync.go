package wire

// hostbase_doctor_sync.go — the [sync] section of `lore doctor` for a
// binary without lore.db (E-GAPS-1 G4; v0.1.17 saas/cmd/cli/doctor.go
// doctorSync). The old section read lore.db's sync tables; in 0.1.18 the
// row files are the truth, so only the fields with a source on disk or in
// git are filled:
//
//	data_dir_present         .lore/data exists
//	tracked_files            files git tracks under .lore/data
//	open_conflicts           row files holding conflict markers (BranchConflicts)
//	file_errors              unusable row files (the backend's Problems; each is
//	                         already a doctor error through DoctorRowFiles, so
//	                         no second line here)
//	project_files            live project rows of the repo store (DoctorProjects
//	                         warns on more than one)
//	in_git_repo              git rev-parse --is-inside-work-tree
//	merge_driver_configured  git config merge.lore.driver
//	data_dir_gitignored      git status --ignored lists a file under .lore/data
//	uncommitted_files        git status lists a changed file under .lore/data
//
// The lore.db counters (baselined, pending_exports, trash_rows, errors,
// dangling_references, duplicate_groups) have no source here and stay
// zero, which the report omits (opsdoctor.Sync.NoLoreDB). The live-rows
// state (live ref, hooks, last fetch) is the live hook's notes
// (wire/host/live_doctor.go).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// mergeDriverConfigKey is the git config key the git wiring sets
// (wire/host/gitwire.go: merge.<mergeDriverLore>.driver).
const mergeDriverConfigKey = "merge.lore.driver"

// git status --porcelain=v1 -z: "!! " is an ignored file, every other
// entry a change not committed yet; after a rename (R) or copy (C) entry
// its source path follows as its own entry.
const (
	gitStatusIgnored = "!! "
	gitStatusRenamed = 'R'
	gitStatusCopied  = 'C'
	// gitInsideTrue is rev-parse --is-inside-work-tree's answer in a work tree.
	gitInsideTrue = "true"
)

// doctor [sync] lines.
const (
	doctorDataIgnoredWarning = ".lore/data is gitignored, so lore knowledge is never shared; remove the ignore rule (lore sync refuses with E_SYNC_DATA_IGNORED meanwhile)"
	doctorNoMergeDriverNote  = "lore merge driver not configured in this clone (the next lore command installs it, unless LORE_SYNC_GIT=0)"
)

// doctorSync fills the [sync] section of the repo store; nil when the repo
// store is not a row-file backend. warn degrades, note does not.
func (a *App) doctorSync(ctx context.Context, warn, note func(string)) *opsdoctor.Sync {
	st, err := a.state(ctx)
	if err != nil {
		return nil
	}
	d, ok := concrete(st.byName[store.SourceRepo]).(conflictDataDir)
	if !ok {
		return nil
	}
	s := &opsdoctor.Sync{NoLoreDB: true}
	dir, dataRel := d.Dir(), d.DataPath()
	if fi, serr := os.Stat(filepath.Join(dir, filepath.FromSlash(dataRel))); serr == nil && fi.IsDir() {
		s.DataDirPresent = true
	}
	if s.DataDirPresent {
		a.doctorSyncFiles(ctx, d, s)
	}
	if eng, eerr := a.Engine(ctx); eerr == nil {
		if rows, lerr := eng.List(ctx, store.SourceRepo, schema.TableProjects); lerr == nil {
			s.ProjectFiles = len(rows)
		}
	}
	git := func(args ...string) ([]byte, error) { return gitRun{dir: dir, args: args}.output(ctx) }
	if out, gerr := git("rev-parse", "--is-inside-work-tree"); gerr != nil || strings.TrimSpace(string(out)) != gitInsideTrue {
		return s // not a git checkout: the git fields do not apply
	}
	s.InGitRepo = true
	if out, gerr := git("config", "--get", mergeDriverConfigKey); gerr == nil && strings.TrimSpace(string(out)) != "" {
		s.MergeDriverOK = true
	} else {
		note(doctorNoMergeDriverNote)
	}
	if out, gerr := git("ls-files", "-z", "--", dataRel); gerr == nil {
		s.TrackedFiles = len(nulFields(out))
	}
	if out, gerr := git("status", "--porcelain=v1", "-z", "--ignored", "--untracked-files=all", "--", dataRel); gerr == nil {
		entries := nulFields(out)
		for i := 0; i < len(entries); i++ {
			e := entries[i]
			if strings.HasPrefix(e, gitStatusIgnored) {
				s.DataIgnored = true
				continue
			}
			s.Uncommitted++
			if e[0] == gitStatusRenamed || e[0] == gitStatusCopied {
				i++ // the source path follows as its own entry
			}
		}
	}
	if s.DataIgnored {
		warn(doctorDataIgnoredWarning)
	}
	return s
}

// doctorSyncFiles fills open_conflicts and file_errors from the repo
// store's unusable row files.
func (a *App) doctorSyncFiles(ctx context.Context, d conflictDataDir, s *opsdoctor.Sync) {
	probs, err := d.Problems(ctx)
	if err != nil {
		return // DoctorRowFiles reports the folder that cannot be read
	}
	marked := map[string]bool{}
	if files, cerr := a.BranchConflicts(ctx); cerr == nil {
		for _, f := range files {
			marked[f.Path] = true
		}
	}
	s.OpenConflicts = len(marked)
	for _, p := range probs {
		kind := opsdoctor.FileErrorInvalid
		if marked[p.Path] {
			kind = opsdoctor.FileErrorConflict
		}
		s.FileErrors = append(s.FileErrors, opsdoctor.FileError{Path: d.DataPath() + "/" + p.Path, Kind: kind, Detail: oneLine(p.Reason)})
	}
}

// nulFields splits git's -z output into its entries.
func nulFields(b []byte) []string {
	var out []string
	for _, f := range bytes.Split(b, []byte{0}) {
		if len(f) > 0 {
			out = append(out, string(f))
		}
	}
	return out
}
