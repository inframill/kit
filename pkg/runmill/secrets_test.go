package runmill

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// SPEC-0000 REQ-009/020/025: maps preserve values and retain strict wire limits.
func TestSecretMapValidation(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `{"a":null}`, `{"a":1}`, `{"a":true}`, `{"a":{}}`, `{"a":[]}`, `{"a":"x","a":"y"}`, `{"A":"x"}`, `{"a/b":"x"}`, `{"1a":"x"}`, `{"a":"x"} {}`, `{"a":"x"`, "{\"a\":\"\xff\"}"} {
		if _, err := decodeSecrets([]byte(input)); err == nil {
			t.Fatal("accepted invalid secrets")
		}
	}
	for _, name := range []string{"a", "a-b_c0", strings.Repeat("a", 128)} {
		want := map[string]string{name: " \n\t$TOKEN\x00", "empty": ""}
		data, err := encodeSecrets(want)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeSecrets(data)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("secret map did not round-trip", err)
		}
		got[name] = "changed"
		if want[name] == "changed" {
			t.Fatal("decoded map aliases caller")
		}
	}
	for _, value := range []string{strings.Repeat("x", maxSecretBytes-len(`{"a":""}`)), strings.Repeat("\n", (maxSecretBytes-len(`{"a":""}`))/2)} {
		input := map[string]string{"a": value}
		data, err := encodeSecrets(input)
		if err != nil || len(data) != maxSecretBytes {
			t.Fatal("exact encoded boundary rejected", err)
		}
		if _, err := decodeSecrets(data); err != nil {
			t.Fatal(err)
		}
		if _, err := decodeSecrets(append(data, ' ')); err == nil {
			t.Fatal("oversized encoded input accepted")
		}
		input["a"] += "x"
		if _, err := encodeSecrets(input); err == nil {
			t.Fatal("oversized map accepted")
		}
	}
}

// SPEC-0000 REQ-009/020/025: invalid caller maps fail before starting any process.
func TestInvalidSecretMapBeforeProcess(t *testing.T) {
	for _, input := range []map[string]string{
		{"bad/name": "synthetic-private-value"},
		{strings.Repeat("a", 129): "synthetic-private-value"},
		{"a": "synthetic-private-value\xff"},
		{"a": strings.Repeat("x", maxSecretBytes)},
	} {
		// No client is installed: any attempt to invoke a process would panic.
		_, err := (&providerClient{}).Execute(t.Context(), testScope(t), "echo", nil, input)
		if err == nil || strings.Contains(err.Error(), "synthetic-private-value") {
			t.Fatal("invalid map was accepted or disclosed")
		}
	}
}

// SPEC-0000 REQ-009/020/025: nil and empty input encode as {}, and Serve owns its map.
func TestEmptySecretMapsExecute(t *testing.T) {
	path := executableFixture(t, printInfo(fixtureInfo), "cat")
	host, err := loadFixture(t.Context(), []string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	command := Execution{ProviderID: "custom", Command: "echo", Context: testScope(t)}

	_, request := servingRequests()
	for _, secrets := range []map[string]string{nil, {}} {
		value, err := host.Execute(t.Context(), command, secrets)
		if err != nil {
			t.Fatal(err)
		}
		got := requireType[map[string]any](t, value)["secrets"]
		if !reflect.DeepEqual(got, map[string]any{}) {
			t.Fatal("empty input did not reach process as object")
		}
		encoded, err := encodeSecrets(secrets)
		if err != nil || !bytes.Equal(encoded, []byte("{}")) {
			t.Fatal("empty map encoding failed", err)
		}
		provider := &servingProvider{}
		code, _, _ := serveFixture(t, provider, "run", runParams{invocationParams: request, Secrets: encoded})
		if code != 0 || provider.secrets == nil || len(provider.secrets) != 0 {
			t.Fatal("Serve did not provide an owned empty map")
		}
	}
}
