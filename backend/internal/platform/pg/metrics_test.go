package pg

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A pool opens no connection until it needs one, so this needs no database.
func TestPoolCollector(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/nothing?pool_max_conns=7")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	want := `
# HELP loghub_db_pool_acquired_connections Database connections in use.
# TYPE loghub_db_pool_acquired_connections gauge
loghub_db_pool_acquired_connections 0
# HELP loghub_db_pool_max_connections The most database connections the pool will open.
# TYPE loghub_db_pool_max_connections gauge
loghub_db_pool_max_connections 7
# HELP loghub_db_pool_acquires_total Connections taken from the pool.
# TYPE loghub_db_pool_acquires_total counter
loghub_db_pool_acquires_total 0
`
	c := NewPoolCollector(pool)
	if err := testutil.CollectAndCompare(c, strings.NewReader(want),
		"loghub_db_pool_acquired_connections", "loghub_db_pool_max_connections", "loghub_db_pool_acquires_total"); err != nil {
		t.Error(err)
	}
	if n := testutil.CollectAndCount(c); n != 8 {
		t.Errorf("collected %d metrics, want 8", n)
	}
	// promlint holds the names to Prometheus's conventions.
	if problems, err := testutil.CollectAndLint(c); err != nil || len(problems) > 0 {
		t.Errorf("lint: %v %v", problems, err)
	}
}
