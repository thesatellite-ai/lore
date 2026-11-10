//go:build userscen

package userscen

// campaign_tasks_schema_test.go — the --json shape check of the tasks
// campaign: a CLI --json document is validated against the 2xx response
// schema docs/reference/openapi.json publishes for the same operation
// (by operationId), so a field the reference promises cannot go missing,
// change its JSON type or turn null where the schema does not allow it.
// The CLI and REST print one document per operation (rest_parity), so the
// published schema is the contract of both.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// campTasksOpenAPIPath is the published reference, relative to this
// package's directory (go test runs in it).
var campTasksOpenAPIPath = filepath.Join("..", "..", "docs", "reference", "openapi.json")

// campTasksSchema is the subset of JSON Schema the reference uses.
type campTasksSchema struct {
	Ref        string                      `json:"$ref"`
	Type       json.RawMessage             `json:"type"`
	Properties map[string]*campTasksSchema `json:"properties"`
	Required   []string                    `json:"required"`
	Items      *campTasksSchema            `json:"items"`
	AnyOf      []*campTasksSchema          `json:"anyOf"`
	AddProps   json.RawMessage             `json:"additionalProperties"`
}

// campTasksSpec is the loaded reference: response schema per operationId,
// and the components a $ref points into.
type campTasksSpec struct {
	byOp       map[string]*campTasksSchema
	components map[string]*campTasksSchema
}

var (
	campTasksSpecOnce sync.Once
	campTasksSpecVal  *campTasksSpec
	campTasksSpecErr  error
)

// campTasksLoadSpec reads openapi.json once per test binary.
func campTasksLoadSpec(t *testing.T) *campTasksSpec {
	t.Helper()
	campTasksSpecOnce.Do(func() {
		raw, err := os.ReadFile(campTasksOpenAPIPath)
		if err != nil {
			campTasksSpecErr = err
			return
		}
		var doc struct {
			Paths map[string]map[string]struct {
				OperationID string `json:"operationId"`
				Responses   map[string]struct {
					Content map[string]struct {
						Schema *campTasksSchema `json:"schema"`
					} `json:"content"`
				} `json:"responses"`
			} `json:"paths"`
			Components struct {
				Schemas map[string]*campTasksSchema `json:"schemas"`
			} `json:"components"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			campTasksSpecErr = err
			return
		}
		sp := &campTasksSpec{byOp: map[string]*campTasksSchema{}, components: doc.Components.Schemas}
		for _, methods := range doc.Paths {
			for _, o := range methods {
				for code, r := range o.Responses {
					if strings.HasPrefix(code, "2") {
						if c, ok := r.Content["application/json"]; ok && c.Schema != nil {
							sp.byOp[o.OperationID] = c.Schema
						}
					}
				}
			}
		}
		campTasksSpecVal = sp
	})
	if campTasksSpecErr != nil {
		t.Fatalf("read %s: %v", campTasksOpenAPIPath, campTasksSpecErr)
	}
	return campTasksSpecVal
}

// campTasksCheckShape fails t when doc (a CLI --json stdout) does not fit
// the reference's response schema of operation opID.
func campTasksCheckShape(t *testing.T, opID, doc string) {
	t.Helper()
	sp := campTasksLoadSpec(t)
	s, ok := sp.byOp[opID]
	if !ok {
		t.Fatalf("operation %s has no 2xx JSON response in the reference", opID)
	}
	var v json.RawMessage
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("%s: stdout is not JSON (%v): %.400s", opID, err, doc)
	}
	var problems []string
	sp.check(s, v, "$", &problems, 0)
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Errorf("%s --json does not match docs/reference/openapi.json:\n  %s\ndocument: %.800s", opID, strings.Join(problems, "\n  "), doc)
	}
}

// campTasksMaxDepth bounds $ref recursion (a self-referencing tree schema).
const campTasksMaxDepth = 40

// check validates v against s at path, appending what does not fit.
func (sp *campTasksSpec) check(s *campTasksSchema, v json.RawMessage, path string, out *[]string, depth int) {
	if s == nil || depth > campTasksMaxDepth {
		return
	}
	if s.Ref != "" {
		name := strings.TrimPrefix(s.Ref, "#/components/schemas/")
		ref, ok := sp.components[name]
		if !ok {
			*out = append(*out, path+": unresolved $ref "+s.Ref)
			return
		}
		sp.check(ref, v, path, out, depth+1)
		return
	}
	if len(s.AnyOf) > 0 {
		for _, alt := range s.AnyOf {
			var p []string
			sp.check(alt, v, path, &p, depth+1)
			if len(p) == 0 {
				return
			}
		}
		*out = append(*out, path+": fits none of anyOf: "+campTasksShort(v))
		return
	}
	var typ string
	if len(s.Type) > 0 {
		_ = json.Unmarshal(s.Type, &typ)
	}
	got := campTasksJSONType(v)
	if typ != "" && got != typ && !(typ == "number" && got == "integer") {
		*out = append(*out, fmt.Sprintf("%s: want %s, got %s (%s)", path, typ, got, campTasksShort(v)))
		return
	}
	switch got {
	case "object":
		var m map[string]json.RawMessage
		_ = json.Unmarshal(v, &m)
		for _, r := range s.Required {
			if _, ok := m[r]; !ok {
				*out = append(*out, path+": required field "+r+" is missing")
			}
		}
		for k, x := range m {
			if ps, ok := s.Properties[k]; ok {
				sp.check(ps, x, path+"."+k, out, depth+1)
			}
		}
	case "array":
		var l []json.RawMessage
		_ = json.Unmarshal(v, &l)
		for i, x := range l {
			sp.check(s.Items, x, fmt.Sprintf("%s[%d]", path, i), out, depth+1)
		}
	}
}

// campTasksJSONType is the JSON Schema type name of v.
func campTasksJSONType(v json.RawMessage) string {
	t := strings.TrimSpace(string(v))
	switch {
	case t == "null":
		return "null"
	case strings.HasPrefix(t, "{"):
		return "object"
	case strings.HasPrefix(t, "["):
		return "array"
	case strings.HasPrefix(t, `"`):
		return "string"
	case t == "true" || t == "false":
		return "boolean"
	case strings.ContainsAny(t, ".eE"):
		return "number"
	}
	return "integer"
}

// campTasksShort is v cut for a message.
func campTasksShort(v json.RawMessage) string {
	s := string(v)
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
