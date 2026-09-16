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
	db pg.Beginner
	q  *gen.Queries
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
