package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraft202012Validation(t *testing.T) {
	// minContains is Draft 2019-09/2020-12 behavior absent from the former
	// Inframill evaluator.
	schema := map[string]any{
		"$schema":   "https://json-schema.org/draft/2020-12/schema",
		testTypeKey: testObjectType,
		testPropertiesKey: map[string]any{testPortsKey: map[string]any{
			testTypeKey: testArrayType, "contains": map[string]any{testTypeKey: "integer"}, "minContains": float64(2),
		}},
	}
	violations := compileAndValidate(t, schema, map[string]any{testPortsKey: []any{float64(80), "443"}})
	if len(violations) == 0 || !strings.HasPrefix(violations[0].Path, testPortsKey) {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestStandardValidationPaths(t *testing.T) {
	schema := map[string]any{
		testTypeKey: testObjectType, testRequiredKey: []any{testSizeKey, testPortsKey},
		testPropertiesKey: map[string]any{
			testSizeKey: map[string]any{testTypeKey: "string", "minLength": float64(2)},
			testPortsKey: map[string]any{
				testTypeKey: testArrayType, "minItems": float64(1),
				"items": map[string]any{testTypeKey: "string", "pattern": `^[0-9]+$`},
			},
			testNodesKey: map[string]any{
				testTypeKey: testObjectType, "propertyNames": map[string]any{"pattern": `^[a-z]+$`},
				"additionalProperties": map[string]any{testTypeKey: "boolean"},
			},
		},
		"additionalProperties": false,
	}
	for name, testCase := range map[string]struct {
		values  map[string]any
		path    string
		message string
	}{
		"required":      {values: map[string]any{testPortsKey: []any{"80"}}, path: testSizeKey, message: "required"},
		"type":          {values: map[string]any{testSizeKey: float64(1), testPortsKey: []any{"80"}}, path: testSizeKey, message: "type string"},
		"minimum items": {values: map[string]any{testSizeKey: "sm", testPortsKey: []any{}}, path: testPortsKey, message: "at least 1 items"},
		"item pattern":  {values: map[string]any{testSizeKey: "sm", testPortsKey: []any{"http"}}, path: "ports.0", message: "must match"},
		"additional":    {values: map[string]any{testSizeKey: "sm", testPortsKey: []any{"80"}, "extra": true}, path: "extra", message: "not allowed"},
		"property name": {values: map[string]any{testSizeKey: "sm", testPortsKey: []any{"80"}, testNodesKey: map[string]any{"Web-1": true}}, path: "nodes.Web-1", message: "match"},
	} {
		t.Run(name, func(t *testing.T) {
			violations := compileAndValidate(t, schema, testCase.values)
			for _, violation := range violations {
				if violation.Path == testCase.path && strings.Contains(violation.Message, testCase.message) {
					return
				}
			}
			t.Fatalf("violations = %#v", violations)
		})
	}
}

func TestExternalSchemaReferencesFailClosed(t *testing.T) {
	validator, err := (Compiler{}).Compile(map[string]map[string]any{testSchemaName: {"$ref": "file:///etc/passwd"}})
	if err != nil {
		t.Fatal(err)
	}
	violations := validator.Validate(testSchemaName, map[string]any{})
	if len(violations) != 1 || !strings.Contains(violations[0].Message, "is not allowed") {
		t.Fatalf("violations = %#v", violations)
	}
}

const debugMessageKey = "message"

// SPEC-0000 REQ-007/010: the shipped neutral example has a strict Values schema.
func TestDebugBlueprintSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "blueprints", "debug", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var definition map[string]any
	if err = json.Unmarshal(data, &definition); err != nil {
		t.Fatal(err)
	}
	validator, err := (Compiler{}).Compile(map[string]map[string]any{"debug": definition})
	if err != nil {
		t.Fatal(err)
	}
	if violations := validator.Validate("debug", map[string]any{debugMessageKey: "hello", "mode": "success"}); len(violations) != 0 {
		t.Fatal(violations)
	}
	for _, values := range []map[string]any{{}, {debugMessageKey: 7}, {debugMessageKey: "ok", "mode": "invalid"}, {debugMessageKey: "ok", "unknown": true}} {
		if violations := validator.Validate("debug", values); len(violations) == 0 {
			t.Fatal("invalid Values accepted", values)
		}
	}
}
