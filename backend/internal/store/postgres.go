package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store/gen"
)

// Store owns the database handles that repositories read and write through.
type Store struct {
	db conn
	q  *gen.Queries
}

// conn is the pool, or a transaction: something to query and to begin in.
type conn interface {
	pg.Beginner
	gen.DBTX
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{db: pool, q: gen.New(pool)}
}

// InTx runs fn against a Store whose queries all go through one transaction.
// Repositories must be built from the Store passed to fn; ones built from the
// outer Store still go through the outer handle. Called on a Store that is
// already in a transaction, InTx runs fn in a savepoint instead.
func (s *Store) InTx(ctx context.Context, fn func(*Store) error) error {
	return pg.InTx(ctx, s.db, func(tx pgx.Tx) error {
		return fn(&Store{db: tx, q: s.q.WithTx(tx)})
	})
}

// Scope is who a query runs for. Row-level security shows an admin every
// tenant's events, and anyone else only their own tenant's (ADR 0003).
type Scope struct {
	TenantID string
	Admin    bool
}

// AdminScope sees every tenant.
var AdminScope = Scope{Admin: true}

// InScope runs fn in a transaction whose row-level security context is scope.
func (s *Store) InScope(ctx context.Context, scope Scope, fn func(*Store) error) error {
	return s.InTx(ctx, func(s *Store) error {
		if err := s.setScope(ctx, scope); err != nil {
			return err
		}
		return fn(s)
	})
}

// setScope sets the context for the rest of the current transaction.
func (s *Store) setScope(ctx context.Context, scope Scope) error {
	err := s.q.SetTenantContext(ctx, gen.SetTenantContextParams{TenantID: scope.TenantID, IsAdmin: scope.Admin})
	if err != nil {
		return pg.Wrap(err, "cannot set tenant context")
	}
	return nil
}
