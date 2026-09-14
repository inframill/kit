package inframill

import (
	"encoding/json"
	"reflect"
)

// Normalize validates JSON data and creates an independent normalized snapshot.
func Normalize[T any](value T) (T, error) {
	var result T

	content, err := json.Marshal(value)
	if err != nil {
		return result, err
	}

	err = json.Unmarshal(content, &result)
	return result, err
}

func IsNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	//nolint:exhaustive // Only kinds that can be nil need special treatment.
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func CloneJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return CloneObject(v)
	case []any:
		if v == nil {
			return []any(nil)
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = CloneJSON(item)
		}
		return out
	default:
		return value
	}
}

func CloneObject(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for k, v := range value {
		out[k] = CloneJSON(v)
	}
	return out
}

func CloneInfo(info InstanceInfo) InstanceInfo {
	info.Values = CloneObject(info.Values)
	if info.Failure != nil {
		f := *info.Failure
		info.Failure = &f
	}
	return info
}

func NormalizeArguments(args Arguments) (Arguments, error) {
	if args == nil {
		return Arguments{}, nil
	}
	return Normalize(args)
}

// CloneCommandDefinition returns an independent snapshot of a normalized definition.
func CloneCommandDefinition(d CommandDefinition) CommandDefinition {
	d.ArgumentsSchema = CloneObject(d.ArgumentsSchema)
	d.Effects = append([]string(nil), d.Effects...)
	return d
}
