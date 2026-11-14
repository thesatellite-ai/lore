package rowfs

// badfile.go — a row file that is not a usable row says what is wrong with
// it and what to do next, on every surface that meets it (the read notice,
// show, doctor): permission denied (chmod), an unresolved git merge
// (conflict markers), invalid JSON at line:col, or an id that is not the
// one its name says (a duplicate when another file holds it). Before this
// a chmod 000 file made `show` fail E_INTERNAL "internal error", a merge
// left with markers read as plain "invalid JSON", and no error named the
// file or a way out.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// FileCause is why a row file is not a usable row; the next step follows
// from it. "" is any other reason (a stray entry, a file over the cap).
type FileCause string

// The causes a row file problem can carry (Problem.Cause, CauseOf).
const (
	// CauseUnreadable: the file exists but cannot be read (permission denied).
	CauseUnreadable FileCause = "unreadable"
	// CauseConflict: the file holds the markers of an unresolved git merge.
	CauseConflict FileCause = "conflict"
	// CauseInvalidJSON: the file is not one JSON object (empty, cut short, hand-broken).
	CauseInvalidJSON FileCause = "invalid_json"
	// CauseWrongID: the file holds a row whose table or id differ from its name.
	CauseWrongID FileCause = "wrong_id"
)

// causeError tags an *errs.Error with its FileCause; the text, code and
// hint are the wrapped error's (errors.As finds the *errs.Error).
type causeError struct {
	cause FileCause
	err   *errs.Error
	// holds is the path (under the data dir) of the row a CauseWrongID
	// file holds, so a Folder can say when that file exists too.
	holds string
}

// Error is the wrapped error's code and message without its cause: the
// message already says the cause in the person's words, the raw decoder
// or OS text after it only repeated it ("…: canon: invalid JSON …").
func (e causeError) Error() string { return "[" + string(e.err.Code) + "] " + e.err.Message }

// Unwrap exposes the *errs.Error (errs.CodeOf, errors.As).
func (e causeError) Unwrap() error { return e.err }

// CauseOf is the FileCause in err's chain, "" when it carries none.
func CauseOf(err error) FileCause {
	var c causeError
	if errors.As(err, &c) {
		return c.cause
	}
	return ""
}

// HintOf is the next step an error of a row file carries ("" when none).
func HintOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

// unreadable is the error of a row file the OS refuses to read; full is
// its absolute path, rel its path under the data dir.
func unreadable(full, rel string, err error) error {
	why := "the file cannot be read"
	if errors.Is(err, fs.ErrPermission) {
		why = "permission denied"
	} else {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			why = pe.Err.Error()
		}
	}
	return causeError{cause: CauseUnreadable, err: errs.Wrap(errs.BadPath, "rowfs: cannot read "+quote(rel)+": "+why, err)}
}

// undecodable classifies a document that did not decode as a row (cause
// is row.Decode's error): conflict markers first (a merge left them, the
// JSON error is only their symptom), then bytes that are no JSON, with
// their line:col. Any other refusal (a row from a newer lore, E_OLD_LORE;
// a JSON object that is no row) keeps its own code and words.
func undecodable(rel string, doc []byte, cause error) error {
	if line := markerLine(doc); line > 0 {
		return causeError{cause: CauseConflict, err: errs.Wrap(errs.InvalidInput, "rowfs: "+quote(rel)+" holds an unresolved git merge conflict (conflict markers from line "+strconv.Itoa(line)+")", cause)}
	}
	if errs.CodeOf(cause) == errs.InvalidInput && !json.Valid(doc) {
		return causeError{cause: CauseInvalidJSON, err: errs.Wrap(errs.InvalidInput, "rowfs: "+quote(rel)+" is not valid JSON: "+jsonProblem(doc), cause)}
	}
	return cause
}

// markerLine is the 1-based line of the first git conflict marker line
// ("<<<<<<< ", "=======", ">>>>>>> ") in doc, 0 when there is none.
func markerLine(doc []byte) int {
	if !bytes.Contains(doc, markerStart) {
		return 0
	}
	for i, l := range strings.Split(string(doc), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "<<<<<<< ") || l == "<<<<<<<" || l == "=======" || strings.HasPrefix(l, ">>>>>>> ") {
			return i + 1
		}
	}
	return 0
}

// jsonProblem says where and why doc is not one JSON object: "the file is
// empty", "it ends early at line 2:5", "invalid character 'x' at line 3:1".
func jsonProblem(doc []byte) string {
	if len(bytes.TrimSpace(doc)) == 0 {
		return "the file is empty"
	}
	var v json.RawMessage
	err := json.Unmarshal(doc, &v)
	var se *json.SyntaxError
	switch {
	case errors.As(err, &se) && se.Offset >= int64(len(doc)):
		return "it ends early at line " + lineCol(doc, len(doc))
	case errors.As(err, &se):
		return se.Error() + " at line " + lineCol(doc, int(se.Offset))
	case err != nil:
		return err.Error()
	}
	return "it is not one JSON object"
}

// lineCol is "line:col" (1-based) of the byte just before offset off in
// doc: json.SyntaxError.Offset counts the bytes read, the bad one included.
func lineCol(doc []byte, off int) string {
	off = max(min(off, len(doc)), 1)
	before := doc[:off-1]
	line := bytes.Count(before, []byte("\n")) + 1
	col := off - bytes.LastIndexByte(before, '\n') - 1
	return strconv.Itoa(line) + ":" + strconv.Itoa(col)
}

// wrongID is the error of a file whose row is not the one its name says;
// holds is the path (under the data dir) that row's file would have.
func wrongID(rel, holds string) error {
	return causeError{cause: CauseWrongID, holds: holds, err: errs.New(errs.InvalidInput, "rowfs: "+quote(rel)+" holds the row "+quote(strings.TrimSuffix(holds, ".json"))+", not the one its file name says")}
}

// explain adds the next step to err, an error of the row file rel (under
// f's data dir): what to run, with the file's path as git sees it from the
// store's top. An error with a hint already, or without a FileCause, is
// returned as it is.
func (f *Folder) explain(rel string, err error) error {
	var c causeError
	if !errors.As(err, &c) || c.err.Hint != "" {
		return err
	}
	if c.cause == CauseWrongID && c.holds != "" && f.sibling(c.holds) {
		c.err = errs.New(c.err.Code, c.err.Message+"; "+quote(c.holds)+" holds that id too (a duplicate id)")
	}
	gitPath := f.storePath(rel)
	restore := "git -C " + f.root.dir + " checkout -- " + gitPath
	switch c.cause {
	case CauseUnreadable:
		c.err = c.err.WithHint("make it readable again: chmod u+rw " + filepath.Join(f.dataDir, filepath.FromSlash(rel)))
	case CauseConflict:
		c.err = c.err.WithHint("lore sync conflicts lists it; keep one side of each conflict and delete the marker lines (during the merge: git -C " + f.root.dir + " checkout --ours -- " + gitPath + ", or --theirs), then git add it")
	case CauseInvalidJSON:
		c.err = c.err.WithHint("a row file is one JSON object; fix it, or put it back: " + restore)
	case CauseWrongID:
		c.err = c.err.WithHint("put the id back as the file name says, or put the file back: " + restore)
	}
	return c
}

// sibling reports whether the row file rel (under the data dir) exists.
func (f *Folder) sibling(rel string) bool {
	_, err := os.Lstat(filepath.Join(f.dataDir, filepath.FromSlash(rel)))
	return err == nil
}
