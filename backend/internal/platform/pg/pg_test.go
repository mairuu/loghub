package pg

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

func TestWrap(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"connection lost", &pgconn.PgError{Code: "08006"}, "database_unavailable"},
		{"too many connections", &pgconn.PgError{Code: "53300"}, "database_unavailable"},
		{"disk full", &pgconn.PgError{Code: "53100"}, "database_unavailable"},
		{"server shutting down", fmt.Errorf("exec: %w", &pgconn.PgError{Code: "57P01"}), "database_unavailable"},
		{"deadline", context.DeadlineExceeded, "database_unavailable"},
		{"query canceled", &pgconn.PgError{Code: "57014"}, "internal_error"},
		{"syntax error", &pgconn.PgError{Code: "42601"}, "internal_error"},
		{"check violation", &pgconn.PgError{Code: "23514"}, "internal_error"},
		{"not a database error", fmt.Errorf("boom"), "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Wrap(tc.err, "doing something")
			if got := errors.CodeOf(err); got != tc.want {
				t.Errorf("code = %s, want %s", got, tc.want)
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("%v does not wrap %v", err, tc.err)
			}
		})
	}
}
