package harness_test

import (
	"encoding/json"
	"testing"

	"github.com/sir-hassan/inframill/pkg/harness"
)

func TestSettingsAreObjectsAndDecodeStrictly(t *testing.T) {
	type target struct {
		Enabled bool `json:"enabled"`
	}

	var zero harness.Settings
	var decoded target
	if err := zero.Decode(&decoded); err != nil {
		t.Fatalf("decode zero settings: %v", err)
	}
	settings, err := harness.NewSettings(map[string]any{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.Decode(&decoded); err != nil || !decoded.Enabled {
		t.Fatalf("decode settings = %#v, %v", decoded, err)
	}
	unknown, err := harness.NewSettings(map[string]any{"typo": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := unknown.Decode(&decoded); err == nil {
		t.Fatal("unknown settings field error = nil")
	}

	for _, input := range []string{"null", `"value"`, "[]", "1"} {
		var invalid harness.Settings
		if err := json.Unmarshal([]byte(input), &invalid); err == nil {
			t.Errorf("unmarshal %s error = nil", input)
		}
	}
}

func TestSettingsCopyInputAndMarshalOutput(t *testing.T) {
	input := map[string]any{"name": "original"}
	settings, err := harness.NewSettings(input)
	if err != nil {
		t.Fatal(err)
	}
	input["name"] = "changed"
	data, err := settings.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	data[0] = '['
	var decoded struct {
		Name string `json:"name"`
	}
	if err := settings.Decode(&decoded); err != nil || decoded.Name != "original" {
		t.Fatalf("settings changed through caller memory: %#v, %v", decoded, err)
	}
}

func TestConfigUsesLowerCamelCaseSerializedShape(t *testing.T) {
	var config harness.Config
	err := json.Unmarshal([]byte(`{
		"main":{"port":8080},
		"logger":{"type":"test","settings":{}},
		"handlers":[{"type":"http","settings":{"path":"/api","enabled":true}}],
		"workers":[{"type":"worker","settings":{}}]
	}`), &config)
	if err != nil {
		t.Fatal(err)
	}
	if config.Main.Port != 8080 || config.Logger.Type != "test" || config.Handlers[0].Type != "http" || config.Workers[0].Type != "worker" {
		t.Fatalf("config = %#v", config)
	}
}
