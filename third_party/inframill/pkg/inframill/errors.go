package inframill

func MissingArgument(path string) error { return InvalidArgument(path, path+" is required") }

func InvalidArgument(path, message string) error {
	return &Error{Code: InvalidRequest, Path: path, Message: message}
}

// ErrorCode is a stable machine-readable failure category.
type ErrorCode string

const (
	CommandInterrupted ErrorCode = "COMMAND_INTERRUPTED"
	InvalidRequest     ErrorCode = "INVALID_REQUEST"
	InvalidInstanceID  ErrorCode = "INVALID_INSTANCE_ID"
	InvalidConfig      ErrorCode = "INVALID_CONFIG"
	BlueprintNotFound  ErrorCode = "BLUEPRINT_NOT_FOUND"
	CommandFailed      ErrorCode = "COMMAND_FAILED"
	VerificationFailed ErrorCode = "VERIFICATION_FAILED"
	CommandCancelled   ErrorCode = "COMMAND_CANCELLED"
	StateFailed        ErrorCode = "STATE_FAILED"
)

// Error is the public typed engine failure.
type Error struct {
	Code    ErrorCode
	Message string
	Path    string
	Cause   error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Cause }
