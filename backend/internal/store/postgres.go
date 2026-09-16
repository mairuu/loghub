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
	pool *pgxpool.Pool
	q    *gen.Queries
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: gen.New(pool)}
}

// InTx runs fn against a Store whose queries all go through one transaction.
// Repositories must be built from the Store passed to fn; ones built from the
// outer Store still go straight to the pool.
func (s *Store) InTx(ctx context.Context, fn func(*Store) error) error {
	return pg.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(&Store{pool: s.pool, q: s.q.WithTx(tx)})
	})
}
