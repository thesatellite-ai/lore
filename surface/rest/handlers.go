package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/surface/docs"
)

// handlers holds what every route needs.
type handlers struct {
	reg    *op.Registry
	runner op.Runner
	opts   Options
}

// operation is the handler of one operation's route. It only assembles the
// JSON input and the Caller; decoding, checks and the call itself happen in
// the Runner.
func (h *handlers) operation(e op.Entry, rt op.Route) http.Handler {
	fields := map[string]op.FieldInfo{}
	for _, f := range e.InputFields() {
		fields[f.Name] = f
	}
	asks := docs.AsksFirst(e.Spec())
	withID := docs.PathHasID(rt.Path)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent, err := agentOf(r)
		if err != nil {
			writeError(w, err)
			return
		}
		body, yes, err := input(r, rt.Method, fields, asks, withID)
		if err != nil {
			writeError(w, err)
			return
		}
		// OV3/REST: the agent is the HTTP client's (X-Lore-Agent or none), never
		// the `lore api` server process's own detection (AgentFixed).
		secret, err := secretChoiceOf(r) // SECRETS 4 over REST (P2-CTX-5)
		if err != nil {
			writeError(w, err)
			return
		}
		caller := op.Caller{Actor: h.opts.Actor, Agent: agent, AgentFixed: true, Interactive: false, Yes: yes, Secret: secret}
		res, err := h.runner.Run(r.Context(), e, op.JSONDecoder{Body: body}, caller)
		if err != nil {
			writeError(w, err)
			return
		}
		b, err := res.JSON()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, rt.Status, b)
	})
}

// agentOf reads X-Lore-Agent: printable, at most MaxAgentLen bytes.
func agentOf(r *http.Request) (string, error) {
	a := r.Header.Get(HeaderAgent)
	if len(a) > MaxAgentLen {
		return "", errs.New(errs.InvalidInput, HeaderAgent+" is longer than "+strconv.Itoa(MaxAgentLen)+" bytes")
	}
	for _, c := range a {
		if !unicode.IsPrint(c) {
			return "", errs.New(errs.InvalidInput, HeaderAgent+" holds a control character")
		}
	}
	return a, nil
}

// input builds the JSON object handed to op.JSONDecoder: the body (POST,
// PATCH, DELETE), the query parameters (GET, DELETE), and the {id} path
// value. The "yes" control field is taken out for operations that ask
// first; for any other operation it stays and the decoder refuses it as
// unknown.
func input(r *http.Request, method string, fields map[string]op.FieldInfo, asks, withID bool) ([]byte, bool, error) {
	obj := map[string]json.RawMessage{}
	raw, err := io.ReadAll(r.Body)
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return nil, false, errs.New(errs.BodyTooLarge, "request body is over "+strconv.Itoa(MaxBodyBytes)+" bytes")
	case err != nil:
		return nil, false, errs.Wrap(errs.InvalidInput, "read request body", err)
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if method == http.MethodGet {
			return nil, false, errs.New(errs.InvalidInput, "a GET request takes query parameters, not a body")
		}
		if jErr := json.Unmarshal(raw, &obj); jErr != nil || obj == nil {
			return nil, false, errs.New(errs.InvalidInput, "the request body must be one JSON object")
		}
	}
	query := r.URL.Query()
	if len(query) > 0 && !docs.QueryInput(method) {
		return nil, false, errs.New(errs.InvalidInput, "a "+method+" request takes a JSON body, not query parameters")
	}
	if qErr := addQuery(obj, query, fields, asks); qErr != nil {
		return nil, false, qErr
	}
	if withID {
		id, idErr := json.Marshal(r.PathValue(docs.IDParam))
		if idErr != nil {
			return nil, false, errs.Wrap(errs.Internal, "encode id", idErr)
		}
		if prev, ok := obj[docs.IDParam]; ok && !bytes.Equal(bytes.TrimSpace(prev), id) {
			return nil, false, errs.New(errs.InvalidInput, "the body id differs from the id in the path")
		}
		obj[docs.IDParam] = id
	}
	yes := false
	if v, ok := obj[docs.YesField]; ok && asks {
		if yErr := json.Unmarshal(v, &yes); yErr != nil {
			return nil, false, errs.New(errs.InvalidInput, `"yes" must be true or false`)
		}
		delete(obj, docs.YesField)
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, false, errs.Wrap(errs.Internal, "encode input", err)
	}
	return b, yes, nil
}

// addQuery converts query parameters to JSON members by field type.
func addQuery(obj map[string]json.RawMessage, query url.Values, fields map[string]op.FieldInfo, asks bool) error {
	for name, vals := range query {
		if _, dup := obj[name]; dup {
			return errs.New(errs.InvalidInput, "parameter "+name+" given twice")
		}
		f, ok := fields[name]
		if name == docs.YesField && asks && !ok {
			f, ok = op.FieldInfo{Name: name, Type: op.TypeBool}, true
		}
		if !ok {
			return errs.New(errs.InvalidInput, "unknown parameter "+name)
		}
		v, err := queryValue(f, vals)
		if err != nil {
			return err
		}
		obj[name] = v
	}
	return nil
}

// queryValue is one parameter's JSON, parsed by the field's type.
func queryValue(f op.FieldInfo, vals []string) (json.RawMessage, error) {
	bad := func(want string) error { return errs.New(errs.InvalidInput, "parameter "+f.Name+": want "+want) }
	switch f.Type {
	case op.TypeStrings:
		return marshal(vals)
	case op.TypeStringMap:
		m := map[string]string{}
		for _, kv := range vals {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return nil, bad("key=value")
			}
			m[k] = v
		}
		return marshal(m)
	case op.TypeObject, op.TypeList:
		return nil, errs.New(errs.Internal, "parameter "+f.Name+": not an input field")
	case op.TypeString, op.TypeText, op.TypeBool, op.TypeInt, op.TypeUint, op.TypeFloat, op.TypeDuration:
	}
	if len(vals) != 1 {
		return nil, bad("one value")
	}
	s := vals[0]
	switch f.Type {
	case op.TypeBool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, bad("true or false")
		}
		return marshal(b)
	case op.TypeInt:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, bad("an integer")
		}
		return marshal(n)
	case op.TypeUint:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, bad("a non-negative integer")
		}
		return marshal(n)
	case op.TypeFloat:
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, bad("a number")
		}
		return marshal(n)
	case op.TypeDuration:
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, bad("a duration like 45m or 1h30m")
		}
		// a time.Duration field decodes from integer nanoseconds.
		return marshal(int64(d))
	case op.TypeString, op.TypeText, op.TypeStrings, op.TypeStringMap, op.TypeObject, op.TypeList:
	}
	return marshal(s)
}

// marshal encodes a concrete value (json.Marshal's `any` parameter is the
// stdlib signature; T is always a concrete type here).
func marshal[T string | bool | int64 | uint64 | float64 | []string | map[string]string](v T) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "encode parameter", err)
	}
	return b, nil
}

// openapi serves the OpenAPI document with the attached stores' kinds.
func (h *handlers) openapi(w http.ResponseWriter, r *http.Request) {
	kinds, err := h.kinds(r)
	if err != nil {
		writeError(w, err)
		return
	}
	defs := make([]schema.TableDef, len(kinds))
	for i, k := range kinds {
		defs[i] = k.Def
	}
	b, err := docs.OpenAPI(h.reg, defs)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bytes.TrimRight(b, "\n"))
}

// kinds asks the KindSource, or none.
func (h *handlers) kinds(r *http.Request) ([]Kind, error) {
	if h.opts.Kinds == nil {
		return nil, nil
	}
	return h.opts.Kinds.Kinds(r.Context())
}
