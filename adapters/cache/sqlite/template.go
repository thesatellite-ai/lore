package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/tmpdir"
	"github.com/thesatellite-ai/lore/schema"
)

// Empty-cache templates: a test-process speed-up, never used by lore itself.
//
// Creating a cache file runs the DDL of every typed table (planFor →
// syncTables → createAndFill), which is most of what opening a fresh test
// harness costs (about 63 % of one ops harness, and ~8x more under -race
// because the SQLite engine is translated Go). A test process opens
// thousands of brand-new, empty caches over the same registry, so it may
// call UseEmptyTemplates once: from then on, Open of a file that does not
// exist yet first writes a copy of an empty cache built once per registry
// in this process, then opens it as usual.
//
// Why this is the same as a fresh create: the copy is the byte image of a
// file this package created with the same code, registry and layoutVersion
// and then closed cleanly (WAL checkpointed into the main file), and Open
// still runs init on it, which re-checks the layout version and every
// table's fingerprint against the registry. So even a template built for
// another registry could only cost time, never leave a wrong table
// (TestTemplateMatchesFreshCreate pins the schema equality).
//
// State: the templates live in memory only (no file outlives the build),
// and are off unless a test process turns them on; the lore binary never
// calls UseEmptyTemplates.

// templatesOn is set by UseEmptyTemplates; Open reads it on every call.
var templatesOn atomic.Bool

// templates maps a templateKey to its *templateEntry.
var templates sync.Map

// templateEntry is one registry's empty cache image, built once.
type templateEntry struct {
	once  sync.Once
	image []byte
	err   error
}

// templateFileMode is the mode a copied cache file is created with: the
// mode SQLite itself creates a database file with (before the umask).
const templateFileMode fs.FileMode = 0o644

// UseEmptyTemplates turns empty-cache templates on for the rest of this
// process. It is meant for test harnesses only (see the comment above);
// calling it twice is harmless.
func UseEmptyTemplates() { templatesOn.Store(true) }

// templateKey names the empty cache a registry yields: its tables'
// fingerprints (which change with any column, index or FTS change) and the
// layout version.
func templateKey(reg *schema.Registry) (string, error) {
	h := sha256.New()
	h.Write([]byte(strconv.Itoa(layoutVersion)))
	for _, def := range reg.Tables() {
		p, err := planFor(def, "")
		if err != nil {
			return "", err
		}
		h.Write([]byte{0})
		h.Write([]byte(def.Name.String()))
		h.Write([]byte{0})
		h.Write([]byte(p.fingerprint))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// seedFromTemplate writes the empty template of reg to path when templates
// are on and path does not exist yet; otherwise it does nothing. A file
// that appears meanwhile (another opener) is left alone.
//
// It never fails the open: a template is only a shortcut, so when the copy
// cannot be made (an unwritable folder, a full disk) the error is dropped
// and Open creates the file itself, answering exactly as it would have
// without templates (E_DISK_FULL, a permission error, ...).
func seedFromTemplate(ctx context.Context, path string, reg *schema.Registry, exp Expander) {
	if !templatesOn.Load() {
		return
	}
	_ = copyTemplate(ctx, path, reg, exp) // fallback: the normal create path
}

// copyTemplate is seedFromTemplate without the on switch (tests call it
// directly so they never turn templates on for the whole test process).
func copyTemplate(ctx context.Context, path string, reg *schema.Registry, exp Expander) error {
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil // exists (or cannot be checked): open it as it is
	}
	key, err := templateKey(reg)
	if err != nil {
		return err
	}
	v, _ := templates.LoadOrStore(key, &templateEntry{})
	e, ok := v.(*templateEntry)
	if !ok {
		return errs.New(errs.Internal, "sqlite cache: template map holds a foreign value")
	}
	e.once.Do(func() { e.image, e.err = buildTemplate(ctx, reg, exp, filepath.Dir(path)) })
	if e.err != nil {
		return e.err
	}
	return writeNew(path, e.image)
}

// buildTemplate creates an empty cache over reg in a temp folder, closes
// it with its WAL folded into the main file, and returns the file's bytes.
// The folder is removed before returning. near is the folder the cache
// goes to: the temp folder falls back to it when TMPDIR cannot be used.
func buildTemplate(ctx context.Context, reg *schema.Registry, exp Expander, near string) (img []byte, err error) {
	dir, err := tmpdir.Dir("lore-cache-template-", near)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "sqlite cache: template folder", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	path := filepath.Join(dir, "empty.db")
	c, err := openFile(ctx, path, reg, exp, FreeBytes)
	if err != nil {
		return nil, err
	}
	if _, err := c.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return nil, errors.Join(dbErr("template checkpoint", err), c.Close())
	}
	if err := c.Close(); err != nil {
		return nil, err
	}
	img, err = os.ReadFile(path)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "sqlite cache: read template", err)
	}
	return img, nil
}

// tmplSeq makes the temp names of concurrent template copies unique
// within this process (the pid makes them unique across processes).
var tmplSeq atomic.Uint64

// writeNew creates path with img, never replacing a file: the image goes
// to a temp file in the same folder and is hard-linked into place, so a
// concurrent opener sees either no file or the whole image. The temp file
// is created with templateFileMode under the process umask, as SQLite
// creates a new database file.
func writeNew(path string, img []byte) (err error) {
	name := path + ".tmpl-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatUint(tmplSeq.Add(1), 10)
	tmp, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, templateFileMode)
	if err != nil {
		return errs.Wrap(errs.Internal, "sqlite cache: write template copy", err)
	}
	defer func() { err = errors.Join(err, os.Remove(name)) }()
	if _, err := tmp.Write(img); err != nil {
		return errors.Join(errs.Wrap(errs.Internal, "sqlite cache: write template copy", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, "sqlite cache: write template copy", err)
	}
	if err := os.Link(name, path); err != nil && !errors.Is(err, fs.ErrExist) {
		return errs.Wrap(errs.Internal, "sqlite cache: place template copy", err)
	}
	return nil
}
