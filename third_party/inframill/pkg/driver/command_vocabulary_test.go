package driver

import (
	"strings"
	"testing"
)

// SPEC-0012 REQ-001/002: only current protocol and command fields are accepted.
func TestREQ002CommandProtocolVocabulary(t *testing.T) {
	for _, document := range []string{
		strings.Replace(fixtureInfo, `"protocol":1`, `"protocol":2`, 1),
		strings.Replace(fixtureInfo, `"commands":`, `"operations":`, 1),
		strings.Replace(fixtureInfo, `"commands":`, `"operations":{},"commands":`, 1),
	} {
		path := executableFixture(t, printInfo(document), "exit 99")
		manager, err := New(t.Context(), Configurations{"invalid": {Path: path}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		entries := listEntries(t, manager)
		if len(entries) != 1 || entries[0].Status != Offline {
			t.Fatal("invalid metadata accepted", entries)
		}
		if _, err := manager.Prepare(t.Context(), CommandRequest{DriverID: "invalid", Command: "echo", Scope: testScope(t)}); err == nil {
			t.Fatal("offline command prepared")
		}
	}
	for _, document := range []string{
		`{"protocol":1,"config":{},"operation":"echo"}`,
		`{"protocol":1,"config":{},"operation":"echo","command":"echo"}`,
	} {
		var request Request
		if err := DecodeRequest([]byte(document), &request); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}
