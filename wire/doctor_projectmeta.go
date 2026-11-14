package wire

// doctor_projectmeta.go — damaged project files doctor names and --fix
// repairs. I-15: a row file an editor saved with a UTF-8 byte order mark
// (warning; --fix strips it). H-3: a deleted .lore/data/_meta.json (the
// project marker: its format _v and project id) went unnoticed. doctor said
// HEALTHY, reads quietly assumed the oldest format, and `git commit -a`
// committed the deletion. Doctor now names it as an error with the git
// line that puts it back, and doctor --fix puts it back from HEAD when the
// work tree is the only place it is missing.

import (
	"context"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/row"
)

// DoctorCheckProjectMarker names the deleted-project-marker check.
const DoctorCheckProjectMarker DoctorCheck = "project_marker"

// projectMarkerRel is the marker's path from the project root, slash form.
const projectMarkerRel = loreFolderName + "/" + rowfs.DataDir + "/" + rowfs.MetaFileName

// DoctorProjectMarker reports a project marker that HEAD has and the work
// tree lacks; with fix it checks it out from HEAD (nothing else changes).
func (a *App) DoctorProjectMarker(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return nil, nil
	}
	root := a.loreRootFrom(top)
	if !loreFolderAt(root) {
		return nil, nil // no project here: the repo class check says so
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(projectMarkerRel))); err == nil || !os.IsNotExist(err) {
		return nil, nil
	}
	if _, err := (gitRun{dir: root, args: []string{"cat-file", "-e", "HEAD:./" + projectMarkerRel}}).output(ctx); err != nil {
		return nil, nil // never committed: nothing git can put back
	}
	restore := "git -C " + root + " checkout HEAD -- " + projectMarkerRel
	f := DoctorFinding{Check: DoctorCheckProjectMarker, Level: DoctorError,
		Message: projectMarkerRel + " is deleted from the work tree; it records the project's id and format, and reads assume the oldest format without it",
		Fix:     restore}
	if fix {
		if _, err := (gitRun{dir: root, args: []string{"checkout", "HEAD", "--", projectMarkerRel}}).output(ctx); err != nil {
			return []DoctorFinding{f}, nil // the line still names the fix to run by hand
		}
		f.Fixed = true
		f.Message = projectMarkerRel + " was deleted from the work tree; put it back from HEAD"
	}
	return []DoctorFinding{f}, nil
}

// DoctorCheckRowBOM names the byte-order-mark check.
const DoctorCheckRowBOM DoctorCheck = "row_file_bom"

// DoctorRowBOM reports the project's row files that start with a UTF-8
// byte order mark (I-15: Notepad saves one); lore reads them, but an older
// lore refuses them, so with fix the mark is stripped (nothing else in the
// file changes) and the file waits for the person's commit.
func (a *App) DoctorRowBOM(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return nil, nil
	}
	root := a.loreRootFrom(top)
	data := filepath.Join(root, loreFolderName, rowfs.DataDir)
	var out []DoctorFinding
	err := filepath.WalkDir(data, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() || filepath.Ext(p) != ".json" {
			return nil // an unreadable entry is the row_file check's to report
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		b, rerr := os.ReadFile(filepath.Clean(p))
		if rerr != nil || !row.HasBOM(b) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		f := DoctorFinding{Check: DoctorCheckRowBOM, Level: DoctorWarning,
			Message: rel + " starts with a UTF-8 byte order mark (an editor such as Notepad added it); lore reads it, an older lore leaves the row out",
			Fix:     "lore doctor --fix strips it"}
		if fix {
			info, serr := os.Stat(p)
			if serr == nil {
				if werr := os.WriteFile(p, row.TrimBOM(b), info.Mode().Perm()); werr == nil {
					f.Fixed = true
					f.Message = rel + ": removed the UTF-8 byte order mark an editor added; commit the file"
				}
			}
		}
		out = append(out, f)
		return nil
	})
	return out, err
}
