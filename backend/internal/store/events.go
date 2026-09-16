package store

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store/gen"
)

// NewEventParams is one normalized event, ready to insert. Nil fields are
// stored as NULL. internal/ingest builds these from vendor records.
//
// The fields match gen.InsertEventsParams one for one, in order, so Insert
// converts between the two; a schema change that breaks the match fails to
// compile.
type NewEventParams struct {
	Ts             time.Time
	TenantID       string
	Source         string
	Vendor         *string
	Product        *string
	EventType      *string
	EventSubtype   *string
	Severity       *int16
	Action         *string
	SrcIP          *netip.Addr
	SrcPort        *int32
	DstIP          *netip.Addr
	DstPort        *int32
	Protocol       *string
	UserName       *string
	Host           *string
	Process        *string
	URL            *string
	HTTPMethod     *string
	StatusCode     *int32
	RuleName       *string
	RuleID         *string
	CloudAccountID *string
	CloudRegion    *string
	CloudService   *string
	// Raw is a JSON object.
	Raw []byte
	// Tags may be nil, which is stored as an empty array.
	Tags []string
}

// insertChunk bounds how many events go to the server in one batch. A
// variable so tests can cross chunk boundaries with few events.
var insertChunk = 1000

type EventRepo struct{ store *Store }

func NewEventRepo(store *Store) *EventRepo { return &EventRepo{store: store} }

// Insert stores events in one transaction and reports, by position, why any
// of them were not stored: an unknown tenant, or a value the database
// refuses. The normalizer is meant to make the second impossible, but a
// refused event must not fail the others, since the collector would retry
// the whole batch forever. The returned error means nothing was stored.
func (r *EventRepo) Insert(ctx context.Context, events []NewEventParams) ([]error, error) {
	rejected := make([]error, len(events))
	if len(events) == 0 {
		return rejected, nil
	}

	known, err := r.knownTenants(ctx, events)
	if err != nil {
		return nil, err
	}

	// Group by tenant, keeping arrival order within each group.
	var tenants []string
	byTenant := map[string][]int{}
	for i, e := range events {
		if !known[e.TenantID] {
			rejected[i] = unknownTenant(e.TenantID)
			continue
		}
		if _, ok := byTenant[e.TenantID]; !ok {
			tenants = append(tenants, e.TenantID)
		}
		byTenant[e.TenantID] = append(byTenant[e.TenantID], i)
	}

	err = r.store.InTx(ctx, func(s *Store) error {
		for _, tenant := range tenants {
			// RLS checks every row against this, so a row for any other
			// tenant fails instead of landing in the wrong place.
			if err := s.setScope(ctx, Scope{TenantID: tenant}); err != nil {
				return err
			}
			for chunk := range slices.Chunk(byTenant[tenant], insertChunk) {
				if err := insertEvents(ctx, s, events, chunk, rejected); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rejected, nil
}

func (r *EventRepo) knownTenants(ctx context.Context, events []NewEventParams) (map[string]bool, error) {
	var ids []string
	for _, e := range events {
		if !slices.Contains(ids, e.TenantID) {
			ids = append(ids, e.TenantID)
		}
	}
	found, err := r.store.q.FilterTenantIDs(ctx, ids)
	if err != nil {
		return nil, pg.Wrap(err, "cannot look up tenants")
	}
	known := make(map[string]bool, len(found))
	for _, id := range found {
		known[id] = true
	}
	return known, nil
}

// insertEvents inserts the events at idx in a savepoint. If the database
// refuses one of them, it rolls the batch back and retries the events one at
// a time, so only the refused ones are rejected.
func insertEvents(ctx context.Context, s *Store, events []NewEventParams, idx []int, rejected []error) error {
	params := make([]gen.InsertEventsParams, len(idx))
	for k, i := range idx {
		params[k] = gen.InsertEventsParams(events[i])
		if params[k].Tags == nil {
			// The column is NOT NULL, and pgx sends a nil slice as NULL.
			params[k].Tags = []string{}
		}
	}
	tenant := events[idx[0]].TenantID

	err := s.InTx(ctx, func(s *Store) error { return execBatch(ctx, s, params) })
	if err == nil || refusal(err, tenant) == nil {
		return err
	}

	for k, i := range idx {
		err := s.InTx(ctx, func(s *Store) error { return execBatch(ctx, s, params[k:k+1]) })
		if err == nil {
			continue
		}
		if rejected[i] = refusal(err, tenant); rejected[i] == nil {
			return err
		}
	}
	return nil
}

// execBatch returns the first failure. Once one statement fails, the ones
// after it only report that the transaction is aborted.
func execBatch(ctx context.Context, s *Store, params []gen.InsertEventsParams) error {
	var first error
	s.q.InsertEvents(ctx, params).Exec(func(_ int, err error) {
		if first == nil && err != nil {
			first = err
		}
	})
	if first != nil {
		return pg.Wrap(first, "cannot insert events")
	}
	return nil
}

// refusal turns an error caused by the event's own values into the rejection
// to report for it, or returns nil when the failure is not about the event.
func refusal(err error, tenant string) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil
	}
	switch {
	case pgErr.Code == "23503": // foreign_key_violation: the tenant was deleted since the lookup
		return unknownTenant(tenant)
	case pgErr.Code == "54000", // program_limit_exceeded: a value too large to index
		strings.HasPrefix(pgErr.Code, "22"), // data_exception: bad encoding, JSON, numeric overflow
		strings.HasPrefix(pgErr.Code, "23"): // integrity_constraint_violation: a CHECK failed
		return errors.Invalid("unstorable_event", "the database refused a value in this event").Wrapping(pgErr)
	}
	return nil
}

func unknownTenant(id string) error {
	return errors.Invalid("unknown_tenant", fmt.Sprintf("tenant %q does not exist", id))
}
