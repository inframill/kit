package connectadapter_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sir-hassan/inframill/internal/connectadapter"
	"github.com/sir-hassan/inframill/pkg/harness"
)

// SPEC-0000 REQ-014: the factory mounts the injected engine without authentication.
func TestConfiguredEngineHandler(t *testing.T) {
	engine := newEngine(t, t.TempDir(), newSystem())
	factory := connectadapter.NewHandlerFactories(engine)[connectadapter.ConnectEngineType]
	for _, input := range []string{`{}`, `{"path":"/wrong"}`, `{"path":"/inframill.v1.EngineService/","rootToken":"unsupported"}`} {
		var settings harness.Settings
		if err := settings.UnmarshalJSON([]byte(input)); err != nil {
			t.Fatal(err)
		}
		config := harness.HandlerConfig{Type: connectadapter.ConnectEngineType, Settings: settings}
		if _, violations := factory.Validate(t.Context(), config); len(violations) == 0 {
			t.Fatalf("accepted %s", input)
		}
	}
	settings, err := harness.NewSettings(map[string]any{"path": "/inframill.v1.EngineService/"})
	if err != nil {
		t.Fatal(err)
	}
	config := harness.HandlerConfig{Type: connectadapter.ConnectEngineType, Settings: settings}
	path, violations := factory.Validate(t.Context(), config)
	if path != "/inframill.v1.EngineService/" || len(violations) != 0 {
		t.Fatal(path, violations)
	}
	handler, err := factory.Create(t.Context(), nil, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path+"ListCommandDefinitions", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Body.String(), testDriver) {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}
