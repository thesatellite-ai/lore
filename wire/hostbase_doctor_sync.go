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
//	data_dir_gitignored      git ignores a lore file under .lore/data: a probe
//	                         per file kind (adapters/dataignore, the same check
//	                         `lore sync` refuses on) or a real row file that
//	                         git status --ignored lists; never OS clutter
//	                         (.DS_Store, ._name: a global core.excludesFile
//	                         ignoring those says nothing about lore data)
//	uncommitted_files        git status lists a changed file under .lore/data
//	                         (OS clutter not counted)
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
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/dataignore"
	"github.com/thesatellite-ai/lore/adapters/reposync"
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

// doctorLinkedByRemoteNote is the [sync] line of a repo with no lore data
// that a store links by its remote: lore writes nothing into it, so no
// merge driver, hooks or ignore lines are installed (by design).
func doctorLinkedByRemoteNote(remote string) string {
	return "this repo holds no lore data: a store links it by its remote " + remote + ", its rows live in that store folder, and lore writes no file into it (no .lore/, merge driver or .gitignore lines); only lore org link puts lore's commit-msg leak check into the clone it runs in (lore whoami says when it is missing)"
}

// doctor [sync] lines.
const (
	doctorDataIgnoredWarning = ".lore/data is gitignored, so lore knowledge is never shared; `lore doctor --fix` re-includes it in .gitignore (then commit .gitignore), or remove the ignore rule (lore sync refuses with E_SYNC_DATA_IGNORED meanwhile)"
	// doctorDataIgnoredPrefix starts the warning when lore files under
	// .lore/data are ignored by a rule (named, B1-8: a broad rule such as
	// `_*` catches only _meta.json, and "is gitignored" alone read as false
	// when git check-ignore on a row file printed nothing).
	doctorDataIgnoredPrefix = "git ignores lore files under .lore/data, so they are never shared: "
	doctorDataIgnoredFix    = "; `lore doctor --fix` re-includes them in .gitignore (then commit .gitignore), or remove the ignore rule (lore sync refuses with E_SYNC_DATA_IGNORED meanwhile)"
	doctorNoMergeDriverNote = "lore merge driver not configured in this clone (a read leaves git as it is: `lore sync`, `lore doctor --fix` or the next lore write here installs it, unless LORE_SYNC_GIT=0)"
	// doctorBrokenMergeDriverNote: merge.lore.driver is set to something
	// that is not a lore merge driver (a missing absolute program is
	// repaired by the next lore command, see wire/host/gitwire.go).
	doctorBrokenMergeDriverNote = "git config " + mergeDriverConfigKey + " does not run `lore merge-driver`, so lore files merge wrongly; run `lore doctor --fix` to install lore's driver"
)

// dataDirPresent reports whether the store's .lore/data folder exists
// (doctor's data_dir_present, and `lore sync status`'s).
func dataDirPresent(d conflictDataDir) bool {
	fi, err := os.Stat(filepath.Join(d.Dir(), filepath.FromSlash(d.DataPath())))
	return err == nil && fi.IsDir()
}

// trackedDataFiles is how many files git tracks under the store's
// .lore/data (doctor's tracked_files, and `lore sync status`'s); 0 when
// the folder is not in a git checkout.
func trackedDataFiles(ctx context.Context, d conflictDataDir) int {
	out, err := gitRun{dir: d.Dir(), args: []string{"ls-files", "-z", "--", d.DataPath()}}.output(ctx)
	if err != nil {
		return 0
	}
	return len(nulFields(out))
}

// repoDataFacts is data_dir_present and tracked_files of the repo store;
// false, 0 when the repo store is not a row-file backend.
func (a *App) repoDataFacts(ctx context.Context) (present bool, tracked int) {
	st, err := a.state(ctx)
	if err != nil {
		return false, 0
	}
	d, ok := concrete(st.byName[store.SourceRepo]).(conflictDataDir)
	if !ok || !dataDirPresent(d) {
		return false, 0
	}
	return true, trackedDataFiles(ctx, d)
}

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
	s.DataDirPresent = dataDirPresent(d)
	if s.DataDirPresent {
		if pf, ferr := ProjectFormatAt(gitTopLevel(ctx, dir)); ferr != nil {
			s.Format = "unreadable: " + errLine(ferr)
		} else if pf.Format != rowfs.ProjectFormatUnknown {
			s.Format = ProjectFormatLine(pf)
		}
	}
	if s.DataDirPresent {
		a.doctorSyncFiles(ctx, d, s)
		// E-8: modify/delete conflicts carry no markers; count every
		// unmerged lore file and name the stopped git operation's way out
		if files := unmergedLoreFiles(ctx, dir, dataRel); len(files) > 0 {
			s.OpenConflicts = max(s.OpenConflicts, len(files))
			_, derr := gitRun{dir: dir, args: []string{"config", "--get", mergeDriverConfigKey}}.output(ctx)
			warn(unmergedLine(gitOpInProgress(ctx, dir), files, derr == nil))
		}
	}
	if eng, eerr := a.Engine(ctx); eerr == nil {
		if rows, lerr := eng.List(ctx, store.SourceRepo, schema.TableProjects); lerr == nil {
			s.ProjectFiles = len(rows)
		}
	}
	// fmt1: in a project on the 0.1.17 format the one next step is lore
	// migrate run (the format line says it); the git wiring and .gitignore
	// notes would point at lore fix (which refuses there) or at a
	// .gitignore edit that waits for the migration, so they are left out
	formatOld := s.DataDirPresent && ProjectFormatOld(gitTopLevel(ctx, dir))
	git := func(args ...string) ([]byte, error) { return gitRun{dir: dir, args: args}.output(ctx) }
	if out, gerr := git("rev-parse", "--is-inside-work-tree"); gerr != nil || strings.TrimSpace(string(out)) != gitInsideTrue {
		return s // not a git checkout: the git fields do not apply
	}
	s.InGitRepo = true
	switch out, gerr := git("config", "--get", mergeDriverConfigKey); {
	case gerr != nil || strings.TrimSpace(string(out)) == "":
		if remote := linkedByRemote(ctx, a.opts); remote != "" {
			note(doctorLinkedByRemoteNote(remote)) // nothing is ever wired in such a repo
		} else if !formatOld {
			note(doctorNoMergeDriverNote)
		}
	case !reposync.DriverRuns(string(out)):
		note(doctorBrokenMergeDriverNote)
	default:
		s.MergeDriverOK = true
	}
	s.TrackedFiles = trackedDataFiles(ctx, d)
	// A rendered file git left unmerged (no keep-ours rule yet, or a merge
	// without the driver) is an open conflict too (devco D-19): lore can
	// write it again, and git still waits for the add.
	if out, gerr := git("diff", "--name-only", "-z", "--relative", "--diff-filter=U"); gerr == nil {
		for _, p := range nulFields(out) {
			if IsRenderedFile(p) {
				s.OpenConflicts++
				warn(doctorRenderedConflictLine(p))
			}
		}
	}
	if out, gerr := git("status", "--porcelain=v1", "-z", "--ignored", "--untracked-files=all", "--", dataRel); gerr == nil {
		entries := nulFields(out)
		for i := 0; i < len(entries); i++ {
			e := entries[i]
			skipSource := e[0] == gitStatusRenamed || e[0] == gitStatusCopied
			p := e[min(len(e), len(gitStatusIgnored)):] // "XY path"
			switch {
			case dataignore.IsOSJunk(p): // OS clutter: not lore's, ignored or not
			case strings.HasPrefix(e, gitStatusIgnored):
				if dataignore.IsRowFile(p) {
					s.DataIgnored = true // a real row file git will not take
				}
			default:
				s.Uncommitted++
			}
			if skipSource {
				i++ // the source path follows as its own entry
			}
		}
	}
	// B1-8: the rules are only checked where rows live (.lore/data): a
	// repo with no lore project of its own (a .lore/ holding only a
	// LORE.md, a repo a store links by its remote) has no lore file a rule
	// could hide, and warning about files that do not exist made it
	// DEGRADED for nothing (the git wiring re-includes only there too)
	var hit map[string]string
	if s.DataDirPresent {
		probes := dataignore.Probes() // under dataignore.DataRel; this store's data path may differ
		for i, p := range probes {
			probes[i] = dataRel + strings.TrimPrefix(p, dataignore.DataRel)
		}
		var perr error
		if hit, perr = dataignore.Ignored(ctx, dir, probes); perr == nil && len(hit) > 0 {
			s.DataIgnored = true // a whole kind of lore file is ignored
		}
	}
	if s.DataIgnored {
		warn(dataIgnoredLine(hit))
	}
	// A read never adds these lines (wire/gitignorelines.go): the finding
	// names them and the command that does.
	if missing := MissingGitignoreLines(ctx, dir); len(missing) > 0 && HoldsLoreData(dir) && !formatOld {
		note(doctorGitignoreLinesNote(missing)) // clutter in a teammate's git status, never lost data: a note
	}
	// A repo with no lore data of its own (a client repo) never gets its
	// .gitignore changed, not even by --fix (plain files sweep 3.8): lore's
	// local files stay out of git status through .git/info/exclude only.
	return s
}

// doctorRenderedConflictLine is the [sync] warning of a rendered file git
// left unmerged: lore writes it again from the merged rows, then git needs
// the add that marks it resolved.
func doctorRenderedConflictLine(rel string) string {
	return rel + " has a git merge conflict; lore generates it, so `lore render && git add " + rel + "` settles it"
}

// doctorGitignoreLinesNote is the [sync] line of a .gitignore that lacks
// lore's local files (a v0.1.17 repo before its first 0.1.18 write).
func doctorGitignoreLinesNote(missing []string) string {
	return ".gitignore does not list lore's local files (" + strings.Join(missing, ", ") + "), so a teammate's clone shows them in git status; `lore doctor --fix` adds them (then commit .gitignore); a read creates none of them"
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

// dataIgnoredLine is the [sync] warning of ignored lore data: each kind of
// lore file a rule ignores, with the rule ("<file>:<line>:<pattern>"), so
// the reader can check it with git check-ignore -v; the generic line when
// only real row files were found ignored (git status names those).
func dataIgnoredLine(hit map[string]string) string {
	if len(hit) == 0 {
		return doctorDataIgnoredWarning
	}
	paths := make([]string, 0, len(hit))
	for p := range hit {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	const shown = 3
	parts := make([]string, 0, shown+1)
	for i, p := range paths {
		if i == shown {
			parts = append(parts, "and "+strconv.Itoa(len(paths)-shown)+" more")
			break
		}
		parts = append(parts, dataignore.Shown(p)+" (by "+hit[p]+")")
	}
	return doctorDataIgnoredPrefix + strings.Join(parts, ", ") + doctorDataIgnoredFix
}
