package runmill

import (
	"strings"
	"testing"
)

// SPEC-0000 REQ-002/008: discovery and invocation use the same resource name grammar.
func TestResourceNameCharacters(t *testing.T) {
	for _, name := range []string{"Z", "0", "azAZ09-", "-Provider-", "---", strings.Repeat("A", 1024)} {
		info := ProviderInfo{Name: name, Version: "1.0.0", ProtocolVersion: 1, Commands: map[string]CommandDefinition{name: {Description: "fixture", AccessMode: "read_write"}}}
		if err := info.Validate(); err != nil {
			t.Fatal(name, err)
		}
		request := invocationParams{settingsParams: settingsParams{Settings: Settings{}}, Command: name, WorkspaceDir: "workspace", ArtifactDir: "artifacts", ExecutionID: "opaque"}
		if err := request.validate(); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, name := range []string{"", "a_b", ".", "..", "a.b", "a/b", `a\b`, " a", "a ", "a\n", "é"} {
		info := ProviderInfo{Name: "Provider", Version: "1.0.0", ProtocolVersion: 1, Commands: map[string]CommandDefinition{name: {Description: "fixture", AccessMode: "read_write"}}}
		if err := info.Validate(); err == nil {
			t.Fatalf("accepted command %q", name)
		}
		request := invocationParams{settingsParams: settingsParams{Settings: Settings{}}, Command: name, WorkspaceDir: "workspace", ArtifactDir: "artifacts", ExecutionID: "opaque"}
		if err := request.validate(); err == nil {
			t.Fatalf("accepted invocation %q", name)
		}
	}
}

// SPEC-0000 REQ-002/008: Host preserves expanded configured, reported and command names.
func TestExpandedNamesThroughHost(t *testing.T) {
	document := strings.ReplaceAll(strings.ReplaceAll(fixtureInfo, "custom", "-Reported-"), "echo", "-Command-")
	path := executableFixture(t, printInfo(document), "cat")
	host, err := newLoadedHost(t.Context(), map[string]Executable{"-Provider-": {Path: path}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := listEntries(t, host)
	if len(entries) != 1 || entries[0].Availability != Available || entries[0].Info.Name != "-Reported-" {
		t.Fatal(entries)
	}
	prepared := Execution{ProviderID: "-Provider-", Command: "-Command-", Context: testScope(t)}

	result, err := host.Execute(t.Context(), prepared, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requireType[map[string]any](t, result)["command"] != "-Command-" {
		t.Fatal(result)
	}
}
