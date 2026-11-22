// Package vecfile keeps text vectors in one file between lore processes
// (layer L3, devco D-902, hunt H-157): `lore ask` reads every live row's
// vector, and embedding a store of thousands of rows costs ~80 µs a row,
// so a one-shot call reads the vectors earlier calls computed and embeds
// only the texts it has not seen.
//
// The file is a rebuildable cache: a header line naming the format, the
// model and the vector width, then fixed records (the text's 64-bit hash,
// the vector as little-endian float32, a CRC-32 of both). A file of
// another format, model or width reads as empty and is replaced by the
// next write; a damaged record is skipped. Writes replace the whole file
// (a temp file renamed over it), so two processes writing at once lose
// at most the other's new vectors, which a later call computes again.
package vecfile

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// version is the record format; a file of another reads as empty.
const version = 1

// MaxRecords bounds the file (~1 KB a record at width 256): past it a
// write keeps only the vectors it was handed (the caller hands the ones in
// use, however many), so the file of a home whose stores churn does not
// grow without end and the rows in use are never dropped.
const MaxRecords = 60_000

// header is the first line of a file of model and width dim.
func header(model string, dim int) string {
	return fmt.Sprintf("lore-vectors %d %s %d\n", version, strings.ReplaceAll(model, " ", "_"), dim)
}

// Read is the vectors of path for model and width dim; empty (no error)
// when the file is missing, of another format, model or width, or cut.
func Read(path, model string, dim int) map[uint64][]float32 {
	out := map[uint64][]float32{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	line, err := r.ReadString('\n')
	if err != nil || line != header(model, dim) {
		return out
	}
	rec := make([]byte, recordLen(dim))
	for {
		if _, err := io.ReadFull(r, rec); err != nil {
			return out // the end, or a cut last record
		}
		body, sum := rec[:len(rec)-4], binary.LittleEndian.Uint32(rec[len(rec)-4:])
		if crc32.ChecksumIEEE(body) != sum {
			continue // a damaged record
		}
		v := make([]float32, dim)
		for i := range v {
			v[i] = math.Float32frombits(binary.LittleEndian.Uint32(body[8+4*i:]))
		}
		out[binary.LittleEndian.Uint64(body)] = v
	}
}

// recordLen is a record's size at width dim.
func recordLen(dim int) int { return 8 + 4*dim + 4 }

// Write adds vecs (each of width dim) to path's vectors of model and
// writes the file anew (temp file, then rename). When the two together
// pass MaxRecords only vecs is kept. A vector of another width is left
// out.
func Write(path, model string, dim int, vecs map[uint64][]float32) error {
	all := Read(path, model, dim)
	for k, v := range vecs {
		if len(v) == dim {
			all[k] = v
		}
	}
	if len(all) > MaxRecords {
		all = map[uint64][]float32{}
		for k, v := range vecs {
			if len(v) == dim {
				all[k] = v
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("vecfile: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("vecfile: %w", err)
	}
	defer os.Remove(tmp.Name()) // gone after the rename; left by a failure
	w := bufio.NewWriterSize(tmp, 1<<16)
	w.WriteString(header(model, dim))
	rec := make([]byte, recordLen(dim))
	for k, v := range all {
		binary.LittleEndian.PutUint64(rec, k)
		for i, x := range v {
			binary.LittleEndian.PutUint32(rec[8+4*i:], math.Float32bits(x))
		}
		binary.LittleEndian.PutUint32(rec[len(rec)-4:], crc32.ChecksumIEEE(rec[:len(rec)-4]))
		w.Write(rec)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("vecfile: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("vecfile: write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("vecfile: %w", err)
	}
	return nil
}
