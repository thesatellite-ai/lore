package gitfast

// index.go — reads .git/index (versions 2, 3 and 4) for the questions
// that need only the index, never the work tree: `ls-files --stage` and
// the ":path" (stage 0) object names of cat-file --batch. A split or
// sparse index, or anything this reader does not know, falls back to git.

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// indexEntry is one index entry.
type indexEntry struct {
	mode  uint32
	id    string
	stage int
	path  string
	// extended: the entry has extended flags (intent-to-add,
	// skip-worktree); readers that compare content leave those to git
	extended bool
}

// readIndex parses the work tree's index (or the hook's temporary one,
// indexFile). ok=false: ask git.
func (r *repo) readIndex() ([]indexEntry, bool) {
	if r.top == "" {
		return nil, false
	}
	path := r.indexFile
	if path == "" {
		path = filepath.Join(r.gitDir, "index")
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, true // no index yet: nothing staged
	}
	if err != nil || len(raw) < 12+20 || !bytes.Equal(raw[:4], []byte("DIRC")) {
		return nil, false
	}
	version := binary.BigEndian.Uint32(raw[4:8])
	if version < 2 || version > 4 {
		return nil, false
	}
	n := int(binary.BigEndian.Uint32(raw[8:12]))
	pos := 12
	end := len(raw) - 20
	entries := make([]indexEntry, 0, n)
	prev := ""
	for range n {
		start := pos
		if pos+62 > end {
			return nil, false
		}
		mode := binary.BigEndian.Uint32(raw[pos+24:])
		id := hex.EncodeToString(raw[pos+40 : pos+60])
		flags := binary.BigEndian.Uint16(raw[pos+60:])
		pos += 62
		extended := flags&0x4000 != 0
		if extended {
			if version < 3 || pos+2 > end {
				return nil, false
			}
			pos += 2
		}
		var path string
		if version == 4 {
			strip, k := uvarint(raw[pos:end])
			if k <= 0 || int(strip) > len(prev) {
				return nil, false
			}
			pos += k
			nul := bytes.IndexByte(raw[pos:end], 0)
			if nul < 0 {
				return nil, false
			}
			path = prev[:len(prev)-int(strip)] + string(raw[pos:pos+nul])
			pos += nul + 1
		} else {
			nul := bytes.IndexByte(raw[pos:end], 0)
			if nul < 0 {
				return nil, false
			}
			path = string(raw[pos : pos+nul])
			pos += nul + 1
			// entries are padded with NULs to a multiple of 8 bytes
			for (pos-start)%8 != 0 {
				pos++
			}
		}
		if mode == 0o40000 {
			return nil, false // a sparse-index directory entry
		}
		entries = append(entries, indexEntry{mode: mode, id: id, stage: int(flags>>12) & 3, path: path, extended: extended})
		prev = path
	}
	// extensions: refuse a split index ("link") and sparse ("sdir")
	for pos+8 <= end {
		sig := string(raw[pos : pos+4])
		size := int(binary.BigEndian.Uint32(raw[pos+4:]))
		if sig == "link" || sig == "sdir" {
			return nil, false
		}
		pos += 8 + size
	}
	return entries, true
}

// uvarint is git's index v4 offset varint (big-endian base-128 with +1).
func uvarint(b []byte) (uint64, int) {
	if len(b) == 0 {
		return 0, 0
	}
	c := b[0]
	v := uint64(c & 0x7f)
	i := 1
	for c&0x80 != 0 {
		if i >= len(b) {
			return 0, 0
		}
		v++
		c = b[i]
		i++
		v = (v << 7) + uint64(c&0x7f)
	}
	return v, i
}

// lsFilesStage answers `ls-files --stage -z [--] [paths]` (paths relative
// to dir; "." = everything under dir).
func lsFilesStage(r *repo, inv invocation) (Reply, bool) {
	stage, z := false, false
	var paths []string
	dashdash := false
	for _, a := range inv.args {
		switch {
		case dashdash:
			paths = append(paths, a)
		case a == "--":
			dashdash = true
		case a == "--stage" || a == "-s":
			stage = true
		case a == "-z":
			z = true
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		default:
			paths = append(paths, a)
		}
	}
	if !stage || !z {
		return Reply{}, false
	}
	prefix, ok := r.prefixOf(inv.dir)
	if !ok {
		return Reply{}, false
	}
	var full pathspec
	for _, p := range paths {
		if p == "." {
			p = ""
		} else {
			ps, ok := parsePathspec([]string{p}, inv.literal)
			if !ok {
				return Reply{}, false
			}
			p = ps[0]
		}
		full = append(full, strings.TrimSuffix(prefix+p, "/"))
	}
	entries, ok := r.readIndex()
	if !ok {
		return Reply{}, false
	}
	var out bytes.Buffer
	for _, e := range entries {
		if prefix != "" && !strings.HasPrefix(e.path, prefix) {
			continue
		}
		if len(full) > 0 && !matchAny(full, e.path) {
			continue
		}
		out.WriteString(strconv.FormatUint(uint64(e.mode), 8) + " " + e.id + " " + strconv.Itoa(e.stage) + "\t" + e.path[len(prefix):])
		out.WriteByte(0)
	}
	return Reply{Out: out.Bytes()}, true
}

// matchAny reports whether path is one of the prefixes or under one ("" =
// everything).
func matchAny(prefixes []string, path string) bool {
	for _, p := range prefixes {
		if p == "" || path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// indexLookup resolves ":path" / ":0:path" (stage 0, root-relative).
func (r *repo) indexLookup(expr string) resolved {
	path := strings.TrimPrefix(expr, ":")
	if p, ok := strings.CutPrefix(path, "0:"); ok {
		path = p
	} else if len(path) >= 2 && path[1] == ':' {
		return notOK // another stage: git
	}
	if path == "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
		return notOK
	}
	entries, ok := r.readIndex()
	if !ok {
		return notOK
	}
	for _, e := range entries {
		if e.path == path {
			if e.stage != 0 {
				return notOK // a conflict: git words it
			}
			return resolved{id: e.id, found: true, ok: true, treePath: true}
		}
	}
	return resolved{ok: true, treePath: true}
}

// lsFilesPlain answers `ls-files [-z] [--full-name] [--error-unmatch] [--]
// [paths]`: the index's paths under dir (relative to dir unless
// --full-name). An unmerged entry or a name git would quote is left to
// git. A pathspec that matches nothing under --error-unmatch is answered
// as git answers it (exit 1, git's words on stderr, unmatchedReply): "is
// this file tracked?" about a file that is not was the one form of the
// question that still started git, once per render of a project whose
// _meta.json is not committed yet (devco:D-945, bug hunt H-203).
func lsFilesPlain(r *repo, inv invocation) (Reply, bool) {
	z, fullName, errorUnmatch := false, false, false
	var paths []string
	dashdash := false
	for _, a := range inv.args {
		switch {
		case dashdash:
			paths = append(paths, a)
		case a == "--":
			dashdash = true
		case a == "-z":
			z = true
		case a == "--full-name":
			fullName = true
		case a == "--error-unmatch":
			errorUnmatch = true
		case a == "--cached" || a == "-c":
		case strings.HasPrefix(a, "-"):
			return Reply{}, false
		default:
			paths = append(paths, a)
		}
	}
	prefix, ok := r.prefixOf(inv.dir)
	if !ok {
		return Reply{}, false
	}
	var full []string
	for _, p := range paths {
		if p == "." {
			full = append(full, strings.TrimSuffix(prefix, "/"))
			continue
		}
		ps, ok := parsePathspec([]string{p}, inv.literal)
		if !ok {
			return Reply{}, false
		}
		full = append(full, strings.TrimSuffix(prefix+ps[0], "/"))
	}
	entries, ok := r.readIndex()
	if !ok {
		return Reply{}, false
	}
	matched := make([]bool, len(full))
	var out bytes.Buffer
	for _, e := range entries {
		if prefix != "" && !strings.HasPrefix(e.path, prefix) {
			continue
		}
		if len(full) > 0 {
			hit := false
			for i, p := range full {
				if p == "" || e.path == p || strings.HasPrefix(e.path, p+"/") {
					matched[i], hit = true, true
				}
			}
			if !hit {
				continue
			}
		}
		if e.stage != 0 {
			return Reply{}, false // unmerged entries: git prints them its way
		}
		name := e.path
		if !fullName {
			name = e.path[len(prefix):]
		}
		if !z && needsQuote(name) {
			return Reply{}, false
		}
		out.WriteString(name)
		if z {
			out.WriteByte(0)
		} else {
			out.WriteByte('\n')
		}
	}
	if errorUnmatch {
		var missed []string
		for i, m := range matched {
			if !m {
				missed = append(missed, paths[i])
			}
		}
		if len(missed) > 0 {
			return unmatchedReply(out.Bytes(), missed)
		}
	}
	return Reply{Out: out.Bytes()}, true
}

// unmatchedReply is ls-files --error-unmatch's answer when the pathspecs
// missed match no index entry: what matched on stdout, exit 1, and one
// "did not match" line per miss on stderr, as git prints them (git
// matches a pathspec by case even under core.ignorecase). A miss that is
// a directory form ("dir/") or "." is left to git: lsFilesPlain matches
// those more loosely than git does.
func unmatchedReply(out []byte, missed []string) (Reply, bool) {
	for _, p := range missed {
		if p == "." || strings.HasSuffix(p, "/") {
			return Reply{}, false
		}
	}
	var stderr strings.Builder // a Builder never fails a write
	for _, p := range missed {
		stderr.WriteString("error: pathspec '" + p + "' did not match any file(s) known to git\n")
	}
	stderr.WriteString("Did you forget to 'git add'?")
	return Reply{Out: out, Code: 1, Stderr: stderr.String()}, true
}
