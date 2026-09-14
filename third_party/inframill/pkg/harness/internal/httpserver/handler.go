// Package httpserver owns Hummus's reusable outer HTTP boundary.
package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Level identifies the logger method used for an HTTP lifecycle record.
type Level uint8

const (
	// LevelDebug is used for low-value lifecycle records.
	LevelDebug Level = iota
	// LevelInfo is used for successful requests.
	LevelInfo
	// LevelError is used for server failures and recovered panics.
	LevelError
)

// LogFunc receives alternating field names and values.
type LogFunc func(context.Context, Level, string, ...any)

// New wraps dispatcher with panic recovery, request IDs, and safe request logging.
func New(dispatcher http.Handler, log LogFunc) http.Handler {
	return requestLogging(log, recoverPanics(log, dispatcher))
}

func recoverPanics(log LogFunc, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		//nolint:contextcheck // The deferred recovery logs with the request context.
		defer func() {
			if recover() != nil {
				log(request.Context(), LevelError, "recovered HTTP panic",
					"event", "http.request.panicked", "component", "http")
				writeError(writer, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func requestLogging(log LogFunc, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestID := validRequestID(request.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		writer.Header().Set("X-Request-ID", requestID)
		recorder := &responseRecorder{ResponseWriter: writer, status: http.StatusOK, route: "unmatched"}
		next.ServeHTTP(recorder, request)
		level := LevelInfo
		if recorder.status >= http.StatusInternalServerError {
			level = LevelError
		}
		log(request.Context(), level, "HTTP request completed",
			"event", "http.request.completed", "component", "http", "request_id", requestID,
			"method", request.Method, "route", recorder.route, "status", recorder.status,
			"duration_ms", time.Since(startedAt).Milliseconds(), "response_bytes", recorder.bytes,
		)
	})
}

type responseRecorder struct {
	http.ResponseWriter

	status int
	bytes  int
	route  string
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.status != http.StatusOK || status == http.StatusOK {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(data []byte) (int, error) {
	written, err := recorder.ResponseWriter.Write(data)
	recorder.bytes += written

	return written, err
}

// Unwrap lets http.ResponseController reach optional interfaces on the writer.
func (recorder *responseRecorder) Unwrap() http.ResponseWriter { return recorder.ResponseWriter }

// SetMatchedRoute records the configured route without logging the raw URL.
func (recorder *responseRecorder) SetMatchedRoute(route string) { recorder.route = route }

func validRequestID(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return ""
		}
	}

	return value
}

func newRequestID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err == nil {
		return hex.EncodeToString(bytes)
	}

	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": message})
}
