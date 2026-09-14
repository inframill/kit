package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Settings stores an opaque copied JSON object decoded by a component factory.
type Settings struct {
	raw json.RawMessage
}

// NewSettings copies value into an opaque JSON object.
func NewSettings(value any) (Settings, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return Settings{}, fmt.Errorf("marshal settings: %w", err)
	}
	var settings Settings
	if err := settings.UnmarshalJSON(data); err != nil {
		return Settings{}, err
	}

	return settings, nil
}

// Decode strictly decodes the settings object into target.
func (settings Settings) Decode(target any) error {
	data := settings.raw
	if len(data) == 0 {
		data = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode settings: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("decode settings: multiple JSON values")
	}

	return nil
}

// MarshalJSON returns a copy of the normalized settings object.
func (settings Settings) MarshalJSON() ([]byte, error) {
	if len(settings.raw) == 0 {
		return []byte("{}"), nil
	}

	return append([]byte(nil), settings.raw...), nil
}

// UnmarshalJSON validates and copies a JSON settings object.
func (settings *Settings) UnmarshalJSON(data []byte) error {
	if settings == nil {
		return errors.New("unmarshal settings into nil receiver")
	}
	var object map[string]json.RawMessage
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("settings must be a JSON object")
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("settings must be a JSON object: %w", err)
	}
	if object == nil || bytes.TrimSpace(data)[0] != '{' {
		return errors.New("settings must be a JSON object")
	}
	settings.raw = append(settings.raw[:0], bytes.TrimSpace(data)...)

	return nil
}
