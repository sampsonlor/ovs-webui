// Package apicontract compiles the embedded OpenAPI 3.1 schemas. No request can
// select a schema URL or trigger network/filesystem schema loading.
package apicontract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed openapi.json
var Document []byte

type Parameter struct {
	Name, In string
	Required bool
	Schema   map[string]any
	compiled *jsonschema.Schema
}
type Operation struct {
	ID, Method, Path, Domain, Capability, ResourceKind string
	ServiceIssue                                       int
	Public, Sensitive, SecretResponse                  bool
	Parameters                                         []Parameter
	Body                                               *jsonschema.Schema
	Responses                                          map[int]*jsonschema.Schema
	pattern                                            *regexp.Regexp
	names                                              []string
}
type Contract struct {
	Operations []*Operation
	Schemas    map[string]*jsonschema.Schema
}
type specOperation struct {
	ID             string      `json:"operationId"`
	Capability     string      `json:"x-capability"`
	Domain         string      `json:"x-request-domain"`
	ResourceKind   string      `json:"x-resource-kind"`
	Issue          int         `json:"x-service-issue"`
	Sensitive      bool        `json:"x-sensitive-request"`
	SecretResponse bool        `json:"x-secret-response"`
	Parameters     []Parameter `json:"parameters"`
	Body           *struct {
		Content map[string]struct{ Schema map[string]any }
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct{ Schema map[string]any }
	} `json:"responses"`
}

func New() (*Contract, error) {
	var wire struct {
		Paths      map[string]map[string]specOperation
		Components struct{ Schemas map[string]any }
	}
	if err := json.Unmarshal(Document, &wire); err != nil {
		return nil, err
	}
	root, err := jsonschema.UnmarshalJSON(bytes.NewReader(Document))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const base = "urn:ovs-webui:public:v1"
	if err = compiler.AddResource(base, root); err != nil {
		return nil, err
	}
	out := &Contract{Schemas: map[string]*jsonschema.Schema{}}
	for name := range wire.Components.Schemas {
		schema, err := compiler.Compile(base + "#/components/schemas/" + name)
		if err != nil {
			return nil, err
		}
		out.Schemas[name] = schema
	}
	parameterIndex := 0
	compile := func(schema map[string]any) (*jsonschema.Schema, error) {
		// Absolute component refs let synthetic parameter roots share this compiler.
		var absolutize func(any) any
		absolutize = func(v any) any {
			switch t := v.(type) {
			case map[string]any:
				r := map[string]any{}
				for k, v := range t {
					if k == "$ref" {
						if ref, ok := v.(string); ok && strings.HasPrefix(ref, "#/") {
							v = base + ref
						}
					}
					r[k] = absolutize(v)
				}
				return r
			case []any:
				r := make([]any, len(t))
				for i, v := range t {
					r[i] = absolutize(v)
				}
				return r
			default:
				return v
			}
		}
		parameterIndex++
		key := fmt.Sprintf("urn:ovs-webui:parameter:%d", parameterIndex)
		if err := compiler.AddResource(key, absolutize(schema)); err != nil {
			return nil, err
		}
		return compiler.Compile(key)
	}
	for path, methods := range wire.Paths {
		for method, w := range methods {
			op := &Operation{ID: w.ID, Method: strings.ToUpper(method), Path: "/api/v1" + path, Domain: w.Domain, Capability: w.Capability, ResourceKind: w.ResourceKind, ServiceIssue: w.Issue, Sensitive: w.Sensitive, SecretResponse: w.SecretResponse, Parameters: w.Parameters, Responses: map[int]*jsonschema.Schema{}}
			op.Public = w.ID == "readOpenAPI" || w.ID == "readContract" || w.ID == "readRuntime" || w.ID == "createSession"
			pieces := strings.Split(op.Path, "/")
			for i, p := range pieces {
				if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
					op.names = append(op.names, p[1:len(p)-1])
					pieces[i] = "([^/]+)"
				} else {
					pieces[i] = regexp.QuoteMeta(p)
				}
			}
			op.pattern = regexp.MustCompile("^" + strings.Join(pieces, "/") + "$")
			for i := range op.Parameters {
				schema, err := compile(op.Parameters[i].Schema)
				if err != nil {
					return nil, err
				}
				op.Parameters[i].compiled = schema
			}
			if w.Body != nil {
				op.Body, err = compile(w.Body.Content["application/json"].Schema)
				if err != nil {
					return nil, err
				}
			}
			for code, response := range w.Responses {
				status, err := strconv.Atoi(code)
				if err != nil {
					continue
				}
				if content, ok := response.Content["application/json"]; ok {
					op.Responses[status], err = compile(content.Schema)
					if err != nil {
						return nil, err
					}
				} else if status == 204 {
					op.Responses[status] = nil
				}
			}
			out.Operations = append(out.Operations, op)
		}
	}
	sort.Slice(out.Operations, func(i, j int) bool { return out.Operations[i].Path < out.Operations[j].Path })
	return out, nil
}
func (c *Contract) Match(method, path string) (*Operation, map[string]string, []string) {
	allowed := []string{}
	for _, op := range c.Operations {
		m := op.pattern.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		if op.Method != method {
			allowed = append(allowed, op.Method)
			continue
		}
		params := map[string]string{}
		for i, n := range op.names {
			params[n] = m[i+1]
		}
		return op, params, nil
	}
	return nil, nil, allowed
}
func (o *Operation) ValidateParameters(path map[string]string, query url.Values, header func(string) []string) error {
	known := map[string]bool{}
	for _, p := range o.Parameters {
		var values []string
		switch p.In {
		case "path":
			values = []string{path[p.Name]}
		case "query":
			known[p.Name] = true
			values = query[p.Name]
		case "header":
			values = header(p.Name)
		}
		if len(values) > 1 {
			return apitypes.Fail(400, "AMBIGUOUS_REQUEST")
		}
		if len(values) == 0 {
			if p.Required {
				if p.Name == "If-Match" {
					return apitypes.Fail(428, "PRECONDITION_REQUIRED")
				}
				return apitypes.Fail(400, "REQUIRED_PARAMETER")
			}
			continue
		}
		var value any = values[0]
		if p.Schema["type"] == "integer" {
			n, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil {
				return apitypes.Fail(422, "INVALID_PARAMETER")
			}
			value = n
		}
		if err := p.compiled.Validate(value); err != nil {
			return apitypes.Fail(422, "INVALID_PARAMETER")
		}
	}
	for k := range query {
		if !known[k] {
			return apitypes.Fail(400, "UNKNOWN_PARAMETER")
		}
	}
	return nil
}
func (o *Operation) Decode(data []byte) (map[string]any, []byte, error) {
	value, err := ipc.ParseObject(data)
	if err != nil {
		return nil, nil, apitypes.Fail(400, "INVALID_JSON")
	}
	if o.Body == nil || o.Body.Validate(value) != nil {
		return nil, nil, apitypes.Fail(422, "INVALID_REQUEST")
	}
	canonical, err := json.Marshal(value)
	return value, canonical, err
}
func (o *Operation) ValidateResponse(status int, body []byte) error {
	schema, declared := o.Responses[status]
	if declared && status == 204 && len(body) == 0 {
		return nil
	}
	if schema == nil {
		return apitypes.Fail(500, "INVALID_SERVICE_RESPONSE")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil || schema.Validate(value) != nil {
		return apitypes.Fail(500, "INVALID_SERVICE_RESPONSE")
	}
	return nil
}
