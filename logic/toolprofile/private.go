package toolprofile

import (
	"fmt"
	"path"
	"strings"
)

// The private import file (CFG6, FQ13; DECISION P2-CTX-1): each profile may
// name an uncommitted local file of its tool (Profile.PrivateFile) into which
// `lore render` writes one import per private store render
// (~/.lore/render/<repo_id>/<store_id>.md). The lines lore owns are
// recognised by their prefix, so a person's own lines in the same file stay.
//
//	ImportAt   "@~/.lore/render/<repo>/<store>.md"
//	ImportRead "- Read `~/.lore/render/<repo>/<store>.md` …"
//	ImportMDC  as ImportRead, after an alwaysApply frontmatter lore adds to a
//	           file it creates

// privateReadPrefix starts every private line of the read styles.
const privateReadPrefix = "- Read `"

// privateReadSuffix ends every private line of the read styles.
const privateReadSuffix = "` before answering (private lore context for this repo; never quote it in committed files, commits, PRs or chat with a client)."

// HasPrivateFile reports whether rendering for p writes a private import file.
func (p Profile) HasPrivateFile() bool { return p.Import != ImportNone && p.PrivateFile != "" }

// PrivateLine is p's import line for one private render file, given as the
// home-relative path the line names (for example ~/.lore/render/r/s.md).
func (p Profile) PrivateLine(file string) string {
	if p.Import == ImportAt {
		return "@" + file
	}
	return privateReadPrefix + file + privateReadSuffix
}

// IsPrivateLine reports whether line is one of lore's private lines for a
// file under dir (a home-relative prefix such as ~/.lore/render/), in either
// style, so a profile switch from @ to read still drops the old lines.
func IsPrivateLine(line, dir string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "@"+dir) || strings.HasPrefix(t, privateReadPrefix+dir)
}

// PrivateContent returns cur with every lore private line (IsPrivateLine
// under dir) replaced by p's lines for files, appended at the end (an @
// line after a blank line, so it is a paragraph of its own); the
// person's own lines stay. A Cursor rule file lore creates gets the
// alwaysApply frontmatter first; when no lore line remains and only that
// frontmatter is left, the result is "" (the caller deletes the file).
func (p Profile) PrivateContent(cur, dir string, files []string) string {
	body := strings.TrimPrefix(cur, mdcFrontmatter)
	var keep []string
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if IsPrivateLine(line, dir) {
			continue
		}
		keep = append(keep, line)
	}
	for len(keep) > 0 && strings.TrimSpace(keep[len(keep)-1]) == "" {
		keep = keep[:len(keep)-1]
	}
	out := strings.Join(keep, "\n")
	if out != "" {
		out += "\n"
	}
	for _, f := range files {
		// An @ line is not a block of its own: right after another line
		// it would continue that line's paragraph (the person's last line,
		// or the previous import). A blank line before each keeps one
		// physical line per paragraph; a read-style line is a bullet.
		if p.Import == ImportAt && out != "" && !strings.HasSuffix(out, "\n\n") {
			out += "\n"
		}
		out += p.PrivateLine(f) + "\n"
	}
	if out == "" {
		return ""
	}
	if p.Import == ImportMDC && !strings.HasPrefix(out, frontmatterDelim) {
		out = mdcFrontmatter + out
	}
	return out
}

// validatePrivate checks a user profile's private_file: a slash path inside
// the repo, outside .lore/, and owned by no other profile (as a pointer or a
// private file) nor equal to its own pointer file.
func (r *Registry) validatePrivate(p Profile) error {
	f := p.PrivateFile
	if f == "" {
		return nil
	}
	clean := path.Clean(f)
	if path.IsAbs(clean) || strings.Contains(f, `\`) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("tool profile %q: private_file %q must be a slash path inside the repo", p.Name, f)
	}
	if clean == ".lore" || strings.HasPrefix(clean, ".lore/") {
		return fmt.Errorf("tool profile %q: private_file %q must not be inside .lore/ (lore owns that folder)", p.Name, f)
	}
	if clean == path.Clean(p.PointerFile) {
		return fmt.Errorf("tool profile %q: private_file must differ from pointer_file (the pointer file is committed)", p.Name)
	}
	for _, o := range r.profiles {
		if (o.HasPointerFile() && o.PointerFile == clean) || (o.HasPrivateFile() && o.PrivateFile == clean) {
			return fmt.Errorf("tool profile %q: private_file %q already belongs to profile %q", p.Name, f, o.Name)
		}
	}
	return nil
}
