package schema

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	validationsKey       = "x-inframill-validations"
	functionField        = "function"
	selectField          = "select"
	versionField         = "version"
	uniqueFunction       = "unique"
	cidrFunction         = "cidr"
	cidrDisjointFunction = "cidr_disjoint"
	referencesFunction   = "references"
)

var selectorPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*(\.[A-Za-z0-9_][A-Za-z0-9_-]*)*$`)

type validationDeclaration struct {
	Function     string
	Select       string
	Target       string
	TargetSelect string
	Version      int
	Scope        string
	Canonical    bool
}

type selectedValue struct {
	Value any
	Path  string
}

func validateExtensions(schemaRoot map[string]any, valueRoot any, schema map[string]any, value any, path string, result *[]inframill.SchemaViolation) {
	if reference, ok := schema["$ref"].(string); ok {
		resolved, err := resolveLocalReference(schemaRoot, reference)
		if err != nil {
			return
		}
		merged := make(map[string]any, len(resolved)+len(schema)-1)
		maps.Copy(merged, resolved)
		for key, item := range schema {
			if key != "$ref" {
				merged[key] = item
			}
		}
		schema = merged
	}
	if _, declared := schema[validationsKey]; declared {
		if expected, specified := schema["type"].(string); !specified || expected != "array" {
			addViolation(result, path, "Blueprint schema "+validationsKey+" requires type array")
		}
	}
	if array, ok := value.([]any); ok {
		if itemSchema, itemOK := schema["items"].(map[string]any); itemOK {
			for index, item := range array {
				validateExtensions(schemaRoot, valueRoot, itemSchema, item, joinPath(path, strconv.Itoa(index)), result)
			}
		}
		validateFunctions(valueRoot, array, schema, path, result)
	}
	if object, ok := value.(map[string]any); ok {
		properties := map[string]any{}
		if declared, ok := schema["properties"].(map[string]any); ok {
			properties = declared
		}
		var additional map[string]any
		if declared, ok := schema["additionalProperties"].(map[string]any); ok {
			additional = declared
		}
		for _, key := range sortedKeys(object) {
			childSchema, declared := properties[key].(map[string]any)
			if !declared {
				childSchema = additional
			}
			if childSchema != nil {
				validateExtensions(schemaRoot, valueRoot, childSchema, object[key], joinPath(path, key), result)
			}
		}
	}
}

func validateFunctions(valueRoot any, values []any, schema map[string]any, path string, result *[]inframill.SchemaViolation) {
	raw, declared := schema[validationsKey]
	if !declared {
		return
	}
	validations, ok := raw.([]any)
	if !ok {
		addViolation(result, path, "Blueprint schema "+validationsKey+" must be an array")
		return
	}
	for index, rawValidation := range validations {
		declaration, err := decodeValidation(rawValidation)
		if err != nil {
			addViolation(result, path, fmt.Sprintf("Blueprint schema %s.%d: %v", validationsKey, index, err))
			continue
		}
		selected := selectValues(values, declaration.Select, path, result)
		switch declaration.Function {
		case uniqueFunction:
			validateUnique(selected, declaration.Select, result)
		case cidrFunction:
			validateCIDRs(selected, declaration, result)
		case cidrDisjointFunction:
			validateDisjointCIDRs(selected, result)
		case referencesFunction:
			validateReferences(valueRoot, selected, declaration, path, result)
		}
	}
}

func decodeValidation(value any) (validationDeclaration, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return validationDeclaration{}, errors.New("validation must be an object")
	}
	function, ok := object[functionField].(string)
	if !ok || function == "" {
		return validationDeclaration{}, errors.New("function must be a non-empty string")
	}
	allowed := map[string]map[string]bool{
		uniqueFunction:       {functionField: true, selectField: true},
		cidrFunction:         {functionField: true, selectField: true, versionField: true, "scope": true, "canonical": true},
		cidrDisjointFunction: {functionField: true, selectField: true},
		referencesFunction:   {functionField: true, selectField: true, "target": true, "target_select": true},
	}
	fields, known := allowed[function]
	if !known {
		return validationDeclaration{}, fmt.Errorf("unsupported function %q", function)
	}
	for _, key := range sortedKeys(object) {
		if !fields[key] {
			return validationDeclaration{}, fmt.Errorf("function %q does not support field %q", function, key)
		}
	}

	declaration := validationDeclaration{Function: function}
	var err error
	if declaration.Select, err = optionalSelector(object, selectField); err != nil {
		return validationDeclaration{}, err
	}
	if function == cidrFunction {
		version, exists := schemaNumber(object[versionField])
		if !exists || version != 4 {
			return validationDeclaration{}, fmt.Errorf("function %q version must equal 4", function)
		}
		declaration.Version = version
		if rawScope, exists := object["scope"]; exists {
			scope, scopeOK := rawScope.(string)
			if !scopeOK || scope != "private" {
				return validationDeclaration{}, fmt.Errorf("function %q scope must equal %q", function, "private")
			}
			declaration.Scope = scope
		}
		if rawCanonical, exists := object["canonical"]; exists {
			canonical, canonicalOK := rawCanonical.(bool)
			if !canonicalOK {
				return validationDeclaration{}, fmt.Errorf("function %q canonical must be a boolean", function)
			}
			declaration.Canonical = canonical
		}
	}
	if function == referencesFunction {
		target, exists := object["target"].(string)
		if !exists || !validSelector(target) {
			return validationDeclaration{}, fmt.Errorf("function %q target must be a dotted root path", function)
		}
		declaration.Target = target
		if declaration.TargetSelect, err = optionalSelector(object, "target_select"); err != nil {
			return validationDeclaration{}, err
		}
	}
	return declaration, nil
}

func optionalSelector(object map[string]any, field string) (string, error) {
	raw, exists := object[field]
	if !exists {
		return "", nil
	}
	selector, ok := raw.(string)
	if !ok || !validSelector(selector) {
		return "", fmt.Errorf("%s must be a non-empty dotted field path", field)
	}
	return selector, nil
}

func validSelector(selector string) bool {
	return selectorPattern.MatchString(selector)
}

func selectValues(values []any, selector, path string, result *[]inframill.SchemaViolation) []selectedValue {
	selected := make([]selectedValue, 0, len(values))
	for index, value := range values {
		valuePath := joinPath(path, strconv.Itoa(index))
		if selector == "" {
			selected = append(selected, selectedValue{Value: value, Path: valuePath})
			continue
		}
		resolved, found := lookupValue(value, selector)
		selectedPath := joinPath(valuePath, selector)
		if !found {
			addViolation(result, selectedPath, fmt.Sprintf("validation select %q does not resolve", selector))
			continue
		}
		selected = append(selected, selectedValue{Value: resolved, Path: selectedPath})
	}
	return selected
}

func lookupValue(root any, selector string) (any, bool) {
	current := root
	for segment := range strings.SplitSeq(selector, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func validateUnique(values []selectedValue, selector string, result *[]inframill.SchemaViolation) {
	for index, value := range values {
		for previous := range index {
			if reflect.DeepEqual(values[previous].Value, value.Value) {
				message := "must be unique"
				if selector != "" {
					message += " by " + selector
				}
				addViolation(result, value.Path, message)
				break
			}
		}
	}
}

func validateCIDRs(values []selectedValue, declaration validationDeclaration, result *[]inframill.SchemaViolation) {
	for _, value := range values {
		text, ok := value.Value.(string)
		if !ok || !validDeclaredCIDR(text, declaration) {
			addViolation(result, value.Path, declaredCIDRMessage(declaration))
		}
	}
}

func validDeclaredCIDR(value string, declaration validationDeclaration) bool {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || declaration.Version == 4 && !prefix.Addr().Is4() {
		return false
	}
	if declaration.Canonical && prefix != prefix.Masked() {
		return false
	}
	return declaration.Scope != "private" || privateCIDR(prefix.Masked())
}

func privateCIDR(prefix netip.Prefix) bool {
	for _, raw := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		privatePrefix := netip.MustParsePrefix(raw)
		if prefix.Bits() >= privatePrefix.Bits() && privatePrefix.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func declaredCIDRMessage(declaration validationDeclaration) string {
	parts := []string{"must be a"}
	if declaration.Canonical {
		parts = append(parts, "canonical")
	}
	if declaration.Scope != "" {
		parts = append(parts, declaration.Scope)
	}
	if declaration.Version == 4 {
		parts = append(parts, "IPv4")
	}
	return strings.Join(parts, " ") + " CIDR"
}

func validateDisjointCIDRs(values []selectedValue, result *[]inframill.SchemaViolation) {
	prefixes := make([]netip.Prefix, len(values))
	valid := make([]bool, len(values))
	for index, value := range values {
		text, ok := value.Value.(string)
		if !ok {
			addViolation(result, value.Path, "must be a CIDR")
			continue
		}
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			addViolation(result, value.Path, "must be a CIDR")
			continue
		}
		prefixes[index] = prefix.Masked()
		valid[index] = true
	}
	for index, prefix := range prefixes {
		if !valid[index] {
			continue
		}
		for previous := range index {
			if valid[previous] && (prefix.Contains(prefixes[previous].Addr()) || prefixes[previous].Contains(prefix.Addr())) {
				addViolation(result, values[index].Path, "must not overlap "+values[previous].Path)
				break
			}
		}
	}
}

func validateReferences(valueRoot any, values []selectedValue, declaration validationDeclaration, path string, result *[]inframill.SchemaViolation) {
	target, found := lookupValue(valueRoot, declaration.Target)
	if !found {
		addViolation(result, path, fmt.Sprintf("validation target %q does not resolve", declaration.Target))
		return
	}
	targetValues, ok := target.([]any)
	if !ok {
		addViolation(result, path, fmt.Sprintf("validation target %q must resolve to an array", declaration.Target))
		return
	}
	selectedTargets := selectValues(targetValues, declaration.TargetSelect, declaration.Target, result)
	for _, value := range values {
		found := false
		for _, targetValue := range selectedTargets {
			if reflect.DeepEqual(value.Value, targetValue.Value) {
				found = true
				break
			}
		}
		if !found {
			addViolation(result, value.Path, "must reference a value declared at "+declaration.Target)
		}
	}
}

func resolveLocalReference(root map[string]any, reference string) (map[string]any, error) {
	if !strings.HasPrefix(reference, "#/") {
		return nil, fmt.Errorf("unsupported external reference %q", reference)
	}
	var current any = root
	for segment := range strings.SplitSeq(strings.TrimPrefix(reference, "#/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid local reference %q", reference)
		}
		current, ok = object[segment]
		if !ok {
			return nil, fmt.Errorf("unknown local reference %q", reference)
		}
	}
	resolved, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid local reference %q", reference)
	}
	return resolved, nil
}

func schemaNumber(value any) (int, bool) {
	number, ok := value.(float64)
	return int(number), ok
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	if child == "" {
		return parent
	}
	return parent + "." + child
}

func addViolation(result *[]inframill.SchemaViolation, path, message string) {
	*result = append(*result, inframill.SchemaViolation{Path: path, Message: message})
}
