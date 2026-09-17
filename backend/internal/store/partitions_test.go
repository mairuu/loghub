package store_test

import (
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

func TestMaintainPartitions(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)

	// The partitions follow the UTC date, so take it from the database.
	var today time.Time
	if err := db.Owner.QueryRow(t.Context(), `SELECT (now() AT TIME ZONE 'UTC')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	day := func(offset int) time.Time { return today.AddDate(0, 0, offset) }
	partition := func(offset int) string { return "events_" + day(offset).Format("20060102") }
	noon := func(offset int) time.Time { return day(offset).Add(12 * time.Hour) }

	// A day inside the window whose partition is missing, as when the backend
	// was down, so its events land in DEFAULT.
	ownerExec(t, db, "DROP TABLE "+partition(-3))
	// A partition from before the cutoff.
	ownerExec(t, db, fmt.Sprintf("CREATE TABLE %s PARTITION OF events FOR VALUES FROM ('%s') TO ('%s')",
		partition(-30), day(-30).Format(time.DateOnly), day(-29).Format(time.DateOnly)))

	insert(t, db,
		labeled(tenant, "moved", noon(-3), nil),
		labeled(tenant, "first_kept_day", noon(-7), nil),
		labeled(tenant, "expired_in_default", noon(-8), nil),
		labeled(tenant, "expired_partition", noon(-30), nil),
	)

	repo := store.NewEventRepo(store.New(db.App))
	// Just under seven days, which rounds up to seven.
	if err := repo.MaintainPartitions(t.Context(), 7*24*time.Hour-time.Hour); err != nil {
		t.Fatal(err)
	}

	for offset := -7; offset <= 2; offset++ {
		if !partitionExists(t, db, partition(offset)) {
			t.Errorf("partition %s is missing", partition(offset))
		}
	}
	if partitionExists(t, db, partition(-30)) {
		t.Errorf("partition %s was not dropped", partition(-30))
	}

	rows, err := db.Owner.Query(t.Context(),
		`SELECT rule_id, tableoid::regclass::text FROM events WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var label, table string
	if _, err := pgx.ForEachRow(rows, []any{&label, &table}, func() error {
		got[label] = table
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"moved": partition(-3), "first_kept_day": partition(-7)}
	if !maps.Equal(got, want) {
		t.Errorf("events are in %v, want %v", got, want)
	}

	// Nothing is left to do, so a second run changes nothing.
	if err := repo.MaintainPartitions(t.Context(), 7*24*time.Hour); err != nil {
		t.Errorf("second run: %v", err)
	}
}

func ownerExec(t *testing.T, db *storetest.DB, sql string) {
	t.Helper()
	if _, err := db.Owner.Exec(t.Context(), sql); err != nil {
		t.Fatal(err)
	}
}

func partitionExists(t *testing.T, db *storetest.DB, name string) bool {
	t.Helper()
	var exists bool
	if err := db.Owner.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}
