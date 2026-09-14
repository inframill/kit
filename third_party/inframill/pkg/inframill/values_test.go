package inframill_test

import (
	"testing"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

func TestJSONSnapshots(t *testing.T) {
	original := inframill.InstanceValues{"nested": map[string]any{"size": 3}, "items": []string{originalValue}}
	copied, err := inframill.Normalize(original)
	if err != nil {
		t.Fatal(err)
	}
	nested, ok := copied["nested"].(map[string]any)
	if !ok {
		t.Fatal("missing object")
	}
	nested["size"] = 4
	preserved, ok := original["nested"].(map[string]any)
	if !ok || preserved["size"] != 3 {
		t.Fatal("copy aliases original")
	}
	clone := inframill.CloneInfo(inframill.InstanceInfo{Values: copied, Failure: &inframill.InstanceFailure{Message: originalValue}})
	clone.Failure.Message = changedValue
	items, ok := clone.Values["items"].([]any)
	if !ok {
		t.Fatal("missing array")
	}
	items[0] = changedValue
	originalItems, ok := copied["items"].([]any)
	if !ok || originalItems[0] != originalValue {
		t.Fatal("snapshot aliases array")
	}
	if _, checkErr := inframill.Normalize(map[string]any{"bad": make(chan int)}); checkErr == nil {
		t.Fatal("accepted channel")
	}
	args, err := inframill.NormalizeArguments(nil)
	if err != nil || args == nil {
		t.Fatal("nil arguments not normalized")
	}
}

const (
	originalValue = "original"
	changedValue  = "changed"
)
