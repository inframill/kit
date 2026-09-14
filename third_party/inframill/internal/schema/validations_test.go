package schema

import (
	"strings"
	"testing"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	testArrayType     = "array"
	testCatalogKey    = "catalog"
	testNameKey       = "name"
	testNodesKey      = "nodes"
	testObjectType    = "object"
	testOneValue      = "one"
	testPortsKey      = "ports"
	testPropertiesKey = "properties"
	testRequiredKey   = "required"
	testSchemaName    = "test"
	testSizeKey       = "size"
	testTypeKey       = "type"
	testValueKey      = "value"
)

func TestSchemaValidationDeclarationsFailClosed(t *testing.T) {
	for name, testCase := range map[string]struct {
		validation any
		message    string
	}{
		"not an array":        {validation: true, message: "must be an array"},
		"not an object":       {validation: []any{uniqueFunction}, message: "validation must be an object"},
		"missing function":    {validation: []any{map[string]any{}}, message: "function must be a non-empty string"},
		"unknown function":    {validation: []any{map[string]any{functionField: "execute"}}, message: `unsupported function "execute"`},
		"unsupported field":   {validation: []any{map[string]any{functionField: uniqueFunction, "script": "run"}}, message: `does not support field "script"`},
		"invalid selector":    {validation: []any{map[string]any{functionField: uniqueFunction, selectField: "items.*"}}, message: "select must be a non-empty dotted field path"},
		"CIDR version":        {validation: []any{map[string]any{functionField: cidrFunction, versionField: float64(6)}}, message: "version must equal 4"},
		"CIDR scope":          {validation: []any{map[string]any{functionField: cidrFunction, versionField: float64(4), "scope": "public"}}, message: `scope must equal "private"`},
		"CIDR canonical type": {validation: []any{map[string]any{functionField: cidrFunction, versionField: float64(4), "canonical": "yes"}}, message: "canonical must be a boolean"},
		"reference target":    {validation: []any{map[string]any{functionField: referencesFunction}}, message: "target must be a dotted root path"},
	} {
		t.Run(name, func(t *testing.T) {
			schema := map[string]any{testTypeKey: testArrayType, validationsKey: testCase.validation}
			violations := compileAndValidate(t, schema, []any{})
			if len(violations) != 1 || !strings.Contains(violations[0].Message, testCase.message) {
				t.Fatalf("violations = %#v", violations)
			}
		})
	}
}

func TestSchemaValidationDeclarationsRequireArrays(t *testing.T) {
	schema := map[string]any{testTypeKey: testObjectType, validationsKey: []any{map[string]any{functionField: uniqueFunction}}}
	violations := compileAndValidate(t, schema, map[string]any{})
	if len(violations) != 1 || !strings.Contains(violations[0].Message, "requires type array") {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestSchemaUniqueValidationSelectsValues(t *testing.T) {
	for name, testCase := range map[string]struct {
		values     []any
		selectPath string
		wantPath   string
	}{
		"unique scalars":           {values: []any{testOneValue, "two"}},
		"duplicate scalar":         {values: []any{testOneValue, testOneValue}, wantPath: "1"},
		"duplicate selected field": {values: []any{map[string]any{testNameKey: testOneValue}, map[string]any{testNameKey: testOneValue}}, selectPath: testNameKey, wantPath: "1.name"},
	} {
		t.Run(name, func(t *testing.T) {
			validation := map[string]any{functionField: uniqueFunction}
			if testCase.selectPath != "" {
				validation[selectField] = testCase.selectPath
			}
			violations := compileAndValidate(t, validationArraySchema(validation), testCase.values)
			if testCase.wantPath == "" && len(violations) != 0 {
				t.Fatalf("violations = %#v", violations)
			}
			if testCase.wantPath != "" && (len(violations) != 1 || violations[0].Path != testCase.wantPath) {
				t.Fatalf("violations = %#v", violations)
			}
		})
	}
}

func TestSchemaCIDRValidations(t *testing.T) {
	validation := map[string]any{functionField: cidrFunction, versionField: float64(4), "scope": "private", "canonical": true}
	for name, testCase := range map[string]struct {
		cidr  string
		valid bool
	}{
		"ten": {cidr: "10.40.1.0/24", valid: true}, "one seventy": {cidr: "172.20.0.0/16", valid: true},
		"one ninety": {cidr: "192.168.2.0/24", valid: true}, "malformed": {cidr: "10.999.0.0/24"},
		"public": {cidr: "203.0.113.0/24"}, "noncanonical": {cidr: "10.40.1.1/24"}, "wrong version": {cidr: "fd00::/64"},
	} {
		t.Run(name, func(t *testing.T) {
			violations := compileAndValidate(t, validationArraySchema(validation), []any{testCase.cidr})
			if testCase.valid && len(violations) != 0 || !testCase.valid && (len(violations) != 1 || violations[0].Path != "0") {
				t.Fatalf("violations = %#v", violations)
			}
		})
	}

	disjoint := validationArraySchema(map[string]any{functionField: cidrDisjointFunction})
	if violations := compileAndValidate(t, disjoint, []any{"10.40.0.0/16", "10.41.0.0/16"}); len(violations) != 0 {
		t.Fatalf("disjoint violations = %#v", violations)
	}
	violations := compileAndValidate(t, disjoint, []any{"10.40.0.0/16", "10.40.2.0/24"})
	if len(violations) != 1 || violations[0].Path != "1" || !strings.Contains(violations[0].Message, "0") {
		t.Fatalf("overlap violations = %#v", violations)
	}
}

func TestSchemaReferenceValidationUsesRootCollection(t *testing.T) {
	validation := map[string]any{functionField: referencesFunction, "target": testCatalogKey, "target_select": testNameKey}
	schema := map[string]any{testTypeKey: testObjectType, testPropertiesKey: map[string]any{
		testCatalogKey: map[string]any{testTypeKey: testArrayType}, "references": validationArraySchema(validation),
	}}
	values := map[string]any{testCatalogKey: []any{map[string]any{testNameKey: testOneValue}}, "references": []any{testOneValue}}
	if violations := compileAndValidate(t, schema, values); len(violations) != 0 {
		t.Fatalf("valid reference violations = %#v", violations)
	}
	values["references"] = []any{"missing"}
	violations := compileAndValidate(t, schema, values)
	if len(violations) != 1 || violations[0].Path != "references.0" {
		t.Fatalf("unknown reference violations = %#v", violations)
	}
}

func compileAndValidate(t *testing.T, schema map[string]any, value any) []inframill.SchemaViolation {
	t.Helper()
	validator, err := (Compiler{}).Compile(map[string]map[string]any{testSchemaName: schema})
	if err != nil {
		t.Fatal(err)
	}
	values, ok := value.(map[string]any)
	if !ok {
		values = map[string]any{testValueKey: value}
		wrapper := map[string]any{testTypeKey: testObjectType, testPropertiesKey: map[string]any{testValueKey: schema}, testRequiredKey: []any{testValueKey}}
		validator, err = (Compiler{}).Compile(map[string]map[string]any{testSchemaName: wrapper})
		if err != nil {
			t.Fatal(err)
		}
		violations := validator.Validate(testSchemaName, values)
		for index := range violations {
			violations[index].Path = strings.TrimPrefix(violations[index].Path, testValueKey+".")
			if violations[index].Path == testValueKey {
				violations[index].Path = ""
			}
		}
		return violations
	}
	return validator.Validate(testSchemaName, values)
}

func validationArraySchema(validation map[string]any) map[string]any {
	return map[string]any{testTypeKey: testArrayType, validationsKey: []any{validation}}
}
