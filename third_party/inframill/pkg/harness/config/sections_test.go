package config_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sir-hassan/inframill/pkg/harness"
	harnessconfig "github.com/sir-hassan/inframill/pkg/harness/config"
)

func TestYAMLFileLoadsApplicationSections(t *testing.T) {
	path := writeYAML(t, minimalYAML(8080)+`
mail:
  host: smtp.example.test
  port: 587
  enabled: true
  ratio: 0.5
  optional: null
  nested:
    token: ${SECRET}
  items:
    - token: ${SECRET}
    - $${LITERAL}
billing: {}
`)
	names := []string{"mail", "billing", "absent", "billing"}
	option := harnessconfig.WithAllowedSections(names...)
	names[0] = "changed"
	environment := []string{"SECRET=original"}
	loader := harnessconfig.NewYAMLFile(path, option, harnessconfig.WithEnvironment(environment))
	environment[0] = "SECRET=changed"
	want := map[string]any{
		"mail": map[string]any{
			"host": "smtp.example.test", "port": 587, "enabled": true, "ratio": 0.5,
			"optional": nil,
			"nested":   map[string]any{"token": "original"},
			"items":    []any{map[string]any{"token": "original"}, "${LITERAL}"},
		},
		"billing": map[string]any{},
	}
	first, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Sections, want) {
		t.Fatalf("sections = %#v, want %#v", first.Sections, want)
	}
	mail := first.Sections["mail"].(map[string]any)
	mail["nested"].(map[string]any)["token"] = "mutated"
	mail["items"].([]any)[0].(map[string]any)["token"] = "mutated"
	delete(first.Sections, "billing")
	second, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.Sections, want) {
		t.Fatalf("second sections = %#v, want %#v", second.Sections, want)
	}

	omitted, err := harnessconfig.NewYAMLFile(writeYAML(t, minimalYAML(8080)), option).Load(context.Background())
	if err != nil || len(omitted.Sections) != 0 {
		t.Fatalf("omitted sections = %#v, error = %v", omitted.Sections, err)
	}
}

func TestYAMLFileRejectsInvalidApplicationSections(t *testing.T) {
	tests := []struct {
		name    string
		content string
		allowed []string
		path    string
		code    string
	}{
		{name: "no allowlist", content: "mail: {}", path: "mail"},
		{name: "unlisted", content: "billing: {}", allowed: []string{"mail"}, path: "billing"},
		{name: "exact name", content: "Mail: {}", allowed: []string{"mail"}, path: "Mail"},
		{name: "scalar", content: "mail: super-secret", allowed: []string{"mail"}, path: "mail"},
		{name: "sequence", content: "mail: []", allowed: []string{"mail"}, path: "mail"},
		{name: "null", content: "mail: null", allowed: []string{"mail"}, path: "mail"},
		{name: "duplicate section", content: "mail: {}\nmail: {}", allowed: []string{"mail"}, path: "mail", code: "duplicate"},
		{name: "duplicate nested key", content: "mail:\n  token: super-secret\n  token: other", allowed: []string{"mail"}, path: "mail.token", code: "duplicate"},
		{name: "numeric key", content: "mail:\n  1: super-secret", allowed: []string{"mail"}, path: "mail"},
		{name: "blank key", content: "mail:\n  ' ': super-secret", allowed: []string{"mail"}, path: "mail"},
		{name: "nested numeric key", content: "mail:\n  nested:\n    1: super-secret", allowed: []string{"mail"}, path: "mail.nested"},
		{name: "alias", content: "mail:\n  first: &secret super-secret\n  second: *secret", allowed: []string{"mail"}, path: "mail.second"},
		{name: "missing environment", content: "mail:\n  token: ${MISSING}", allowed: []string{"mail"}, path: "mail.token"},
		{name: "partial environment", content: "mail:\n  token: prefix-${SECRET}", allowed: []string{"mail"}, path: "mail.token"},
		{name: "sequence environment", content: "mail:\n  items:\n    - token: ${MISSING}", allowed: []string{"mail"}, path: "mail.items[0].token"},
		{name: "builtin stays strict", content: "main:\n  unknown: super-secret\nmail: {}", allowed: []string{"mail"}, path: "main.unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loader := harnessconfig.NewYAMLFile(writeYAML(t, test.content),
				harnessconfig.WithAllowedSections(test.allowed...),
				harnessconfig.WithEnvironment([]string{"SECRET=super-secret"}),
			)
			loaded, err := loader.Load(context.Background())
			var validation *harness.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("Load error = %T %v", err, err)
			}
			code := test.code
			if code == "" {
				code = "invalid"
			}
			violations := validation.Violations()
			if len(violations) != 1 || violations[0].Path != test.path || violations[0].Code != code {
				t.Fatalf("violations = %#v, want %s (%s)", violations, test.path, code)
			}
			if strings.Contains(err.Error(), "super-secret") {
				t.Fatalf("error exposes configuration value: %v", err)
			}
			if !reflect.DeepEqual(loaded, harness.Config{}) {
				t.Fatal("invalid load returned a partial configuration")
			}
		})
	}
}

func TestYAMLFileRejectsInvalidAllowedSectionNames(t *testing.T) {
	for _, name := range []string{"", " ", "main", "logger", "handlers", "workers"} {
		t.Run(name, func(t *testing.T) {
			loader := harnessconfig.NewYAMLFile(writeYAML(t, "{}"), harnessconfig.WithAllowedSections("mail", name))
			_, err := loader.Load(context.Background())
			var validation *harness.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("Load error = %T %v", err, err)
			}
			violations := validation.Violations()
			if len(violations) != 1 || violations[0].Path != "allowedSections[1]" {
				t.Fatalf("violations = %#v", violations)
			}
		})
	}
}

func TestYAMLFileLastAllowedSectionsOptionWins(t *testing.T) {
	loader := harnessconfig.NewYAMLFile(writeYAML(t, "mail: {}"),
		harnessconfig.WithAllowedSections("mail"), harnessconfig.WithAllowedSections(),
	)
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatal("empty final allowlist accepted a section from the earlier option")
	}
}
