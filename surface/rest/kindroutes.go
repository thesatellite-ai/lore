package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/surface/docs"
)

// Dynamic-kind routes (STORE_SERVER_SPEC 9.6, RESOURCES_DESIGN 3.3,
// CLI_REFERENCE "Dynamic kinds"): a kind is data, so it has no route of its
// own in the registry; these two shapes are rewritten onto the generic
// resource operations, so the CLI and REST run the same validation (ONE
// OPERATION SPEC: an unknown field is E_RESOURCE_INVALID on both).
//
//	POST /v1/resources/<kind>  {"name", "fields": {f: v}, "to", "yes"}
//	     → POST /v1/resources  {"kind", "name", "set": ["f=v"], "set_json": ["f=<json>"], "to", "yes"}
//	GET  /v1/resources/<kind>?where=…
//	     → GET  /v1/resources?kind=<kind>&where=…
//
// A segment that parses as a res_ id stays the show route, and any literal
// sub-route the registry serves (stale, import, …) wins over a kind name.
const (
	resourcesPath = "/v1/resources"
	// Input fields of the generic resource add/list operations.
	inKind    = "kind"
	inName    = "name"
	inSet     = "set"
	inSetJSON = "set_json"
	inTo      = "to"
	// bodyFields is the per-kind body's object of field values.
	bodyFields = "fields"
)

// kindRoutes rewrites the two dynamic-kind shapes, then serves mux.
func kindRoutes(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, ok := kindSegment(r.URL.Path)
		if !ok || !isKindRoute(mux, r, kind) {
			mux.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			q := r.URL.Query()
			q.Set(inKind, kind)
			r2 := r.Clone(r.Context())
			r2.URL = &url.URL{Path: resourcesPath, RawQuery: q.Encode()}
			mux.ServeHTTP(w, r2)
		case http.MethodPost:
			body, err := kindBody(r, kind)
			if err != nil {
				writeError(w, err)
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL = &url.URL{Path: resourcesPath}
			r2.Body = io.NopCloser(bytes.NewReader(body))
			r2.ContentLength = int64(len(body))
			mux.ServeHTTP(w, r2)
		default:
			mux.ServeHTTP(w, r)
		}
	})
}

// kindSegment is <x> of /v1/resources/<x> (one segment, nothing after).
func kindSegment(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, resourcesPath+"/")
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

// isKindRoute: the segment is a kind, not a res_ id and not a literal
// route of the registry (the mux matches a pattern other than {id}).
func isKindRoute(mux *http.ServeMux, r *http.Request, seg string) bool {
	if id, err := ids.ParseAny(seg); err == nil && id.Prefix() == ids.PrefixResource {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return false
	}
	_, pattern := mux.Handler(r)
	switch pattern {
	case "", "/", r.Method + " " + resourcesPath + "/{" + docs.IDParam + "}":
		return true
	default:
		return false
	}
}

// kindBody turns the per-kind body into the generic add input: a string
// value is CLI text (--set), any other JSON value goes to --set-json.
func kindBody(r *http.Request, kind string) ([]byte, error) {
	raw, err := io.ReadAll(r.Body)
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return nil, errs.New(errs.BodyTooLarge, "request body is over "+strconv.Itoa(MaxBodyBytes)+" bytes")
	case err != nil:
		return nil, errs.Wrap(errs.InvalidInput, "read request body", err)
	}
	obj := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if jErr := json.Unmarshal(raw, &obj); jErr != nil || obj == nil {
			return nil, errs.New(errs.InvalidInput, "the request body must be one JSON object")
		}
	}
	for k := range obj {
		if !slices.Contains([]string{inName, bodyFields, inTo, docs.YesField}, k) {
			return nil, errs.New(errs.InvalidInput, "unknown body member "+strconv.Quote(k)+" (want name, fields, to, yes)")
		}
	}
	var fields map[string]json.RawMessage
	if f, ok := obj[bodyFields]; ok {
		if jErr := json.Unmarshal(f, &fields); jErr != nil {
			return nil, errs.New(errs.InvalidInput, `"fields" must be a JSON object`)
		}
	}
	var sets, setJSON []string
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		var s string
		if json.Unmarshal(fields[k], &s) == nil {
			sets = append(sets, k+"="+s)
		} else {
			setJSON = append(setJSON, k+"="+string(fields[k]))
		}
	}
	out := map[string]json.RawMessage{}
	for _, k := range []string{inName, inTo, docs.YesField} {
		if v, ok := obj[k]; ok {
			out[k] = v
		}
	}
	var mErr error
	put := func(k string, v []string) {
		if len(v) == 0 || mErr != nil {
			return
		}
		out[k], mErr = json.Marshal(v)
	}
	put(inSet, sets)
	put(inSetJSON, setJSON)
	if out[inKind], err = json.Marshal(kind); err != nil {
		return nil, errs.Wrap(errs.Internal, "encode kind", err)
	}
	if mErr != nil {
		return nil, errs.Wrap(errs.Internal, "encode fields", mErr)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "encode input", err)
	}
	return b, nil
}
