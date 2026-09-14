package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerRecoversAndLogsOnlySafeRequestMetadata(t *testing.T) {
	var logged bytes.Buffer
	log := func(_ context.Context, level Level, message string, fields ...any) {
		_, _ = fmt.Fprintf(&logged, "%d %s %v", level, message, fields)
	}
	handler := New(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if recorder, ok := writer.(interface{ SetMatchedRoute(route string) }); ok {
			recorder.SetMatchedRoute("/safe")
		}
		panic("secret panic value")
	}), log)
	request := httptest.NewRequest(http.MethodPost, "/safe?token=query-secret", bytes.NewBufferString("body-secret"))
	request.Header.Set("Authorization", "Bearer header-secret")
	request.Header.Set("X-Root-Password", "root-secret")
	request.Header.Set("X-Request-ID", "request-7")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || response.Header().Get("X-Request-ID") != "request-7" {
		t.Fatalf("response status=%d request-id=%q", response.Code, response.Header().Get("X-Request-ID"))
	}
	output := logged.String()
	for _, expected := range []string{"http.request.panicked", "http.request.completed", "request-7", "/safe", "500"} {
		if !bytes.Contains([]byte(output), []byte(expected)) {
			t.Errorf("log %q does not contain %q", output, expected)
		}
	}
	for _, secret := range []string{"query-secret", "body-secret", "header-secret", "root-secret", "secret panic value"} {
		if bytes.Contains([]byte(output), []byte(secret)) {
			t.Errorf("log contains secret %q", secret)
		}
	}
}
