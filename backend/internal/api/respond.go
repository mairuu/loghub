package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

type requestIDKey struct{}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Crockford's alphabet, which is in ASCII order, so IDs sort as their bytes do.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newRequestID is 16 characters: the time in milliseconds, so IDs sort by
// time in the log, then 32 random bits.
func newRequestID() string {
	var b [10]byte
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixMilli())<<16)
	rand.Read(b[6:])
	return idEncoding.EncodeToString(b[:])
}

// observe gives each request an ID, logs it once it is answered, and turns a
// panic into the 500 envelope.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		rec := &statusRecorder{ResponseWriter: w}

		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				s.logger.Error("handler panicked", "request_id", id, "panic", p, "stack", string(debug.Stack()))
				if rec.status == 0 {
					writeJSON(rec, http.StatusInternalServerError, gen.ErrorResponse{
						Code:      "internal_error",
						Message:   internalMessage,
						RequestID: &id,
					})
				}
			}

			// The container healthcheck calls healthz every few seconds.
			level := slog.LevelInfo
			if r.URL.Path == "/api/healthz" && rec.status == http.StatusOK {
				level = slog.LevelDebug
			}
			s.logger.LogAttrs(r.Context(), level, "request",
				slog.String("request_id", id),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.statusOrOK()),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			)
		}()

		next.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) statusOrOK() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

const internalMessage = "an unexpected error occurred"

func statusOf(kind errors.Kind) int {
	switch kind {
	case errors.KindMalformed:
		return http.StatusBadRequest
	case errors.KindInvalid:
		return http.StatusUnprocessableEntity
	case errors.KindUnauthenticated:
		return http.StatusUnauthorized
	case errors.KindForbidden:
		return http.StatusForbidden
	case errors.KindNotFound:
		return http.StatusNotFound
	case errors.KindConflict:
		return http.StatusConflict
	case errors.KindRateLimited:
		return http.StatusTooManyRequests
	case errors.KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// fail answers with the error envelope, with the status the error's kind
// implies.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.failWith(w, r, statusOf(errors.KindOf(err)), err)
}

// failWith answers with the error envelope and an explicit status, for the
// few statuses no error kind maps to.
func (s *Server) failWith(w http.ResponseWriter, r *http.Request, status int, err error) {
	id := requestID(r.Context())
	body := gen.ErrorResponse{
		Code:      errors.CodeOf(err),
		Message:   errors.MessageOf(err),
		RequestID: &id,
	}
	kind := errors.KindOf(err)
	if kind == errors.KindInternal {
		// The detail belongs in the log, not in front of the client.
		body.Message = internalMessage
	}
	if fields := errors.FieldsOf(err); status == http.StatusUnprocessableEntity && len(fields) > 0 {
		out := make([]gen.FieldError, len(fields))
		for i, f := range fields {
			out[i] = gen.FieldError{Field: f.Field, Code: f.Code, Message: f.Message}
		}
		body.Errors = &out
	}

	level, message := slog.LevelDebug, "request failed"
	switch {
	case status < http.StatusInternalServerError:
	case r.Context().Err() != nil:
		// Usually a collector that timed out and will send the batch again.
		level, message = slog.LevelWarn, "request cancelled by the client"
	case kind == errors.KindUnavailable:
		level = slog.LevelWarn
	default:
		level = slog.LevelError
	}
	attrs := append([]slog.Attr{
		slog.String("request_id", id),
		slog.String("code", body.Code),
		slog.String("error", err.Error()),
	}, errors.AttrsOf(err)...)
	s.logger.LogAttrs(r.Context(), level, message, attrs...)

	writeJSON(w, status, body)
}

// respond answers with body as JSON.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, status int, body any) {
	b, err := encode(body)
	if err != nil {
		s.fail(w, r, errors.Internalf(err, "cannot encode response"))
		return
	}
	writeBytes(w, status, b)
}

// writeJSON is for bodies that always encode.
func writeJSON(w http.ResponseWriter, status int, body any) {
	b, _ := encode(body)
	writeBytes(w, status, b)
}

func encode(body any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// A stored syslog line starts with <PRI>, which would otherwise read
	// <PRI>. The Content-Type keeps browsers from reading HTML.
	enc.SetEscapeHTML(false)
	err := enc.Encode(body)
	return buf.Bytes(), err
}

func writeBytes(w http.ResponseWriter, status int, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}
