package op

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// PageFlags are a list's --limit / --page (T-4, S-3 sibling flags): one
// shared input for every noun's list operation, as PurgeFlags is for every
// delete. A list input embeds it, so the CLI flags, the `lore api` JSON
// fields ("limit", "page") and the docs come from the one field set, and
// Invoke cuts the output's rows itself: no list operation pages by hand.
//
//	limit 0, page 0  ─► every row (the default, unchanged)
//	limit N          ─► the first N rows
//	limit N, page P  ─► rows (P-1)*N+1 … P*N
//	page P alone     ─► 50 rows per page (DefaultPageRows, as trash list)
//
// Why in Invoke and not per noun: before this, `lore search` took --limit
// while `lore task list` and `lore memory list` refused it ("unknown flag",
// found in a 5,493-row migration trial); a rule kept by each noun is missed
// by the next one. wire/argshape_test.go checks every *.list takes both.
//
// The output's rows are found once at Define (pageRowsOf): the field
// tagged `page:"rows"`, else the top-level slice named "data", else the
// only top-level slice. An int field named "count" that held the row count
// is set to the rows kept, so count always equals len(rows) in the JSON;
// the human text gets one line saying which rows it shows and the next page.
type PageFlags struct {
	Limit int `json:"limit,omitempty" flag:"limit" min:"1" desc:"at most this many rows (default: all; 50 when --page is given)"`
	Page  int `json:"page,omitempty" flag:"page" min:"1" desc:"page number, from 1 (with --limit)"`
}

// DefaultPageRows is --limit when only --page is given (trash list's
// DELETION 4.5 page size, so both page the same way).
const DefaultPageRows = 50

// tagPage marks the output field holding a list's rows when it is not the
// top-level "data" slice or the only slice (`page:"rows"`).
const (
	tagPage       = "page"
	tagPageRows   = "rows"
	pageDataName  = "data"
	pageCountJSON = "count"
)

// pageFlagsType is the embedded type an input pages by.
var pageFlagsType = reflect.TypeFor[PageFlags]()

// pageShape is where Invoke cuts an output: the rows slice and, when the
// output has one, its int count field (nil index = none).
type pageShape struct {
	flags []int // index of the embedded PageFlags in the input
	rows  []int
	count []int
}

// pageShapeOf returns the paging shape of an operation whose input type in
// embeds PageFlags, nil when it does not. It panics (a programmer error,
// caught at startup like every tag error) when the output has no rows
// field Invoke could cut.
func pageShapeOf(op string, in, out reflect.Type) *pageShape {
	flags, ok := embeddedIndex(in, pageFlagsType)
	if !ok {
		return nil
	}
	rows, ok := pageRowsOf(out)
	if !ok {
		panic(tagError{fmt.Sprintf("op.Define(%s): input embeds op.PageFlags but output %s has no rows to page: tag the rows slice `page:\"rows\"`", op, out)})
	}
	s := &pageShape{flags: flags, rows: rows}
	for i := range out.NumField() {
		sf := out.Field(i)
		if name, _, _ := strings.Cut(sf.Tag.Get(tagJSON), ","); name == pageCountJSON && sf.Type.Kind() == reflect.Int {
			s.count = sf.Index
		}
	}
	return s
}

// embeddedIndex is the index of t's anonymous field of type want.
func embeddedIndex(t reflect.Type, want reflect.Type) ([]int, bool) {
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	for i := range t.NumField() {
		if sf := t.Field(i); sf.Anonymous && sf.Type == want {
			return sf.Index, true
		}
	}
	return nil, false
}

// pageRowsOf finds the rows slice of output type t: a `page:"rows"` field
// (top level or one struct down, for an envelope such as task.checklist's
// data.items), else the top-level "data" slice, else the only top-level
// slice.
func pageRowsOf(t reflect.Type) ([]int, bool) {
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	var data, only []int
	nSlices := 0
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		if sf.Tag.Get(tagPage) == tagPageRows && sf.Type.Kind() == reflect.Slice {
			return sf.Index, true
		}
		if sf.Type.Kind() == reflect.Struct {
			for j := range sf.Type.NumField() {
				in := sf.Type.Field(j)
				if in.IsExported() && in.Tag.Get(tagPage) == tagPageRows && in.Type.Kind() == reflect.Slice {
					return append([]int{i}, in.Index...), true
				}
			}
		}
		if sf.Type.Kind() != reflect.Slice {
			continue
		}
		nSlices++
		only = sf.Index
		if name, _, _ := strings.Cut(sf.Tag.Get(tagJSON), ","); name == pageDataName {
			data = sf.Index
		}
	}
	switch {
	case data != nil:
		return data, true
	case nSlices == 1:
		return only, true
	}
	return nil, false
}

// pageFlags reads the embedded PageFlags of input value v and refuses a
// negative number (E_INVALID_INPUT, with the command's own example).
func (s *pageShape) pageFlags(op string, v reflect.Value) (PageFlags, error) {
	fv := v.FieldByIndex(s.flags)
	f := PageFlags{Limit: int(fv.FieldByName("Limit").Int()), Page: int(fv.FieldByName("Page").Int())}
	if f.Limit < 0 || f.Page < 0 {
		cmd := strings.Join(append([]string{"lore"}, CLIPath(op)...), " ")
		return f, errs.New(errs.InvalidInput, "--limit and --page take a positive number").
			WithHint("e.g. " + cmd + " --limit 20 --page 2")
	}
	return f, nil
}

// cut keeps the asked page of out's rows (out is addressable) and returns
// the human line saying so; "" when nothing was cut.
func (s *pageShape) cut(f PageFlags, out reflect.Value) string {
	limit := f.Limit
	if f.Page > 0 && limit == 0 {
		limit = DefaultPageRows
	}
	if limit == 0 {
		return ""
	}
	rows := out.FieldByIndex(s.rows)
	total := rows.Len()
	pg := max(f.Page, 1)
	start := min((pg-1)*limit, total)
	end := min(start+limit, total)
	if start == 0 && end == total {
		return ""
	}
	rows.Set(rows.Slice(start, end))
	if s.count != nil {
		if c := out.FieldByIndex(s.count); int(c.Int()) == total {
			c.SetInt(int64(end - start))
		}
	}
	if start == end {
		return fmt.Sprintf("page %d is past the last row (%d rows, %d per page)\n", pg, total, limit)
	}
	line := "rows " + strconv.Itoa(start+1) + "–" + strconv.Itoa(end) + " of " + strconv.Itoa(total)
	if end < total {
		line += "; next: --limit " + strconv.Itoa(limit) + " --page " + strconv.Itoa(pg+1)
	}
	return line + "\n"
}
