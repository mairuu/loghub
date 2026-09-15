package errors

import (
	stderrors "errors"
	"fmt"
	"log/slog"
	"strings"
)

var (
	Is     = stderrors.Is
	As     = stderrors.As
	Join   = stderrors.Join
	Unwrap = stderrors.Unwrap
)

type Kind uint8

const (
	KindInternal Kind = iota
	KindMalformed
	KindInvalid
	KindUnauthenticated
	KindForbidden
	KindNotFound
	KindConflict
	KindRateLimited
	KindUnavailable
)

func (k Kind) String() string {
	switch k {
	case KindInternal:
		return "internal"
	case KindMalformed:
		return "malformed"
	case KindInvalid:
		return "invalid"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindForbidden:
		return "forbidden"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindRateLimited:
		return "rate_limited"
	case KindUnavailable:
		return "unavailable"
	default:
		return "unknown"
	}
}

type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Error struct {
	kind    Kind
	code    string
	message string
	fields  []FieldError
	attrs   []slog.Attr
	wrapped error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.kind.String())
	b.WriteString(": ")
	b.WriteString(e.code)
	if e.message != "" {
		b.WriteString(": ")
		b.WriteString(e.message)
	}
	if e.wrapped != nil {
		b.WriteString(": ")
		b.WriteString(e.wrapped.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.wrapped }

func (e *Error) Is(target error) bool {
	var t *Error
	if !stderrors.As(target, &t) {
		return false
	}
	return e.kind == t.kind && e.code == t.code
}
func (e *Error) Kind() Kind           { return e.kind }
func (e *Error) Code() string         { return e.code }
func (e *Error) Message() string      { return e.message }
func (e *Error) Fields() []FieldError { return e.fields }
func (e *Error) Attrs() []slog.Attr   { return e.attrs }

// Wrapping attaches an underlying cause. The cause reaches logs, never clients.
func (e *Error) Wrapping(err error) *Error {
	c := *e
	c.wrapped = err
	return &c
}

// With attaches structured log attributes in slog's key/value form.
func (e *Error) With(args ...any) *Error {
	c := *e
	c.attrs = append(append([]slog.Attr(nil), e.attrs...), argsToAttrs(args)...)
	return &c
}

// WithFields attaches per-field validation failures. Only meaningful on
// KindInvalid, which is the only kind that renders errors[].
func (e *Error) WithFields(fields ...FieldError) *Error {
	c := *e
	c.fields = append(append([]FieldError(nil), e.fields...), fields...)
	return &c
}

// constructors

func newError(kind Kind, code, message string) *Error {
	return &Error{
		kind:    kind,
		code:    code,
		message: message,
	}
}

func Malformed(code, message string) *Error { return newError(KindMalformed, code, message) }
func Invalid(code, message string) *Error   { return newError(KindInvalid, code, message) }
func Unauthenticated(code, message string) *Error {
	return newError(KindUnauthenticated, code, message)
}
func Forbidden(code, message string) *Error   { return newError(KindForbidden, code, message) }
func NotFound(code, message string) *Error    { return newError(KindNotFound, code, message) }
func Conflict(code, message string) *Error    { return newError(KindConflict, code, message) }
func RateLimited(code, message string) *Error { return newError(KindRateLimited, code, message) }
func Unavailable(code, message string) *Error { return newError(KindUnavailable, code, message) }

// Internal takes no code: every internal failure renders as internal_error
// with nothing but a request ID. The detail belongs in the logs.
func Internal(message string) *Error {
	return newError(KindInternal, "internal_error", message)
}

// Internalf is the common shape for an unexpected failure from a dependency.
func Internalf(err error, format string, args ...any) *Error {
	return Internal(fmt.Sprintf(format, args...)).Wrapping(err)
}

// KindOf walks the chain for the outermost declared Kind.
func KindOf(err error) Kind {
	var e *Error
	if As(err, &e) {
		return e.kind
	}
	return KindInternal
}

// CodeOf returns the stable client-facing code, or internal_error.
func CodeOf(err error) string {
	var e *Error
	if As(err, &e) && e.code != "" {
		return e.code
	}
	return "internal_error"
}

// MessageOf returns the human-facing message, which callers must not match on.
func MessageOf(err error) string {
	var e *Error
	if As(err, &e) {
		return e.message
	}
	return ""
}

// FieldsOf returns per-field validation failures, if any.
func FieldsOf(err error) []FieldError {
	var e *Error
	if As(err, &e) {
		return e.fields
	}
	return nil
}

// AttrsOf returns the log attributes attached with With, if any.
func AttrsOf(err error) []slog.Attr {
	var e *Error
	if As(err, &e) {
		return e.attrs
	}
	return nil
}

func argsToAttrs(args []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(args)/2)
	for i := 0; i < len(args); i++ {
		switch a := args[i].(type) {
		case slog.Attr:
			attrs = append(attrs, a)
		case string:
			if i+1 < len(args) {
				attrs = append(attrs, slog.Any(a, args[i+1]))
				i++
			}
		}
	}
	return attrs
}
