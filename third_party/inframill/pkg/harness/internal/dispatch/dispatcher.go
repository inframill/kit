// Package dispatch implements the immutable Hummus HTTP route table.
package dispatch

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
)

const maxForwardDepth = 16

type forwardDepthKey struct{}

// Route binds one canonical prefix to a handler.
type Route struct {
	Path    string
	Handler http.Handler
}

type routeTable struct {
	routes []Route
}

// Dispatcher is allocated before handler construction and sealed exactly once
// before the application can serve requests.
type Dispatcher struct {
	table atomic.Pointer[routeTable]
}

// New creates an unsealed dispatcher with a stable identity.
func New() *Dispatcher { return &Dispatcher{} }

// Seal installs an immutable copy of routes.
func (dispatcher *Dispatcher) Seal(routes []Route) bool {
	copied := append([]Route(nil), routes...)
	slices.SortStableFunc(copied, func(left, right Route) int {
		return len(right.Path) - len(left.Path)
	})

	return dispatcher.table.CompareAndSwap(nil, &routeTable{routes: copied})
}

// ServeHTTP selects the longest matching route while preserving URL.Path.
func (dispatcher *Dispatcher) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	table := dispatcher.table.Load()
	if table == nil {
		writeError(writer, http.StatusServiceUnavailable, "application is not ready")
		return
	}
	depth, _ := request.Context().Value(forwardDepthKey{}).(int)
	if depth >= maxForwardDepth {
		writeError(writer, http.StatusLoopDetected, "forwarding loop detected")
		return
	}
	request = request.WithContext(context.WithValue(request.Context(), forwardDepthKey{}, depth+1))
	for _, route := range table.routes {
		if routeMatches(route.Path, request.URL.Path) {
			if recorder, ok := writer.(interface{ SetMatchedRoute(route string) }); ok {
				recorder.SetMatchedRoute(route.Path)
			}
			route.Handler.ServeHTTP(writer, request)

			return
		}
	}
	http.NotFound(writer, request)
}

func routeMatches(pattern, requested string) bool {
	if pattern == "/" {
		return true
	}
	base := strings.TrimSuffix(pattern, "/")

	return requested == base || strings.HasPrefix(requested, base+"/")
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": message})
}
