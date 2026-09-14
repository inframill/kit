package driver

import "time"

type SafetyClass string

const (
	ReadOnly    SafetyClass = "read_only"
	Reversible  SafetyClass = "reversible"
	Mutating    SafetyClass = "mutating"
	Destructive SafetyClass = "destructive"
)

type Status string

const (
	Connected Status = "connected"
	Offline   Status = "offline"
)

// Entry is an independent snapshot of the last info probe, not a live connection.
// Info is absent when offline. Reason is nonempty when offline.
type Entry struct {
	ID         string     `json:"id"`
	Executable Executable `json:"executable"`
	Status     Status     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	Info       *Info      `json:"info,omitempty"`
	CheckedAt  time.Time  `json:"checkedAt"`
}

// Binding describes an immutable Instance identity to an executable.
type Binding struct {
	InstanceID         string `json:"instanceID"`
	Name               string `json:"name"`
	Blueprint          string `json:"blueprint"`
	BlueprintReference string `json:"blueprintReference"`
	Digest             string `json:"digest"`
}

// ValidationRequest validates the Blueprint drivers section, rendered files and Values.
// Binding may be omitted when validating a Blueprint without an Instance.
type ValidationRequest struct {
	Drivers            map[string]Config
	BlueprintDirectory string
	Binding            *Binding
	Values             map[string]any
}
