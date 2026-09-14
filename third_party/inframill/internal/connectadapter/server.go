// Package connectadapter translates the generated RPC contract to the Engine.
package connectadapter

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

// Handler exposes only generated RPC routes.
func Handler(engine *inframill.Engine) http.Handler {
	mux := http.NewServeMux()
	path, handler := inframillv1connect.NewEngineServiceHandler(&service{engine: engine}, connect.WithReadMaxBytes(1<<20))
	mux.Handle(path, handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

type service struct{ engine *inframill.Engine }

func rpcError(err error) error {
	if err == nil {
		return nil
	}
	if typed, ok := errors.AsType[*connect.Error](err); ok {
		return typed
	}
	code := connect.CodeInternal
	switch {
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	case errors.Is(err, inframill.ErrNotStarted), errors.Is(err, inframill.ErrInstanceFailed):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, inframill.ErrNotFound):
		code = connect.CodeNotFound
	case errors.Is(err, inframill.ErrConflict), errors.Is(err, inframill.ErrAlreadyExists):
		code = connect.CodeAlreadyExists
	case errors.Is(err, inframill.ErrQueueFull), errors.Is(err, inframill.ErrClosed):
		code = connect.CodeUnavailable
	default:
		if typed, ok := errors.AsType[*inframill.Error](err); ok {
			switch typed.Code {
			case inframill.InvalidInstanceID, inframill.InvalidRequest, inframill.InvalidConfig:
				code = connect.CodeInvalidArgument
			case inframill.BlueprintNotFound:
				code = connect.CodeNotFound
			case inframill.CommandInterrupted, inframill.CommandFailed, inframill.VerificationFailed, inframill.CommandCancelled, inframill.StateFailed:
				code = connect.CodeInternal
			}
		}
	}
	return connect.NewError(code, err)
}

func invalid(message string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(message))
}
