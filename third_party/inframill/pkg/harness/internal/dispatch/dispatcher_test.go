package dispatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDispatcherSealsOnceAndUsesLongestSegmentPrefix(t *testing.T) {
	dispatcher := New()
	unsealed := httptest.NewRecorder()
	dispatcher.ServeHTTP(unsealed, httptest.NewRequest(http.MethodGet, "/api", nil))
	if unsealed.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsealed status = %d", unsealed.Code)
	}
	handler := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Handler", name)
			writer.Header().Set("X-Path", request.URL.Path)
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	if !dispatcher.Seal([]Route{
		{Path: "/", Handler: handler("root")},
		{Path: "/api", Handler: handler("api")},
		{Path: "/api/connect", Handler: handler("connect")},
	}) {
		t.Fatal("initial seal failed")
	}
	if dispatcher.Seal(nil) {
		t.Fatal("second seal succeeded")
	}
	for _, test := range []struct{ path, handler string }{
		{path: "/api/connect/service/Call", handler: "connect"},
		{path: "/api/connectivity", handler: "api"},
		{path: "/anything", handler: "root"},
	} {
		response := httptest.NewRecorder()
		dispatcher.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Header().Get("X-Handler") != test.handler || response.Header().Get("X-Path") != test.path {
			t.Errorf("route %q = handler %q path %q", test.path, response.Header().Get("X-Handler"), response.Header().Get("X-Path"))
		}
	}
}

func TestDispatcherBoundsSynchronousForwarding(t *testing.T) {
	dispatcher := New()
	if !dispatcher.Seal([]Route{{Path: "/", Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		dispatcher.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), struct{}{}, "preserved")))
	})}}) {
		t.Fatal("seal failed")
	}
	response := httptest.NewRecorder()
	dispatcher.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusLoopDetected {
		t.Fatalf("loop status = %d", response.Code)
	}
}
