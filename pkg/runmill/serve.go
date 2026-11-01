package runmill

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
)

// ServeOptions supplies the executable version and protocol streams. Args must be
// empty: the embedding executable consumes its configured prefix before Serve.
type ServeOptions struct {
	Version string
	Args    []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Serve handles one JSON request and returns an exit code without global I/O.
func Serve(ctx context.Context, implementation Provider, options ServeOptions) (code int) {
	if options.Stdin == nil {
		options.Stdin = strings.NewReader("")
	}
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	logs := &outputWriter{destination: options.Stderr}
	replies := &replyWriter{sink: &outputWriter{destination: options.Stdout}, limit: MaxValidationReplyBytes}
	fail := func(err error) int {
		detail := &ProviderError{Code: "PROVIDER_ERROR", Message: err.Error()}
		var typed *ProviderError
		if errors.As(err, &typed) && typed.Code != "" && typed.Message != "" {
			detail = typed
		}
		_ = replies.final(Reply{Type: "error", Code: detail.Code, Message: detail.Message})
		return 1
	}
	defer func() {
		if recover() != nil {
			code = fail(errors.New("provider implementation panicked"))
		}
	}()
	if len(options.Args) != 0 {
		return fail(errors.New("Provider dispatch requires a JSON request, without protocol arguments"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	data, err := io.ReadAll(io.LimitReader(options.Stdin, MaxRequestBytes+1))
	if err != nil {
		return fail(errors.New("cannot read Provider request"))
	}
	if len(data) > MaxRequestBytes {
		return fail(errors.New("provider request exceeds size limit"))
	}
	var request requestEnvelope
	if err := decode(data, &request); err != nil {
		return fail(err)
	}
	if request.ProtocolVersion != ProtocolVersion {
		return fail(errors.New("unsupported Provider request protocol"))
	}
	if len(request.Params) == 0 || request.Params[0] != '{' {
		return fail(errors.New("params must be an object"))
	}
	if request.Method == "run" {
		replies.limit = MaxExecutionReplyBytes
	}
	result, err := dispatch(ctx, implementation, options.Version, request, logs, replies.message)
	if err = errors.Join(err, logs.failure(), replies.failure(), ctx.Err()); err != nil {
		return fail(err)
	}
	var encoded json.RawMessage
	if result != nil {
		encoded, err = json.Marshal(result)
		if err != nil {
			return fail(errors.New("provider result is not JSON-serializable"))
		}
	}
	if err := replies.final(Reply{Type: "success", Data: encoded}); err != nil {
		return fail(err)
	}
	return 0
}

// outputWriter serializes callback writes and never retries after a failed write.
type outputWriter struct {
	mu          sync.Mutex
	destination io.Writer
	err         error
}

func (w *outputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	if len(data) == 0 {
		return 0, nil
	}
	n, err := w.destination.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}
func (w *outputWriter) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func dispatch(ctx context.Context, implementation Provider, version string, envelope requestEnvelope, logs io.Writer, message func(string) error) (any, error) {
	switch envelope.Method {
	case "info":
		if err := decode(envelope.Params, &struct{}{}); err != nil {
			return nil, err
		}
		definition, err := implementation.Definition()
		if err != nil {
			return nil, err
		}
		info := ProviderInfo{Name: definition.Name, Version: version, ProtocolVersion: ProtocolVersion, Commands: definition.Commands}
		return info, info.Validate()
	case "validate_settings":
		var request settingsParams
		if err := decode(envelope.Params, &request); err != nil {
			return nil, err
		}
		if err := request.validate(); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, implementation.ValidateSettings(ctx, request.Settings)
	case "run":
		var input runParams
		if err := decode(envelope.Params, &input); err != nil {
			return nil, err
		}
		if input.Secrets == nil {
			return nil, errors.New("secrets object is required for run")
		}
		secrets, err := decodeSecrets(input.Secrets)
		if err != nil {
			return nil, err
		}
		request := input.invocationParams
		if err := request.validate(); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return implementation.Execute(ctx, ExecutionContext{
			Diagnostics: logs, message: message, WorkspaceDir: request.WorkspaceDir,
			ArtifactDir: request.ArtifactDir, ExecutionID: request.ExecutionID, Settings: request.Settings,
		}, request.Command, Arguments(objectOrEmpty(request.Arguments)), secrets)
	default:
		return nil, errors.New("unknown Provider request method")
	}
}
