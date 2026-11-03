// Package lorecfg is the one place lore reads and writes its own config
// files (~/.lore/identity.yaml, .lore/lore.yaml, ...).
//
// YAML is the format. TOML was the format before it, so every config is
// addressed by its BASE name ("identity", "lore") and this package picks the
// file:
//
//   - base.yaml only            → read it.
//   - base.toml only            → migrate it to base.yaml (atomic write, same
//     permissions), rename the original to base.toml.migrated, print one line
//     to stderr, then read the YAML. If the migration cannot be written (a
//     read-only directory, a full disk) the command still works: the TOML is
//     read as before and one warning says why it was not migrated.
//   - both                      → YAML wins; one warning says the TOML is
//     ignored.
//   - neither                   → Located.Exists is false; the caller decides
//     what absence means.
//
// Loading goes through github.com/ubgo/cfgkit so every config type gets the
// same pipeline (defaults → source → Derive → Validate) and joined errors.
// cfgkit's YAML adapter module requires a newer Go toolchain than this
// workspace pins, so the YAML parse is wired with cfgkit.StructuredFunc and
// gopkg.in/yaml.v3 — the "any format in five lines" seam cfgkit documents.
package lorecfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/ubgo/cfgkit"
	"gopkg.in/yaml.v3"
)

// File-name pieces. Named so every reader, writer and existence check spells
// them the same way.
const (
	// ExtYAML is the current config extension. ".yaml" rather than ".yml"
	// because it is the extension the YAML spec recommends and the one lore's
	// docs use.
	ExtYAML = ".yaml"
	// ExtTOML is the legacy extension, read only for backward compatibility
	// and migration; nothing writes it any more.
	ExtTOML = ".toml"
	// MigratedSuffix is appended to a TOML file after its YAML twin is
	// written (identity.toml → identity.toml.migrated). The original is kept,
	// never deleted, so a migration bug can always be undone by hand; the
	// suffix takes it out of the lookup so it is not migrated twice.
	MigratedSuffix = ".migrated"
	// backupStampLayout names a second backup when base.toml.migrated already
	// exists (a TOML re-created after an earlier migration), so an older
	// backup is never overwritten.
	backupStampLayout = "20060102T150405"
	// tmpPattern is the os.CreateTemp pattern for in-flight atomic writes; the
	// temp file lives in the target directory so the final rename never
	// crosses a filesystem.
	tmpPattern = ".lorecfg-*.tmp"
	// msgPrefix starts every notice so it reads as lore's, not the command's.
	msgPrefix = "lore: "
)

// Format is the on-disk format a Located file is in.
type Format int

const (
	// FormatYAML is the current format.
	FormatYAML Format = iota + 1
	// FormatTOML is the legacy format, returned only when a migration could
	// not be written and the old file must be read in place.
	FormatTOML
)

// Located is the result of Locate: which file to read, in which format.
type Located struct {
	Path   string // the file to read; the YAML path when Exists is false
	Format Format
	Exists bool
}

// RefResolver is the seam for loading config values from a secret store
// (1Password "op://vault/item/field", gopass "gopass://path", ...). cfgkit
// offers secret stores only as flat key/value Sources, not as references
// inside a value, so the reference step lives here, in one place: after a
// config is loaded, every field its type lists via RefFields is offered to
// the resolver. No resolver is registered today; adding one needs no change
// to any config file format.
type RefResolver interface {
	// Resolve returns the value ref points at. handled=false means the
	// resolver does not own ref's scheme and the literal value is kept.
	Resolve(ctx context.Context, ref string) (value string, handled bool, err error)
}

// RefFields is implemented by config types whose string values may be
// secret-store references. It returns pointers into the receiver.
type RefFields interface {
	RefFields() []*string
}

// Options tunes Locate and Load. The zero value is the production default:
// notices go to os.Stderr and no reference resolver is applied.
type Options struct {
	// Notices receives the one-line migration / ignored-file messages.
	// nil means os.Stderr.
	Notices io.Writer
	// Resolver resolves secret-store references in RefFields; nil keeps
	// every value literal.
	Resolver RefResolver
}

func (o Options) notices() io.Writer {
	if o.Notices == nil {
		return os.Stderr
	}
	return o.Notices
}

// noticed dedupes notices per process: identity resolution runs several times
// in one command (actor stamping, doctor, ...), and the owner asked for each
// notice ONCE. It is process memory only — nothing is persisted, so a second
// command prints the "both files exist" warning again until it is fixed.
var noticed sync.Map

func notice(o Options, key, msg string) {
	if _, dup := noticed.LoadOrStore(key, struct{}{}); dup {
		return
	}
	_, _ = fmt.Fprintln(o.notices(), msgPrefix+msg) // a notice is advisory: a failed write to the notice stream is not actionable
}

// NoticeSkipped prints, once per process, that the config file at path could
// not be read and is skipped until it is fixed. Callers that fall through to
// another source on a Load error use it so the fall-through is never silent.
// A *ParseError is shortened to its position because Locate already printed
// the full message for a malformed legacy TOML.
func NoticeSkipped(o Options, path string, err error) {
	reason := err.Error()
	var pe *ParseError
	if errors.As(err, &pe) && pe.Line > 0 {
		reason = fmt.Sprintf("invalid at line %d", pe.Line)
	}
	notice(o, "skip:"+path, fmt.Sprintf("%s is skipped until it is fixed (%s)", path, reason))
}

// Locate finds the config file for base inside dir, migrating a legacy TOML
// file to YAML when needed (see the package comment for every case).
// It returns an error only when dir itself cannot be inspected; a failed
// migration degrades to reading the TOML.
func Locate(ctx context.Context, dir, base string, o Options) (Located, error) {
	yamlPath := filepath.Join(dir, base+ExtYAML)
	tomlPath := filepath.Join(dir, base+ExtTOML)

	yamlExists, err := exists(yamlPath)
	if err != nil {
		return Located{}, fmt.Errorf("lorecfg: stat %s: %w", yamlPath, err)
	}
	tomlExists, err := exists(tomlPath)
	if err != nil {
		return Located{}, fmt.Errorf("lorecfg: stat %s: %w", tomlPath, err)
	}

	switch {
	case yamlExists && tomlExists:
		notice(o, "both:"+tomlPath, fmt.Sprintf("%s is ignored because %s exists (delete or rename the .toml file)",
			tomlPath, filepath.Base(yamlPath)))
		return Located{Path: yamlPath, Format: FormatYAML, Exists: true}, nil
	case yamlExists:
		return Located{Path: yamlPath, Format: FormatYAML, Exists: true}, nil
	case !tomlExists:
		return Located{Path: yamlPath, Format: FormatYAML}, nil
	}

	backup, err := migrate(ctx, tomlPath, yamlPath)
	var pe *ParseError
	switch {
	case errors.As(err, &pe):
		// Malformed: reading it as TOML fails the same way, so do not promise
		// a read. Load on the returned Located returns the same line-numbered
		// error; the caller decides whether that fails the command or (like
		// the identity fallback chain) skips the file with NoticeSkipped.
		notice(o, "fail:"+tomlPath, fmt.Sprintf("%v; not migrated to YAML", pe))
		return Located{Path: tomlPath, Format: FormatTOML, Exists: true}, nil
	case err != nil:
		notice(o, "fail:"+tomlPath, fmt.Sprintf("could not migrate %s to YAML, reading it as TOML: %v", tomlPath, err))
		return Located{Path: tomlPath, Format: FormatTOML, Exists: true}, nil
	}
	notice(o, "done:"+tomlPath, fmt.Sprintf("migrated %s → %s (old file kept as %s)",
		tomlPath, filepath.Base(yamlPath), filepath.Base(backup)))
	return Located{Path: yamlPath, Format: FormatYAML, Exists: true}, nil
}

// Load reads loc into a new T through cfgkit, so T's Defaults / Derive /
// Validate hooks run, then applies o.Resolver to T's RefFields.
// Unknown keys are tolerated, as the TOML reader always did.
func Load[T any](ctx context.Context, loc Located, o Options) (*T, error) {
	data, err := os.ReadFile(loc.Path)
	if err != nil {
		return nil, fmt.Errorf("lorecfg: read %s: %w", loc.Path, err)
	}
	src := cfgkit.StructuredFunc(loc.Path, func(dst any) error {
		if loc.Format == FormatTOML {
			return parseTOML(loc.Path, data, dst)
		}
		return yaml.Unmarshal(data, dst)
	})
	cfg, _, err := cfgkit.Load[T](cfgkit.WithSources(src))
	if err != nil {
		// A structured source (the whole file) fails as a unit, with no key,
		// and cfgkit then renders "source <path> failed for : <cause>". The
		// path is already in this message, so report the parse cause alone.
		var se *cfgkit.SourceError
		if errors.As(err, &se) && se.Key == "" && se.Source == loc.Path {
			return nil, fmt.Errorf("lorecfg: load %s: %w", loc.Path, se.Err)
		}
		return nil, fmt.Errorf("lorecfg: load %s: %w", loc.Path, err)
	}
	if err := resolveRefs(ctx, cfg, o.Resolver); err != nil {
		return nil, fmt.Errorf("lorecfg: resolve references in %s: %w", loc.Path, err)
	}
	return cfg, nil
}

// resolveRefs offers each RefFields value of cfg to r. Kept separate from
// Load so the seam is one function a provider change has to touch.
func resolveRefs[T any](ctx context.Context, cfg *T, r RefResolver) error {
	if r == nil {
		return nil
	}
	rf, ok := any(cfg).(RefFields)
	if !ok {
		return nil
	}
	for _, p := range rf.RefFields() {
		if *p == "" {
			continue
		}
		v, handled, err := r.Resolve(ctx, *p)
		if err != nil {
			return err
		}
		if handled {
			*p = v
		}
	}
	return nil
}

// Write stores v as YAML at dir/base.yaml with mode perm, atomically. A
// legacy base.toml is first migrated out of the way (Locate), so a writer
// never leaves a TOML file behind that would later be reported as ignored.
func Write[T any](ctx context.Context, dir, base string, v *T, perm fs.FileMode, o Options) (string, error) {
	if _, err := Locate(ctx, dir, base, o); err != nil {
		return "", err
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("lorecfg: encode %s: %w", base, err)
	}
	path := filepath.Join(dir, base+ExtYAML)
	if err := writeAtomic(path, data, perm); err != nil {
		return "", err
	}
	return path, nil
}

// Remove deletes both the YAML and any legacy TOML for base, so an unset
// cannot be undone by a leftover TOML being migrated back on the next read.
// Backups (*.migrated) are left alone. Reports whether anything was removed.
func Remove(dir, base string) (bool, error) {
	removed := false
	for _, ext := range []string{ExtYAML, ExtTOML} {
		p := filepath.Join(dir, base+ext)
		err := os.Remove(p)
		switch {
		case err == nil:
			removed = true
		case errors.Is(err, fs.ErrNotExist):
		default:
			return removed, fmt.Errorf("lorecfg: remove %s: %w", p, err)
		}
	}
	return removed, nil
}

// migrate writes the YAML equivalent of tomlPath to yamlPath and renames the
// TOML to its backup name. Every key is carried over, including keys the
// current lore version does not read, because the TOML is decoded generically
// rather than into lore's struct. Returns the backup path.
func migrate(_ context.Context, tomlPath, yamlPath string) (string, error) {
	info, err := os.Stat(tomlPath)
	if err != nil {
		return "", fmt.Errorf("stat: %w", err)
	}
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	// A generic tree is the only shape that keeps unknown keys; this is the
	// format boundary, and the tree goes straight to the YAML encoder.
	var tree map[string]any
	if err := parseTOML(tomlPath, raw, &tree); err != nil {
		return "", err
	}
	out, err := encodeYAML(tree)
	if err != nil {
		return "", err
	}
	if err := writeAtomic(yamlPath, out, info.Mode().Perm()); err != nil {
		return "", err
	}
	backup, err := backupName(tomlPath)
	if err != nil {
		_ = os.Remove(yamlPath) // undo the half-done migration; the error being returned is the one to report
		return "", err
	}
	if err := os.Rename(tomlPath, backup); err != nil {
		// Without the rename both files exist and the warning would fire on
		// every run; undo the YAML so the next run retries cleanly.
		_ = os.Remove(yamlPath) // undo the half-done migration; the error being returned is the one to report
		return "", fmt.Errorf("keep backup: %w", err)
	}
	return backup, nil
}

// encodeYAML marshals a TOML-decoded tree. TOML local date/time values decode
// to go-toml structs that YAML would write as mappings, so they are written
// as their TOML text instead; the value is preserved, not reshaped.
func encodeYAML(tree map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(normalizeTOML(tree)); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	return buf.Bytes(), nil
}

func normalizeTOML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeTOML(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = normalizeTOML(e)
		}
		return t
	case toml.LocalDate:
		return t.String()
	case toml.LocalTime:
		return t.String()
	case toml.LocalDateTime:
		return t.String()
	default:
		return v
	}
}

// backupName returns tomlPath+MigratedSuffix, or a time-stamped variant when
// that already exists, so no earlier backup is ever overwritten.
func backupName(tomlPath string) (string, error) {
	b := tomlPath + MigratedSuffix
	ok, err := exists(b)
	if err != nil {
		return "", fmt.Errorf("stat backup: %w", err)
	}
	if !ok {
		return b, nil
	}
	return b + "." + time.Now().UTC().Format(backupStampLayout), nil
}

// writeAtomic writes data to path via a temp file in the same directory and a
// rename, so a reader sees the old file or the new one, never half of one.
func writeAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp) // a leftover temp file is harmless; the write error is the one to report
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close() // the write error is the one to report
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err = f.Chmod(perm); err != nil {
		_ = f.Close() // the write error is the one to report
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close() // the write error is the one to report
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename into %s: %w", path, err)
	}
	return nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}
