package llm

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

// TestToolsCarryTheirArgumentSchema is the guard against the loop that a bare
// "{"type":"object"}" causes: a model told a tool takes no arguments can only
// guess, and guessing produces the same failed call forever.
//
// The declarations here are built the way functiontool builds them — the real
// schema in ParametersJsonSchema, Parameters left nil — so this fails against
// the exact shape the ADK hands over, not a convenient one.
func TestToolsCarryTheirArgumentSchema(t *testing.T) {
	decls := []*genai.FunctionDeclaration{{
		Name:        "list_dir",
		Description: "Lists directory entries.",
		ParametersJsonSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "description": "Directory to list"},
				"recursive": map[string]any{"type": "boolean", "description": "Walk the tree"},
			},
			"required": []string{"path"},
		},
	}}

	got := genaiToolsToChat([]*genai.Tool{{FunctionDeclarations: decls}})
	if len(got) != 1 {
		t.Fatalf("want 1 tool, got %d", len(got))
	}
	params, ok := got[0].Function.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("parameters is %T, want map[string]any", got[0].Function.Parameters)
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		b, _ := json.Marshal(params)
		t.Fatalf("list_dir went on the wire with no properties: %s", b)
	}
	for _, name := range []string{"path", "recursive"} {
		if _, ok := props[name]; !ok {
			t.Errorf("property %q missing from the wire schema", name)
		}
	}
	if params["type"] != "object" {
		t.Errorf("type = %v, want object", params["type"])
	}
}

// TestToolSchemaTypeIsLowercased covers the normalisation the chat format
// needs: jsonschema-go and genai spell the type in ways JSON Schema does not
// accept, and a schema the endpoint rejects costs the whole turn.
func TestToolSchemaTypeIsLowercased(t *testing.T) {
	decls := []*genai.FunctionDeclaration{{
		Name: "list_dir",
		ParametersJsonSchema: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "STRING",
					"description": "Directory to list",
				},
			},
		},
	}}

	got := genaiToolsToChat([]*genai.Tool{{FunctionDeclarations: decls}})
	params := got[0].Function.Parameters.(map[string]any)
	if params["type"] != "object" {
		t.Errorf("top-level type = %v, want object", params["type"])
	}
	prop := params["properties"].(map[string]any)["path"].(map[string]any)
	if prop["type"] != "string" {
		t.Errorf("property type = %v, want string", prop["type"])
	}
	if prop["description"] != "Directory to list" {
		t.Errorf("description dropped: %v", prop)
	}
}

// TestToolWithNoSchemaStillGetsAnObject keeps a genuinely argument-less tool
// valid: the chat format wants an object schema, not an absent one.
func TestToolWithNoSchemaStillGetsAnObject(t *testing.T) {
	decls := []*genai.FunctionDeclaration{{Name: "todo_read"}}

	got := genaiToolsToChat([]*genai.Tool{{FunctionDeclarations: decls}})
	params, ok := got[0].Function.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("parameters is %T, want map[string]any", got[0].Function.Parameters)
	}
	if params["type"] != "object" {
		t.Errorf("type = %v, want object", params["type"])
	}
}

// TestToolSchemaRequiredSurvives makes sure the required list reaches the wire:
// it is what tells the model a path is mandatory rather than optional.
func TestToolSchemaRequiredSurvives(t *testing.T) {
	decls := []*genai.FunctionDeclaration{{
		Name: "read_file",
		ParametersJsonSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []any{"path"},
		},
	}}

	got := genaiToolsToChat([]*genai.Tool{{FunctionDeclarations: decls}})
	params := got[0].Function.Parameters.(map[string]any)
	req, ok := params["required"].([]any)
	if !ok {
		b, _ := json.Marshal(params)
		t.Fatalf("required is %T, want []any: %s", params["required"], b)
	}
	if len(req) != 1 || req[0] != "path" {
		t.Errorf("required = %v, want [path]", req)
	}
}

// TestToolSchemaIsAlwaysJSONMarshalable guards against a schema that survives
// the conversion but panics the request on the way out.
func TestToolSchemaIsAlwaysJSONMarshalable(t *testing.T) {
	decls := []*genai.FunctionDeclaration{{
		Name:                 "write_file",
		ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{"content": map[string]any{"type": "string"}}},
	}}
	got := genaiToolsToChat([]*genai.Tool{{FunctionDeclarations: decls}})
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("tool set does not marshal: %v", err)
	}
}
