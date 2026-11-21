package gitfast

// objects.go — reads git objects from a repository's object store without
// a git process: loose objects (zlib) and packs (idx v2, OFS/REF deltas).
// Objects are immutable by id, so every object read once is kept for the
// life of the Session.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Object types as git names them in cat-file and ls-tree output.
const (
	typeCommit = "commit"
	typeTree   = "tree"
	typeBlob   = "blob"
	typeTag    = "tag"
)

// errMissing means the object is not in this store (git would say
// "missing"); errUnsupported means it may be there but this reader cannot
// tell (an unknown pack version): the caller falls back to git.
var (
	errMissing     = errors.New("gitfast: object missing")
	errUnsupported = errors.New("gitfast: unsupported object layout")
)

// object is one decoded git object.
type object struct {
	typ  string
	data []byte
}

// objectStore reads objects from one objects/ folder and its alternates.
type objectStore struct {
	dirs []string // objects/ folder first, then alternates

	mu    sync.Mutex
	packs []*pack // loaded lazily, reloaded when a lookup misses
	seen  map[string]bool
	cache map[string]object
	// bytes is the content bytes held in cache (a kept store is dropped
	// past objPoolMaxBytes, objpool.go)
	bytes int
	// trees indexes parsed trees by id (treeIndex); nil for one that
	// does not parse
	trees map[string]map[string]treeEntry
}

// newObjectStore opens the object folder dir (and objects/info/alternates).
func newObjectStore(dir string) (*objectStore, error) {
	s := &objectStore{dirs: []string{dir}, seen: map[string]bool{}, cache: map[string]object{}, trees: map[string]map[string]treeEntry{}}
	raw, err := os.ReadFile(filepath.Join(dir, "info", "alternates"))
	if err == nil {
		for line := range strings.SplitSeq(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "\"") {
				return nil, errUnsupported // quoted alternate paths: leave to git
			}
			if !filepath.IsAbs(line) {
				line = filepath.Join(dir, line)
			}
			s.dirs = append(s.dirs, filepath.Clean(line))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

// read returns the object id (40 lowercase hex).
func (s *objectStore) read(id string) (object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(id, 0)
}

// maxDeltaDepth bounds delta chains (git's own default limit is 50; packs
// written with --depth larger are rare, and a cycle must not hang).
const maxDeltaDepth = 10000

// readLocked is read with s.mu held.
func (s *objectStore) readLocked(id string, depth int) (object, error) {
	if o, ok := s.cache[id]; ok {
		return o, nil
	}
	if depth > maxDeltaDepth {
		return object{}, errUnsupported
	}
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != 20 {
		return object{}, errUnsupported
	}
	o, err := s.readLoose(id)
	if errors.Is(err, errMissing) {
		o, err = s.readPacked(raw, depth)
		if errors.Is(err, errMissing) {
			// a pack may have been written since the list was read
			if rerr := s.loadPacks(); rerr != nil {
				return object{}, rerr
			}
			o, err = s.readPacked(raw, depth)
		}
	}
	if err != nil {
		return object{}, err
	}
	s.cache[id] = o
	s.bytes += len(o.data)
	return o, nil
}

// readLoose reads objects/xx/yyyy… in every object folder.
func (s *objectStore) readLoose(id string) (object, error) {
	for _, d := range s.dirs {
		f, err := os.Open(filepath.Join(d, id[:2], id[2:]))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return object{}, err
		}
		o, derr := decodeLoose(f)
		cerr := f.Close()
		if derr != nil {
			return object{}, derr
		}
		if cerr != nil {
			return object{}, cerr
		}
		return o, nil
	}
	return object{}, errMissing
}

// decodeLoose inflates a loose object: "<type> <size>\0<content>".
func decodeLoose(r io.Reader) (object, error) {
	zr, err := zlib.NewReader(r)
	if err != nil {
		return object{}, errUnsupported
	}
	all, err := io.ReadAll(zr)
	if err != nil {
		return object{}, errUnsupported
	}
	head, body, ok := bytes.Cut(all, []byte{0})
	if !ok {
		return object{}, errUnsupported
	}
	typ, size, ok := strings.Cut(string(head), " ")
	n, nerr := strconv.Atoi(size)
	if !ok || nerr != nil || n != len(body) || !knownType(typ) {
		return object{}, errUnsupported
	}
	return object{typ: typ, data: body}, nil
}

// knownType reports whether t is one of the four object types.
func knownType(t string) bool {
	return t == typeCommit || t == typeTree || t == typeBlob || t == typeTag
}

// pack is one .pack with its .idx (version 2) loaded in memory.
type pack struct {
	path    string
	fanout  [256]uint32
	ids     []byte // n*20
	offsets []uint32
	large   []uint64
	f       *os.File
}

// loadPacks reads every pack index not yet loaded.
func (s *objectStore) loadPacks() error {
	for _, d := range s.dirs {
		entries, err := os.ReadDir(filepath.Join(d, "pack"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".idx") {
				continue
			}
			p := filepath.Join(d, "pack", name)
			if s.seen[p] {
				continue
			}
			pk, err := openPack(p)
			if err != nil {
				return err
			}
			s.seen[p] = true
			s.packs = append(s.packs, pk)
		}
	}
	return nil
}

// openPack loads an idx v2 file and opens its pack.
func openPack(idxPath string) (*pack, error) {
	raw, err := os.ReadFile(idxPath)
	if err != nil {
		return nil, err
	}
	if len(raw) < 8+256*4 || !bytes.Equal(raw[:4], []byte{0xff, 't', 'O', 'c'}) || binary.BigEndian.Uint32(raw[4:8]) != 2 {
		return nil, errUnsupported
	}
	p := &pack{path: strings.TrimSuffix(idxPath, ".idx") + ".pack"}
	for i := range 256 {
		p.fanout[i] = binary.BigEndian.Uint32(raw[8+i*4:])
	}
	n := int(p.fanout[255])
	pos := 8 + 256*4
	if len(raw) < pos+n*20+n*4+n*4 {
		return nil, errUnsupported
	}
	p.ids = raw[pos : pos+n*20]
	pos += n*20 + n*4 // skip crc32
	p.offsets = make([]uint32, n)
	for i := range n {
		p.offsets[i] = binary.BigEndian.Uint32(raw[pos+i*4:])
	}
	pos += n * 4
	for i := 0; pos+8 <= len(raw)-40; i++ {
		p.large = append(p.large, binary.BigEndian.Uint64(raw[pos:]))
		pos += 8
	}
	f, err := os.Open(p.path)
	if err != nil {
		return nil, err
	}
	p.f = f
	return p, nil
}

// find returns the pack offset of id.
func (p *pack) find(id []byte) (int64, bool) {
	lo := uint32(0)
	if id[0] > 0 {
		lo = p.fanout[id[0]-1]
	}
	hi := p.fanout[id[0]]
	i := sort.Search(int(hi-lo), func(i int) bool {
		return bytes.Compare(p.ids[(int(lo)+i)*20:(int(lo)+i)*20+20], id) >= 0
	}) + int(lo)
	if i >= int(hi) || !bytes.Equal(p.ids[i*20:i*20+20], id) {
		return 0, false
	}
	off := p.offsets[i]
	if off&0x80000000 != 0 {
		k := int(off & 0x7fffffff)
		if k >= len(p.large) {
			return 0, false
		}
		return int64(p.large[k]), true
	}
	return int64(off), true
}

// readPacked finds id in a loaded pack.
func (s *objectStore) readPacked(id []byte, depth int) (object, error) {
	if len(s.packs) == 0 {
		if err := s.loadPacks(); err != nil {
			return object{}, err
		}
	}
	for _, p := range s.packs {
		if off, ok := p.find(id); ok {
			return s.readAt(p, off, depth)
		}
	}
	return object{}, errMissing
}

// Pack entry type numbers.
const (
	packCommit   = 1
	packTree     = 2
	packBlob     = 3
	packTag      = 4
	packOfsDelta = 6
	packRefDelta = 7
)

// packTypeName maps a non-delta pack type to its name.
var packTypeName = map[int]string{packCommit: typeCommit, packTree: typeTree, packBlob: typeBlob, packTag: typeTag}

// readAt decodes the entry at off in p, resolving deltas.
func (s *objectStore) readAt(p *pack, off int64, depth int) (object, error) {
	if depth > maxDeltaDepth {
		return object{}, errUnsupported
	}
	var hdr [32]byte
	n, err := p.f.ReadAt(hdr[:], off)
	if n == 0 && err != nil {
		return object{}, fmt.Errorf("gitfast: read pack %s: %w", p.path, err)
	}
	b := hdr[:n]
	i := 0
	c := b[i]
	i++
	typ := int(c>>4) & 7
	size := uint64(c & 15)
	shift := uint(4)
	for c&0x80 != 0 {
		if i >= len(b) {
			return object{}, errUnsupported
		}
		c = b[i]
		i++
		size |= uint64(c&0x7f) << shift
		shift += 7
	}
	switch typ {
	case packCommit, packTree, packBlob, packTag:
		data, err := inflateAt(p.f, off+int64(i), size)
		if err != nil {
			return object{}, err
		}
		return object{typ: packTypeName[typ], data: data}, nil
	case packOfsDelta:
		if i >= len(b) {
			return object{}, errUnsupported
		}
		c = b[i]
		i++
		rel := int64(c & 0x7f)
		for c&0x80 != 0 {
			if i >= len(b) {
				return object{}, errUnsupported
			}
			c = b[i]
			i++
			rel = ((rel + 1) << 7) | int64(c&0x7f)
		}
		delta, err := inflateAt(p.f, off+int64(i), size)
		if err != nil {
			return object{}, err
		}
		base, err := s.readAt(p, off-rel, depth+1)
		if err != nil {
			return object{}, err
		}
		data, err := applyDelta(base.data, delta)
		if err != nil {
			return object{}, err
		}
		return object{typ: base.typ, data: data}, nil
	case packRefDelta:
		if i+20 > len(b) {
			return object{}, errUnsupported
		}
		baseID := hex.EncodeToString(b[i : i+20])
		delta, err := inflateAt(p.f, off+int64(i+20), size)
		if err != nil {
			return object{}, err
		}
		base, err := s.readLocked(baseID, depth+1)
		if err != nil {
			return object{}, err
		}
		data, err := applyDelta(base.data, delta)
		if err != nil {
			return object{}, err
		}
		return object{typ: base.typ, data: data}, nil
	}
	return object{}, errUnsupported
}

// inflateAt inflates the zlib stream at off that holds size bytes.
func inflateAt(f *os.File, off int64, size uint64) ([]byte, error) {
	zr, err := zlib.NewReader(io.NewSectionReader(f, off, 1<<62))
	if err != nil {
		return nil, errUnsupported
	}
	out := make([]byte, size)
	if _, err := io.ReadFull(zr, out); err != nil {
		return nil, errUnsupported
	}
	return out, nil
}

// applyDelta builds the target of a git delta from base.
func applyDelta(base, delta []byte) ([]byte, error) {
	i := 0
	varint := func() (uint64, bool) {
		var v uint64
		var shift uint
		for {
			if i >= len(delta) {
				return 0, false
			}
			c := delta[i]
			i++
			v |= uint64(c&0x7f) << shift
			shift += 7
			if c&0x80 == 0 {
				return v, true
			}
		}
	}
	srcSize, ok1 := varint()
	dstSize, ok2 := varint()
	if !ok1 || !ok2 || srcSize != uint64(len(base)) {
		return nil, errUnsupported
	}
	out := make([]byte, 0, dstSize)
	for i < len(delta) {
		op := delta[i]
		i++
		if op&0x80 != 0 {
			var off, n uint64
			for k := range 4 {
				if op&(1<<k) != 0 {
					if i >= len(delta) {
						return nil, errUnsupported
					}
					off |= uint64(delta[i]) << (8 * k)
					i++
				}
			}
			for k := range 3 {
				if op&(0x10<<k) != 0 {
					if i >= len(delta) {
						return nil, errUnsupported
					}
					n |= uint64(delta[i]) << (8 * k)
					i++
				}
			}
			if n == 0 {
				n = 0x10000
			}
			if off+n > uint64(len(base)) {
				return nil, errUnsupported
			}
			out = append(out, base[off:off+n]...)
		} else if op != 0 {
			if i+int(op) > len(delta) {
				return nil, errUnsupported
			}
			out = append(out, delta[i:i+int(op)]...)
			i += int(op)
		} else {
			return nil, errUnsupported
		}
	}
	if uint64(len(out)) != dstSize {
		return nil, errUnsupported
	}
	return out, nil
}

// close releases the open pack files.
func (s *objectStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.packs {
		_ = p.f.Close() // read-only file; nothing to flush
	}
	s.packs = nil
	clear(s.seen)
}
