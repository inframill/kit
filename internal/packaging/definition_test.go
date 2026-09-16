package packaging

import (
	"strings"
	"testing"
)

const validDefinition = `api_version: inframill.dev/blueprint/v1
description: fixture
values_schema: {type: object}
driver_settings: {fixture: {}}
`

// SPEC-0007: authoring rejects definitions the released consumer cannot load.
func TestBlueprintDefinitionStructure(t *testing.T) {
	if _, err := parseDefinition([]byte(validDefinition)); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"old root":               strings.ReplaceAll(validDefinition, "driver_settings:", "drivers:"),
		"missing schema":         strings.ReplaceAll(validDefinition, "values_schema: {type: object}\n", ""),
		"null schema":            strings.ReplaceAll(validDefinition, "{type: object}", "null"),
		"null settings":          strings.ReplaceAll(validDefinition, "fixture: {}", "fixture: null"),
		"no drivers":             strings.ReplaceAll(validDefinition, "{fixture: {}}", "{}"),
		"reserved driver":        strings.ReplaceAll(validDefinition, "fixture: {}", "builtin: {}"),
		"duplicate root":         validDefinition + "description: other\n",
		"duplicate nested":       strings.ReplaceAll(validDefinition, "{type: object}", "{type: object, type: string}"),
		"unknown field":          validDefinition + "config_bindings: {}\n",
		"multiple documents":     validDefinition + "---\n{}\n",
		"alias":                  strings.ReplaceAll(validDefinition, "{fixture: {}}", "{fixture: &s {}, other: *s}"),
		"merge":                  strings.ReplaceAll(validDefinition, "{fixture: {}}", "{fixture: {<<: {}}}"),
		"custom tag":             strings.ReplaceAll(validDefinition, "{fixture: {}}", "!custom {fixture: {}}"),
		"nonfinite":              strings.ReplaceAll(validDefinition, "fixture: {}", "fixture: {x: .nan}"),
		"non-string key":         strings.ReplaceAll(validDefinition, "fixture: {}", "fixture: {1: true}"),
		"empty description":      strings.ReplaceAll(validDefinition, "description: fixture", "description: ''"),
		"non-string description": strings.ReplaceAll(validDefinition, "description: fixture", "description: 42"),
		"oversized":              validDefinition + strings.Repeat(" ", MaxBytes),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDefinition([]byte(data)); err == nil {
				t.Fatal("accepted malformed definition")
			}
		})
	}
}
