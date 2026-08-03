package failure

import "fmt"

const (
	ExitOK         = 0
	ExitGeneral    = 1
	ExitConfig     = 2
	ExitUsage      = 3
	ExitDependency = 4
	ExitConflict   = 5
)

// Error is a user-facing failure with a stable machine code and process exit code.
type Error struct {
	Code     string
	Message  string
	Hint     string
	ExitCode int
	Data     any
	Cause    error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

func New(code, message string, exitCode int) *Error {
	return &Error{Code: code, Message: message, ExitCode: exitCode}
}

func Wrap(code, message string, exitCode int, cause error) *Error {
	return &Error{Code: code, Message: message, ExitCode: exitCode, Cause: cause}
}

func WithHint(err *Error, hint string) *Error {
	err.Hint = hint
	return err
}

func WithData(err *Error, data any) *Error {
	err.Data = data
	return err
}

func As(err error) *Error {
	if typed, ok := err.(*Error); ok {
		return typed
	}
	return &Error{
		Code:     "UNEXPECTED_ERROR",
		Message:  "cfdev encountered an unexpected error",
		ExitCode: ExitGeneral,
		Cause:    err,
	}
}
